package collection

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/market"
	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

type fakeInstanceGuard struct {
	mu           sync.Mutex
	held         bool
	releases     int
	verifies     int
	verifyFailed int
	failErr      error
	verifyErr    error
	blockVerify  bool
	blockAt      int
	verifyEnter  chan struct{}
	verifyResume chan struct{}
	releaseErr   error
	blockRelease bool
}

func (store *fakeScheduleStore) PurgeExpiredPriceTicks(context.Context, time.Time) error {
	return nil
}

type fakeDaemonStore struct {
	*fakeScheduleStore
	mu               sync.Mutex
	purgeTimes       []time.Time
	purgeErr         error
	purgeHook        func(context.Context) error
	targetsHook      func(context.Context) error
	purgeHasDeadline bool
	purgeContextErr  error
}

func (store *fakeDaemonStore) PurgeExpiredPriceTicks(ctx context.Context, now time.Time) error {
	store.mu.Lock()
	store.purgeTimes = append(store.purgeTimes, now)
	_, store.purgeHasDeadline = ctx.Deadline()
	store.purgeContextErr = ctx.Err()
	hook, err := store.purgeHook, store.purgeErr
	store.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return err
}

func (store *fakeDaemonStore) Targets(ctx context.Context) ([]Target, error) {
	store.mu.Lock()
	hook := store.targetsHook
	store.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return nil, err
		}
	}
	return store.fakeScheduleStore.Targets(ctx)
}

func (store *fakeDaemonStore) purgeCalls() []time.Time {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]time.Time(nil), store.purgeTimes...)
}

func (guard *fakeInstanceGuard) breakLock(err error) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	guard.verifyErr = err
}

func (guard *fakeInstanceGuard) AcquireInstanceLock(ctx context.Context) (InstanceLock, bool, error) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.failErr != nil {
		return nil, false, guard.failErr
	}
	if guard.held {
		return nil, false, nil
	}
	guard.held = true
	return &fakeInstanceLock{guard: guard}, true, nil
}

func (guard *fakeInstanceGuard) releaseCount() int {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.releases
}

func (guard *fakeInstanceGuard) verifyFailureCount() int {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.verifyFailed
}

type fakeInstanceLock struct {
	guard *fakeInstanceGuard
}

