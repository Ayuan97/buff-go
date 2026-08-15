package collection

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"buff-go/internal/market"
)

type fakeInstanceGuard struct {
	mu          sync.Mutex
	held        bool
	releases    int
	failErr     error
	verifyErr   error
	blockVerify bool
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

type fakeInstanceLock struct {
	guard *fakeInstanceGuard
}

func (lock *fakeInstanceLock) Verify(ctx context.Context) error {
	lock.guard.mu.Lock()
	blocking, err := lock.guard.blockVerify, lock.guard.verifyErr
	lock.guard.mu.Unlock()
	if blocking {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (lock *fakeInstanceLock) Release(ctx context.Context) error {
	lock.guard.mu.Lock()
	defer lock.guard.mu.Unlock()
	lock.guard.held = false
	lock.guard.releases++
	return nil
}

type daemonHarness struct {
	*scheduleHarness
	daemon    *Daemon
	guard     *fakeInstanceGuard
	mu        sync.Mutex
	cycles    []DaemonCycle
	recovery  RecoveryReport
	recovered bool
}

func newDaemonHarness(t *testing.T, interval time.Duration, configure func(config *SchedulerConfig)) *daemonHarness {
	t.Helper()
	harness := &daemonHarness{
		scheduleHarness: newScheduleHarness(t, configure),
		guard:           &fakeInstanceGuard{},
	}
	daemon, err := NewDaemon(harness.scheduler, DaemonConfig{
		Interval:          interval,
		ShutdownTimeout:   5 * time.Second,
		LockVerifyTimeout: 5 * time.Second,
		Guard:             harness.guard,
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

func TestDaemonRefusesToRunWithoutInstanceLock(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	stop := harness.start(t)
	waitFor(t, "the running daemon to take the instance lock", func() bool {
		return len(harness.recorded()) >= 1
	})

	second, err := NewDaemon(harness.scheduler, DaemonConfig{Interval: time.Millisecond,
		ShutdownTimeout: time.Second, LockVerifyTimeout: time.Second, Guard: harness.guard})
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
		ShutdownTimeout: time.Second, LockVerifyTimeout: time.Second, Guard: harness.guard})
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
		ShutdownTimeout: time.Second, LockVerifyTimeout: 20 * time.Millisecond, Guard: guard})
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

func TestDaemonFailsWhenInstanceLockErrors(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.guard.failErr = errors.New("lock store is unavailable")
	if err := harness.daemon.Run(context.Background()); err == nil {
		t.Fatal("daemon must not start when the instance lock cannot be evaluated")
	}
}

func TestNewDaemonValidation(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	valid := DaemonConfig{Interval: time.Second, ShutdownTimeout: time.Second,
		LockVerifyTimeout: time.Second, Guard: &fakeInstanceGuard{}}
	if _, err := NewDaemon(nil, valid); err == nil {
		t.Fatal("nil scheduler must be rejected")
	}
	broken := valid
	broken.Interval = 0
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("non-positive interval must be rejected")
	}
	broken = valid
	broken.Guard = nil
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("missing instance guard must be rejected")
	}
}