func (lock *fakeInstanceLock) Verify(ctx context.Context) error {
	lock.guard.mu.Lock()
	lock.guard.verifies++
	call := lock.guard.verifies
	blocking, err := lock.guard.blockVerify, lock.guard.verifyErr
	blockAt, entered, resume := lock.guard.blockAt, lock.guard.verifyEnter, lock.guard.verifyResume
	if err != nil {
		lock.guard.verifyFailed++
	}
	lock.guard.mu.Unlock()
	if blockAt == call {
		if entered != nil {
			close(entered)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-resume:
			return nil
		}
	}
	if blocking {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (lock *fakeInstanceLock) Release(ctx context.Context) error {
	lock.guard.mu.Lock()
	blocking, err := lock.guard.blockRelease, lock.guard.releaseErr
	lock.guard.mu.Unlock()
	if blocking {
		<-ctx.Done()
		err = ctx.Err()
	}
	lock.guard.mu.Lock()
	lock.guard.held = false
	lock.guard.releases++
	lock.guard.mu.Unlock()
	return err
}

type daemonHarness struct {
	*scheduleHarness
	daemon      *Daemon
	maintenance *fakeDaemonStore
	guard       *fakeInstanceGuard
	mu          sync.Mutex
	cycles      []DaemonCycle
	recovery    RecoveryReport
	recovered   bool
}

func newDaemonHarness(t *testing.T, interval time.Duration, configure func(config *SchedulerConfig)) *daemonHarness {
	t.Helper()
	scheduleHarness := newScheduleHarness(t, configure)
	maintenance := &fakeDaemonStore{fakeScheduleStore: scheduleHarness.store}
	scheduleHarness.scheduler.store = maintenance
	harness := &daemonHarness{
		scheduleHarness: scheduleHarness,
		maintenance:     maintenance,
		guard:           &fakeInstanceGuard{},
	}
	daemon, err := NewDaemon(harness.scheduler, DaemonConfig{
		Interval:                     interval,
		PriceTickMaintenanceInterval: 24 * time.Hour,
		ShutdownTimeout:              5 * time.Second,
		LockVerifyTimeout:            5 * time.Second,
		Guard:                        harness.guard,
		Observer: func(cycle DaemonCycle) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			harness.cycles = append(harness.cycles, cycle)
		},
		RecoveryObserver: func(report RecoveryReport) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			harness.recovery, harness.recovered = report, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.daemon = daemon
	return harness
}

func (harness *daemonHarness) recoveryReport() (RecoveryReport, bool) {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return harness.recovery, harness.recovered
}

func (harness *daemonHarness) recorded() []DaemonCycle {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return append([]DaemonCycle(nil), harness.cycles...)
}

func (harness *daemonHarness) start(t *testing.T) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- harness.daemon.Run(ctx) }()
	return func() {
		t.Helper()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("daemon must exit cleanly on cancel, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("daemon did not stop after cancel")
		}
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDaemonRunsRepeatedCyclesUntilCanceled(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	stop := harness.start(t)
	waitFor(t, "two cycles", func() bool { return len(harness.recorded()) >= 2 })
	stop()
	if calls := harness.maintenance.purgeCalls(); len(calls) != 1 {
		t.Fatalf("price tick maintenance calls = %d, want 1", len(calls))
	}
}

func TestDaemonFastWorkerRefillsWhileAnotherWorkerIsSlow(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.3"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	slowStarted := make(chan struct{}, 1)
	releaseSlow := make(chan struct{})
	fastStarted := make(chan struct{}, 3)
	harness.fetcher.hook = func(request PageFetch) error {
		if request.Lease.Snapshot.CombinationID == 1 {
			select {
			case slowStarted <- struct{}{}:
			default:
			}
			<-releaseSlow
			return nil
		}
		fastStarted <- struct{}{}
		return nil
	}
	released := false
	stop := harness.start(t)
	defer func() {
		if !released {
			close(releaseSlow)
		}
		stop()
	}()

	select {
	case <-slowStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("slow worker did not start")
	}
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case <-fastStarted:
		case <-time.After(5 * time.Second):
			t.Fatalf("fast worker did not start request %d", attempt+1)
		}
	}
	close(releaseSlow)
	released = true
}

func TestDaemonAlternatesIndependentEndpointLanes(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, func(config *SchedulerConfig) {
		profile := config.Profiles[PlatformSteam]
		profile.BidEndpoint = "market_orderbook"
		config.Profiles[PlatformSteam] = profile
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.mu.Lock()
	harness.store.products = []catalog.SteamProduct{{ProductID: 1, AppID: 730, Name: "AK-47 | Redline"}}
	harness.store.mu.Unlock()
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideBid, DesiredEnabled)
	sides := make(chan market.Side, 4)
	harness.fetcher.hook = func(request PageFetch) error {
		select {
		case sides <- request.Side:
		default:
		}
		return nil
	}
	stop := harness.start(t)
	defer stop()

	got := make([]market.Side, 0, 4)
	for len(got) < 4 {
		select {
		case side := <-sides:
			got = append(got, side)
		case <-time.After(5 * time.Second):
			t.Fatalf("endpoint lane sequence = %v", got)
		}
	}
	for index := 1; index < len(got); index++ {
		if got[index] == got[index-1] {
			t.Fatalf("endpoint lanes did not alternate: %v", got)
		}
	}
}

func TestDaemonCooldownLeavesOtherEndpointRunnable(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, func(config *SchedulerConfig) {
		profile := config.Profiles[PlatformSteam]
		profile.BidEndpoint = "market_orderbook"
		config.Profiles[PlatformSteam] = profile
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.mu.Lock()
	harness.store.products = []catalog.SteamProduct{{ProductID: 1, AppID: 730, Name: "AK-47 | Redline"}}
	harness.store.mu.Unlock()
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideBid, DesiredEnabled)
	retryAt := scheduleNow().Add(time.Hour)
	harness.admitter.script = func(_ int, request ratelimit.Request) (ratelimit.Decision, error) {
		if request.EndpointClass() == "market_summary" {
			blocker, err := ratelimit.NewBlocker(1, ratelimit.ScopeAccountIP, ratelimit.BlockReasonCooldown, retryAt)
			if err != nil {
				return ratelimit.Decision{}, err
			}
			return ratelimit.Block([]ratelimit.Blocker{blocker})
		}
		return harness.admitter.allow(request)
	}
	fetched := make(chan market.Side, 1)
	harness.fetcher.hook = func(request PageFetch) error {
		select {
		case fetched <- request.Side:
		default:
		}
		return nil
	}
	stop := harness.start(t)
	defer stop()
	select {
	case side := <-fetched:
		if side != market.SideBid {
			t.Fatalf("cooled ask let side %q run", side)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask cooldown stopped the independent bid lane")
	}
}

func TestDaemonWakesWhenExactCooldownExpires(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	retryAt := scheduleNow().Add(80 * time.Millisecond)
	harness.admitter.script = func(index int, request ratelimit.Request) (ratelimit.Decision, error) {
		if index == 0 {
			blocker, err := ratelimit.NewBlocker(1, ratelimit.ScopeAccountIP, ratelimit.BlockReasonCooldown, retryAt)
			if err != nil {
				return ratelimit.Decision{}, err
			}
			return ratelimit.Block([]ratelimit.Blocker{blocker})
		}
		return harness.admitter.allow(request)
	}
	fetched := make(chan struct{}, 1)
	harness.fetcher.hook = func(PageFetch) error {
		select {
		case fetched <- struct{}{}:
		default:
		}
		return nil
	}
	stop := harness.start(t)
	defer stop()
	time.Sleep(25 * time.Millisecond)
	harness.admitter.mu.Lock()
	earlyAdmissions := len(harness.admitter.requests)
	harness.admitter.mu.Unlock()
	if earlyAdmissions != 1 {
		t.Fatalf("cooldown caused %d early admissions, want 1", earlyAdmissions)
	}
	select {
	case <-fetched:
	case <-time.After(time.Second):
		t.Fatal("expired cooldown waited for the one-hour planning interval")
	}
}

func TestDaemonCancelRequeuesInFlightTaskAndReleasesLease(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	started := make(chan struct{}, 1)
	harness.fetcher.hook = func(request PageFetch) error {
		started <- struct{}{}
		<-request.Lease.Context().Done()
		return request.Lease.Context().Err()
	}
	stop := harness.start(t)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("resident worker did not start")
	}
	stop()
	if harness.store.claimedCount() != 0 || harness.store.queuedCount() != QueueWatermark {
		t.Fatalf("claimed=%d queued=%d", harness.store.claimedCount(), harness.store.queuedCount())
	}
	component, err := harness.coordinator.RegisterComponent()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := harness.coordinator.AcquireCombination(
		t.Context(), component, 1, resource.TargetRegionDomestic, scheduleNow(),
	)
	if err != nil {
		t.Fatalf("resident lease was not released: %v", err)
	}
	if err := harness.coordinator.Release(lease.Token); err != nil {
		t.Fatal(err)
	}
	if err := harness.coordinator.CancelComponent(t.Context(), component); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonSchedulesPriceTickMaintenanceAtExplicitInterval(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	harness := newDaemonHarness(t, time.Hour, func(config *SchedulerConfig) {
		config.Clock = func() time.Time { return now }
	})
	harness.daemon.config.PriceTickMaintenanceInterval = time.Hour

	next, err := harness.daemon.maintainPriceTicks(context.Background(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Minute)
	if _, err := harness.daemon.maintainPriceTicks(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if calls := harness.maintenance.purgeCalls(); len(calls) != 1 {
		t.Fatalf("maintenance before interval calls = %d, want 1", len(calls))
	}

	now = now.Add(time.Minute)
	harness.maintenance.mu.Lock()
	harness.maintenance.purgeErr = errors.New("tick store is unavailable")
	harness.maintenance.mu.Unlock()
	next, err = harness.daemon.maintainPriceTicks(context.Background(), next)
	if err == nil {
		t.Fatal("maintenance storage failure must be reported")
	}
	if !next.Before(now.Add(time.Hour)) {
		t.Fatalf("failed maintenance next retry = %v", next)
	}
	if _, err := harness.daemon.maintainPriceTicks(context.Background(), next); err != nil {
		t.Fatalf("failed maintenance must not retry before the next interval: %v", err)
	}
	if calls := harness.maintenance.purgeCalls(); len(calls) != 2 {
		t.Fatalf("maintenance calls after failure = %d, want 2", len(calls))
	}
	now = next
	harness.maintenance.mu.Lock()
	harness.maintenance.purgeErr = nil
	harness.maintenance.mu.Unlock()
	if _, err := harness.daemon.maintainPriceTicks(context.Background(), next); err != nil {
		t.Fatalf("maintenance did not retry after short backoff: %v", err)
	}
	harness.maintenance.mu.Lock()
	hasDeadline := harness.maintenance.purgeHasDeadline
	contextErr := harness.maintenance.purgeContextErr
	harness.maintenance.mu.Unlock()
	if !hasDeadline || contextErr != nil {
		t.Fatalf("maintenance context deadline=%v err=%v", hasDeadline, contextErr)
	}
	if calls := harness.maintenance.purgeCalls(); len(calls) != 3 {
		t.Fatalf("maintenance calls after retry = %d, want 3", len(calls))
	}
}

func TestDaemonDisabledDirectionStopsWhileOthersContinue(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	keep := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	stopTarget := harness.store.addSummaryTarget(t, PlatformSteam, 252490, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, stopTarget.ID())

	stop := harness.start(t)
	waitFor(t, "stopped target and continuing refill", func() bool {
		if harness.store.target(stopTarget.ID()).Actual() != ActualStopped {
			return false
		}
		depth, err := harness.store.QueueDepth(context.Background(), keep.ID())
		return err == nil && depth > 0
	})
	stop()
}

func TestDaemonWaitsForInFlightDirectionBeforeStopping(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	harness.fetcher.hook = func(PageFetch) error {
		started <- struct{}{}
		<-release
		return nil
	}
	released := false
	stop := harness.start(t)
	defer func() {
		if !released {
			close(release)
		}
		stop()
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	harness.store.disable(t, target.ID())
	waitFor(t, "busy stop to be skipped", func() bool {
		for _, cycle := range harness.recorded() {
			for _, skipped := range cycle.Skipped {
				if skipped == target.ID() {
					return true
				}
			}
		}
		return false
	})
	if harness.store.target(target.ID()).Actual() != ActualStopping {
		t.Fatal("busy target stopped before its worker released")
	}
	close(release)
	released = true
	waitFor(t, "target to stop after worker release", func() bool {
		return harness.store.target(target.ID()).Actual() == ActualStopped
	})
}

func TestDaemonRecoverReleasesFreshClaims(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.seedClaimedTask(t, target.ID(), 1, scheduleNow())

	report, err := harness.daemon.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ReleasedClaims != 1 {
		t.Fatalf("recovery = %+v", report)
	}
	if harness.store.claimedCount() != 0 || harness.store.queuedCount() != 1 {
		t.Fatalf("claimed=%d queued=%d", harness.store.claimedCount(), harness.store.queuedCount())
	}
}

func TestDaemonRecoverCompletesInterruptedStop(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.seedClaimedTask(t, target.ID(), 1, scheduleNow())
	harness.store.disable(t, target.ID())

	report, err := harness.daemon.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.StoppedTargets) != 1 || report.StoppedTargets[0] != target.ID() {
		t.Fatalf("recovery = %+v", report)
	}
	if harness.store.target(target.ID()).Actual() != ActualStopped {
		t.Fatal("stopping target must become stopped")
	}
	depth, err := harness.store.QueueDepth(context.Background(), target.ID())
	if err != nil || depth != 0 {
		t.Fatalf("queue must be cleared, depth=%d err=%v", depth, err)
	}
}

func TestDaemonReportsRecoveryToObserver(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, target.ID())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := harness.daemon.Run(ctx); err != nil {
		t.Fatal(err)
	}
	report, ok := harness.recoveryReport()
	if !ok || len(report.StoppedTargets) != 1 {
		t.Fatalf("recovery observer = %+v ok=%v", report, ok)
	}
}

func TestDaemonRunReadySignalsAfterRecoveryExactlyOnce(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.seedClaimedTask(t, target.ID(), 1, scheduleNow())
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan error, 2)
	done := make(chan error, 1)
	go func() { done <- harness.daemon.RunReady(ctx, ready) }()

	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("daemon readiness failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not report readiness")
	}
	if _, recovered := harness.recoveryReport(); !recovered {
		t.Fatal("daemon reported ready before recovery was observed")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon must exit cleanly after cancel, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after cancel")
	}
	select {
	case err := <-ready:
		t.Fatalf("daemon reported readiness more than once: %v", err)
	default:
	}
}

func TestDaemonRunReadyReportsRecoveryFailure(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.store.mu.Lock()
	harness.store.failReleaseErr = errors.New("claim store is unavailable")
	harness.store.mu.Unlock()
	ready := make(chan error, 2)
	done := make(chan error, 1)
	go func() { done <- harness.daemon.RunReady(context.Background(), ready) }()

	select {
	case err := <-ready:
		if err == nil {
			t.Fatal("recovery failure must fail readiness")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not report recovery failure")
	}
	if err := <-done; err == nil {
		t.Fatal("daemon must return the recovery failure")
	}
	if harness.guard.releaseCount() != 1 {
		t.Fatalf("startup failure must release the instance lock once, got %d", harness.guard.releaseCount())
	}
	select {
	case err := <-ready:
		t.Fatalf("daemon reported readiness more than once: %v", err)
	default:
	}
}

func TestDaemonRunReadyVerifiesLockBeforeReady(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.guard.verifyErr = errors.New("lock session is gone")
	ready := make(chan error, 1)
	done := make(chan error, 1)
	go func() { done <- harness.daemon.RunReady(context.Background(), ready) }()

	if err := <-ready; err == nil {
		t.Fatal("daemon reported ready without proving the instance lock")
	}
	if err := <-done; err == nil {
		t.Fatal("daemon must return the startup lock verification failure")
	}
}

func TestDaemonRunReadyRejectsLockLostDuringRecovery(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	recoveryEntered := make(chan struct{})
	harness.maintenance.mu.Lock()
	harness.maintenance.targetsHook = func(ctx context.Context) error {
		close(recoveryEntered)
		<-ctx.Done()
		return nil
	}
	harness.maintenance.mu.Unlock()
	ready := make(chan error, 1)
	done := make(chan error, 1)
	go func() { done <- harness.daemon.RunReady(context.Background(), ready) }()

	select {
	case <-recoveryEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not enter target loading")
	}
	harness.guard.breakLock(errors.New("lock session is gone"))
	select {
	case err := <-ready:
		if err == nil {
			t.Fatal("daemon reported ready after losing its lock during recovery")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("daemon did not reject readiness after lock loss")
	}
	if err := <-done; err == nil {
		t.Fatal("daemon must return the recovery-time lock failure")
	}
}

func TestDaemonRunReadyReportsStopRecoveryFailure(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, target.ID())
	harness.store.mu.Lock()
	harness.store.failClearErr = errors.New("queue store is unavailable")
	harness.store.mu.Unlock()
	ready := make(chan error, 1)
	done := make(chan error, 1)
	go func() { done <- harness.daemon.RunReady(context.Background(), ready) }()

	if err := <-ready; err == nil {
		t.Fatal("daemon reported ready before stop recovery converged")
	}
	if err := <-done; err == nil {
		t.Fatal("daemon must return the stop recovery failure")
	}
	report, ok := harness.recoveryReport()
	if !ok || report.Failures == nil {
		t.Fatalf("recovery observer = %+v ok=%v", report, ok)
	}
}

func TestDaemonConvergesPendingStopsOnShutdown(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	stop := harness.start(t)
	waitFor(t, "the first cycle to finish", func() bool { return len(harness.recorded()) >= 1 })
	harness.store.disable(t, target.ID())
	stop()
	if harness.store.target(target.ID()).Actual() != ActualStopped {
		t.Fatal("a pending stop must converge before the daemon exits")
	}
}

func TestDaemonReportsScheduleFailure(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, target.ID())
	harness.store.mu.Lock()
	harness.store.failTargetsErr = errors.New("target store is unavailable")
	harness.store.failTargetsCalls = 1
	harness.store.mu.Unlock()

	cycle := harness.daemon.runCycle(context.Background())
	if cycle.Err == nil {
		t.Fatal("a failed schedule cycle must be reported")
	}
	if len(cycle.Stopped) != 1 || cycle.Stopped[0] != target.ID() {
		t.Fatalf("stop convergence must still run, got %+v", cycle.Stopped)
	}
}

func TestDaemonReportsStopConvergenceFailure(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	running := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	stopping := harness.store.addSummaryTarget(t, PlatformSteam, 252490, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, stopping.ID())
	harness.store.mu.Lock()
	harness.store.failClearErr = errors.New("queue store is unavailable")
	harness.store.mu.Unlock()

	cycle := harness.daemon.runCycle(context.Background())
	if cycle.Err == nil {
		t.Fatal("a failed stop convergence must be reported")
	}
	if len(cycle.Stopped) != 0 {
		t.Fatalf("no target may be reported stopped, got %+v", cycle.Stopped)
	}
	enqueued := false
	for _, outcome := range cycle.Report.Targets {
		if outcome.TargetID == running.ID() && outcome.Enqueued > 0 {
			enqueued = true
		}
	}
	if !enqueued {
		t.Fatal("the schedule cycle itself must still run")
	}
}

func TestDaemonSurvivesCycleFailure(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	stop := harness.start(t)
	waitFor(t, "the first cycle", func() bool { return len(harness.recorded()) >= 1 })
	harness.store.mu.Lock()
	harness.store.failTargetsErr = errors.New("target store is unavailable")
	harness.store.mu.Unlock()
	waitFor(t, "a failed cycle", func() bool {
		for _, cycle := range harness.recorded() {
			if cycle.Err != nil {
				return true
			}
		}
		return false
	})
	harness.store.mu.Lock()
	harness.store.failTargetsErr = nil
	harness.store.mu.Unlock()
	failed := len(harness.recorded())
	waitFor(t, "collection to resume after the failure", func() bool {
		for _, cycle := range harness.recorded()[failed:] {
			for _, worker := range cycle.Report.Workers {
				if worker.Committed {
					return true
				}
			}
		}
		return false
	})
	stop()
}

func TestDaemonRunFailsWhenRecoveryFails(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.mu.Lock()
	harness.store.failReleaseErr = errors.New("claim store is unavailable")
	harness.store.mu.Unlock()
	if err := harness.daemon.Run(context.Background()); err == nil {
		t.Fatal("daemon must not start when recovery cannot run")
	}
}

func TestDaemonExitsWhenInstanceLockIsLost(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- harness.daemon.Run(ctx) }()
	waitFor(t, "the daemon to start", func() bool { return len(harness.recorded()) >= 1 })
	harness.guard.breakLock(errors.New("lock session is gone"))

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("daemon must fail when it can no longer prove exclusivity")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon kept running without the instance lock")
	}
	if harness.guard.releaseCount() != 1 {
		t.Fatalf("the instance lock must be released once, got %d", harness.guard.releaseCount())
	}
}

func TestDaemonDetectsLockLossBetweenLongPlanningCycles(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, func(config *SchedulerConfig) {
		profile := config.Profiles[PlatformSteam]
		profile.RequestInterval = 20 * time.Millisecond
		config.Profiles[PlatformSteam] = profile
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- harness.daemon.Run(ctx) }()
	waitFor(t, "resident dispatch before lock loss", func() bool {
		harness.fetcher.mu.Lock()
		defer harness.fetcher.mu.Unlock()
		return len(harness.fetcher.calls) >= 2
	})
	harness.guard.breakLock(errors.New("lock session is gone"))
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("daemon ignored lock loss between planning cycles")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon waited for the one-hour planning interval to verify its lock")
	}
}

func TestDaemonWatchdogCancelsBlockedMaintenanceAfterLockLoss(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	maintenanceEntered := make(chan struct{})
	harness.maintenance.mu.Lock()
	harness.maintenance.purgeHook = func(ctx context.Context) error {
		close(maintenanceEntered)
		<-ctx.Done()
		return ctx.Err()
	}
	harness.maintenance.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- harness.daemon.Run(context.Background()) }()
	select {
	case <-maintenanceEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance did not start")
	}
	harness.guard.breakLock(errors.New("lock session is gone"))
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked maintenance hid instance lock loss")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("watchdog did not cancel blocked maintenance")
	}
}

func TestDaemonUserCancelDuringWatchdogVerifyIsClean(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.guard.mu.Lock()
	harness.guard.blockAt = 2
	harness.guard.verifyEnter = make(chan struct{})
	harness.guard.verifyResume = make(chan struct{})
	verifyEntered := harness.guard.verifyEnter
	verifyResume := harness.guard.verifyResume
	harness.guard.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- harness.daemon.Run(ctx) }()
	select {
	case <-verifyEntered:
	case <-time.After(4 * time.Second):
		t.Fatal("watchdog verification did not start")
	}
	cancel()
	close(verifyResume)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("user cancellation was misreported as lock loss: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after user cancellation")
	}
}

func TestDaemonSkipsFinalizeWhenLockIsLostDuringWorkerDrain(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	var entered sync.Once
	harness.fetcher.hook = func(PageFetch) error {
		entered.Do(func() { close(requestEntered) })
		<-releaseRequest
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- harness.daemon.Run(ctx) }()
	select {
	case <-requestEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("resident worker did not start")
	}
	harness.store.disable(t, target.ID())
	cancel()
	harness.guard.breakLock(errors.New("lock session is gone"))
	waitFor(t, "watchdog lock failure during drain", func() bool {
		return harness.guard.verifyFailureCount() > 0
	})
	close(releaseRequest)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lock loss during worker drain was ignored")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not finish worker drain")
	}
	if harness.store.target(target.ID()).Actual() != ActualStopping {
		t.Fatal("daemon finalized a target after losing its instance lock")
	}
}

func TestDaemonRefusesToRunWithoutInstanceLock(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	stop := harness.start(t)
	waitFor(t, "the running daemon to take the instance lock", func() bool {
		return len(harness.recorded()) >= 1
	})

	second, err := NewDaemon(harness.scheduler, DaemonConfig{Interval: time.Millisecond,
		PriceTickMaintenanceInterval: time.Hour, ShutdownTimeout: time.Second,
		LockVerifyTimeout: time.Second, Guard: harness.guard})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Run(context.Background()); !errors.Is(err, ErrInstanceLocked) {
		t.Fatalf("second resident scheduler must be refused, got %v", err)
	}
	stop()

	if harness.guard.releaseCount() != 1 {
		t.Fatalf("the instance lock must be released exactly once, got %d", harness.guard.releaseCount())
	}
	third, err := NewDaemon(harness.scheduler, DaemonConfig{Interval: time.Hour,
		PriceTickMaintenanceInterval: time.Hour, ShutdownTimeout: time.Second,
		LockVerifyTimeout: time.Second, Guard: harness.guard})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := third.Run(ctx); err != nil {
		t.Fatalf("successor must start after the lock is released, got %v", err)
	}
}

func TestDaemonSkipsShutdownConvergenceWithoutInstanceLock(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- harness.daemon.Run(ctx) }()
	waitFor(t, "the first cycle", func() bool { return len(harness.recorded()) >= 1 })
	harness.store.disable(t, target.ID())
	harness.guard.breakLock(errors.New("lock session is gone"))
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("losing the instance lock must surface as a failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after cancel")
	}
	if harness.store.target(target.ID()).Actual() != ActualStopping {
		t.Fatal("a daemon that cannot prove exclusivity must not write the stop back")
	}
}

func TestDaemonBoundsInstanceLockVerify(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	guard := &fakeInstanceGuard{blockVerify: true}
	daemon, err := NewDaemon(harness.scheduler, DaemonConfig{Interval: time.Hour,
		PriceTickMaintenanceInterval: time.Hour, ShutdownTimeout: time.Second,
		LockVerifyTimeout: 20 * time.Millisecond, Guard: guard})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- daemon.Run(context.Background()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a verify that never answers must fail the daemon")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("instance lock verification is not bounded by a timeout")
	}
}

func TestDaemonBoundsInstanceLockRelease(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	guard := &fakeInstanceGuard{blockRelease: true}
	daemon, err := NewDaemon(harness.scheduler, DaemonConfig{Interval: time.Hour,
		PriceTickMaintenanceInterval: time.Hour, ShutdownTimeout: time.Second,
		LockVerifyTimeout: 20 * time.Millisecond, Guard: guard})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a lock release that never answers must fail the daemon")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("instance lock release is not bounded by a timeout")
	}
}

func TestDaemonFailsWhenInstanceLockErrors(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.guard.failErr = errors.New("lock store is unavailable")
	if err := harness.daemon.Run(context.Background()); err == nil {
		t.Fatal("daemon must not start when the instance lock cannot be evaluated")
	}
}

func TestNewDaemonValidation(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	valid := DaemonConfig{Interval: time.Second, PriceTickMaintenanceInterval: time.Hour,
		ShutdownTimeout: time.Second, LockVerifyTimeout: time.Second, Guard: &fakeInstanceGuard{}}
	if _, err := NewDaemon(nil, valid); err == nil {
		t.Fatal("nil scheduler must be rejected")
	}
	broken := valid
	broken.Interval = 0
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("non-positive interval must be rejected")
	}
	broken = valid
	broken.PriceTickMaintenanceInterval = 0
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("non-positive maintenance interval must be rejected")
	}
	broken = valid
	broken.Guard = nil
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("missing instance guard must be rejected")
	}
}
