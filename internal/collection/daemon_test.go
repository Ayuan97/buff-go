package collection

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"buff-go/internal/market"
	"buff-go/internal/resource"
)

// fakeInstanceGuard 以进程内互斥模拟单实例栅栏。
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

// daemonHarness 在调度器测试装置上补充常驻循环所需的观测点。
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
		MaxResumeAge:      time.Hour,
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

// runOutcomes 汇总全部已观测周期中的运行结果。
func (harness *daemonHarness) runOutcomes() []RunOutcome {
	outcomes := make([]RunOutcome, 0)
	for _, cycle := range harness.recorded() {
		for _, target := range cycle.Report.Targets {
			outcomes = append(outcomes, target.Runs...)
		}
	}
	return outcomes
}

// start 在后台启动常驻循环，返回的函数取消并等待其干净退出。
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

// runsForTarget 统计某个目标已经创建的运行数量。
func (store *fakeScheduleStore) runsForTarget(id TargetID) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, run := range store.runs {
		if targetID, ok := run.TargetID(); ok && targetID == id {
			count++
		}
	}
	return count
}

// requireCombinationFree 通过重新占用证明组合上没有残留占用。
func requireCombinationFree(t *testing.T, harness *scheduleHarness, id int64) {
	t.Helper()
	component, err := harness.coordinator.RegisterComponent()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = harness.coordinator.CancelComponent(context.Background(), component) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := harness.coordinator.AcquireCombination(ctx, component, resource.CombinationID(id),
		resource.TargetRegionDomestic, scheduleNow(), 730, market.SideAsk)
	if err != nil {
		t.Fatalf("combination %d is still occupied: %v", id, err)
	}
	if err := harness.coordinator.Release(lease.Token); err != nil {
		t.Fatal(err)
	}
}

// 常驻循环按间隔重复执行周期，取消后干净退出。
func TestDaemonRunsRepeatedCyclesUntilCanceled(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, func(config *SchedulerConfig) {
		config.SummaryPeriod = time.Millisecond
	})
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	stop := harness.start(t)
	waitFor(t, "three resident cycles", func() bool { return len(harness.recorded()) >= 3 })
	stop()

	committed := 0
	for index, cycle := range harness.recorded() {
		if cycle.Err != nil {
			t.Fatalf("cycle %d failed: %v", index, cycle.Err)
		}
		for _, outcome := range cycle.Report.Targets {
			for _, run := range outcome.Runs {
				committed += run.PagesCommitted
			}
		}
	}
	if committed < 3 {
		t.Fatalf("each resident cycle must commit a page, got %d", committed)
	}
	if runs := harness.store.runsForTarget(target.ID()); runs < 3 {
		t.Fatalf("each resident cycle must start a run, got %d", runs)
	}
	requireCombinationFree(t, harness.scheduleHarness, 1)
}

// 关闭一个方向后不再派发该方向的请求，另一方向继续运行。
func TestDaemonDisabledDirectionStopsWhileOthersContinue(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, func(config *SchedulerConfig) {
		config.SummaryPeriod = time.Millisecond
	})
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", 730, market.SideBid, netip.MustParseAddr("2.2.2.3"))
	ask := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	bid := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideBid, DesiredEnabled)

	// 先跑一轮建立基线，再关闭 ask 方向。
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	harness.store.disable(t, ask.ID())

	stop := harness.start(t)
	waitFor(t, "the disabled direction to stop", func() bool {
		return harness.store.target(ask.ID()).Actual() == ActualStopped
	})
	waitFor(t, "the other direction to keep running", func() bool {
		return harness.store.runsForTarget(bid.ID()) >= 2
	})
	stop()

	if runs := harness.store.runsForTarget(ask.ID()); runs != 1 {
		t.Fatalf("disabled direction must not start new runs, got %d runs", runs)
	}
}

// 禁用一个 appid 的摘要不影响另一个。
func TestDaemonDisabledCatalogDoesNotAffectOtherGames(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, func(config *SchedulerConfig) {
		config.SummaryPeriod = time.Millisecond
	})
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", 252490, market.SideAsk, netip.MustParseAddr("2.2.2.3"))
	first := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	second := harness.store.addSummaryTarget(t, PlatformSteam, 252490, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, first.ID())

	stop := harness.start(t)
	waitFor(t, "the disabled summary target to stop", func() bool {
		return harness.store.target(first.ID()).Actual() == ActualStopped
	})
	waitFor(t, "the other summary target to run", func() bool {
		return harness.store.runsForTarget(second.ID()) >= 1
	})
	stop()

	if runs := harness.store.runsForTarget(first.ID()); runs != 0 {
		t.Fatalf("disabled summary target must not run, got %d runs", runs)
	}
}

// 运行中途关闭目标：已提交页保留、运行终结为部分覆盖、占用释放。
func TestDaemonStopPreservesCommittedPages(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.pagesPerRun = 4

	var once sync.Once
	harness.fetcher.hook = func(request PageFetch) error {
		if request.PageSequence == 2 {
			once.Do(func() { harness.store.disable(t, target.ID()) })
		}
		return nil
	}

	stop := harness.start(t)
	waitFor(t, "the target to stop", func() bool {
		return harness.store.target(target.ID()).Actual() == ActualStopped
	})
	stop()

	stopped, ok := harness.store.latestRunForTarget(target.ID())
	if !ok {
		t.Fatal("the stopped target must have a run")
	}
	if pages := harness.store.pageCount(stopped.ID()); pages < 1 {
		t.Fatal("committed pages must survive the stop")
	}
	if !stopped.State().Terminal() || stopped.Completeness() != CompletenessPartial {
		t.Fatalf("stopped run must be terminal and partial, got %s/%s", stopped.State(), stopped.Completeness())
	}
	requireCombinationFree(t, harness.scheduleHarness, 1)
}

// 开关仍启用的临时失败在下一周期新建运行。
func TestDaemonTransientFailureStartsNewRunNextCycle(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	var once sync.Once
	harness.fetcher.hook = func(PageFetch) error {
		var failure error
		once.Do(func() { failure = fmt.Errorf("dial: %w", ErrFetchNetwork) })
		return failure
	}

	stop := harness.start(t)
	waitFor(t, "a retry run after the transient failure", func() bool {
		return len(harness.runOutcomes()) >= 2
	})
	stop()

	outcomes := harness.runOutcomes()
	if outcomes[0].State != RunFailed || outcomes[0].Reason != RunReasonNetworkError {
		t.Fatalf("first run must fail with a network error, got %+v", outcomes[0])
	}
	if outcomes[1].RunID == outcomes[0].RunID {
		t.Fatalf("transient failure must start a new run, got the same run %d", outcomes[0].RunID)
	}
	if outcomes[1].State != RunSucceeded {
		t.Fatalf("the retry run must succeed, got %+v", outcomes[1])
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualWaiting, TargetReasonNextCycle)
}

// 上一进程遗留的运行保留续点：重启不丢弃已提交的分页，也不重复消耗限频预算。
func TestDaemonRecoverResumesInterruptedRun(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.pagesPerRun = 3

	// 上一进程完成了第一页后中断：运行仍活动，目标停在 running。
	orphan, _, err := harness.store.CreateSummaryRun(context.Background(), target.ID(), target.SwitchVersion(), Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if orphan, err = harness.store.BeginRun(context.Background(), orphan.ID()); err != nil {
		t.Fatal(err)
	}
	cursor, err := NewCursor([]byte("app730-p1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := harness.store.CommitSummaryPage(context.Background(), SummaryPageCommit{
		RunID: orphan.ID(), PageSequence: 1, CursorBefore: Cursor{}, CursorAfter: cursor,
		CollectedAt: scheduleNow(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.store.TransitionTarget(context.Background(), target.ID(), target.Revision(), target.SwitchVersion(),
		TargetTransition{State: ActualRunning}); err != nil {
		t.Fatal(err)
	}

	report, err := harness.daemon.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ResumedRuns) != 1 || report.ResumedRuns[0] != orphan.ID() {
		t.Fatalf("interrupted run must keep its continuation, got %+v", report.ResumedRuns)
	}
	if len(report.FinishedRuns) != 0 {
		t.Fatalf("resumable runs must not be finished, got %+v", report.FinishedRuns)
	}

	// 下一周期续用同一运行，从存量游标继续第二页而不是重采第一页。
	cycle, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome := cycle.Targets[0].Runs[0]
	if outcome.RunID != orphan.ID() || outcome.State != RunSucceeded {
		t.Fatalf("recovery must resume the interrupted run, got %+v", outcome)
	}
	for _, call := range harness.fetcher.recordedCalls() {
		if call.pageSequence == 1 {
			t.Fatal("committed pages must not be collected again after a restart")
		}
	}
	if pages := harness.store.pageCount(orphan.ID()); pages != 3 {
		t.Fatalf("resumed run must cover every page exactly once, got %d", pages)
	}
}

// 开关已推进的遗留运行必须结束，否则活动运行唯一性会永久挡住新运行。
func TestDaemonRecoverFinishesUnresumableRuns(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	stale := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	staleRun, _, err := harness.store.CreateSummaryRun(context.Background(), stale.ID(), stale.SwitchVersion(), Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	// 关闭再启用推进开关版本，遗留运行属于旧的启用周期。
	harness.store.disable(t, stale.ID())
	harness.store.markStopped(t, stale.ID())
	harness.store.enable(t, stale.ID())

	report, err := harness.daemon.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FinishedRuns) != 1 || report.FinishedRuns[0] != staleRun.ID() {
		t.Fatalf("stale-switch run must be finished, got %+v", report.FinishedRuns)
	}
	// 这条运行是被运维的关闭终止的，不是崩溃，原因必须如实记录。
	stored, _ := harness.store.run(staleRun.ID())
	if stored.State() != RunStopped || stored.Reason() != RunReasonSwitchDisabled {
		t.Fatalf("stale run must record the stop, got %s/%s", stored.State(), stored.Reason())
	}
	// 结束旧运行后当前开关可以新建运行。
	current := harness.store.target(stale.ID())
	if _, created, err := harness.store.CreateSummaryRun(context.Background(), current.ID(), current.SwitchVersion(), Cursor{}); err != nil || !created {
		t.Fatalf("recovered target must accept a new run, created=%v err=%v", created, err)
	}
}

// 进程在关闭受理与完成之间中断时，启动恢复补齐 stopped。
func TestDaemonRecoverCompletesInterruptedStop(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	orphan, _, err := harness.store.CreateSummaryRun(context.Background(), target.ID(), target.SwitchVersion(), Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	harness.store.disable(t, target.ID())

	report, err := harness.daemon.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.StoppedTargets) != 1 || report.StoppedTargets[0] != target.ID() {
		t.Fatalf("interrupted stop must be completed, got %+v", report.StoppedTargets)
	}
	if harness.store.target(target.ID()).Actual() != ActualStopped {
		t.Fatal("target must be stopped after recovery")
	}
	stored, _ := harness.store.run(orphan.ID())
	if stored.State() != RunStopped || stored.Reason() != RunReasonSwitchDisabled {
		t.Fatalf("a disabled target's run must record the stop, got %s/%s", stored.State(), stored.Reason())
	}
}

// 恢复的全局读取失败视为启动失败，常驻循环不会带着未知状态继续。
func TestDaemonRunFailsWhenRecoveryFails(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.mu.Lock()
	harness.store.failTargetsErr = errors.New("target store is unavailable")
	harness.store.mu.Unlock()

	if err := harness.daemon.Run(context.Background()); err == nil {
		t.Fatal("daemon must not start when recovery cannot read the state")
	}
}

// 单个目标的恢复失败被隔离：其余目标照常收敛，常驻循环仍然启动。
func TestDaemonRecoveryIsolatesTargetFailures(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	broken := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	if _, _, err := harness.store.CreateSummaryRun(context.Background(), broken.ID(), broken.SwitchVersion(), Cursor{}); err != nil {
		t.Fatal(err)
	}
	harness.store.disable(t, broken.ID())
	healthy := harness.store.addSummaryTarget(t, PlatformSteam, 252490, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, healthy.ID())
	harness.store.mu.Lock()
	harness.store.failFinishErr = errors.New("run store is unavailable")
	harness.store.mu.Unlock()

	report, err := harness.daemon.Recover(context.Background())
	if err != nil {
		t.Fatalf("a single target failure must not abort recovery, got %v", err)
	}
	if report.Failures == nil {
		t.Fatal("the failed target must be reported")
	}
	if len(report.StoppedTargets) != 1 || report.StoppedTargets[0] != healthy.ID() {
		t.Fatalf("the healthy target must still converge, got %+v", report.StoppedTargets)
	}
	if harness.store.target(broken.ID()).Actual() == ActualStopped {
		t.Fatal("a target with an unfinished run must not be marked stopped")
	}
}

// 关闭请求的写回失败留下残留运行时，下一次收敛兜底结束它再写回 stopped。
func TestDaemonReconcileStopsFinishesLeftoverRun(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	leftover, _, err := harness.store.CreateSummaryRun(context.Background(), target.ID(), target.SwitchVersion(), Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = harness.store.BeginRun(context.Background(), leftover.ID()); err != nil {
		t.Fatal(err)
	}
	// 目标已受理关闭，但运行没有随之结束：模拟上一轮收敛写回失败。
	harness.store.disable(t, target.ID())

	stopped, _, err := harness.daemon.reconcileStops(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 1 || stopped[0] != target.ID() {
		t.Fatalf("the pending stop must converge, got %+v", stopped)
	}
	stored, _ := harness.store.run(leftover.ID())
	if stored.State() != RunStopped || stored.Reason() != RunReasonSwitchDisabled {
		t.Fatalf("leftover run must be stopped by the switch, got %s/%s", stored.State(), stored.Reason())
	}
	if harness.store.target(target.ID()).Actual() != ActualStopped {
		t.Fatal("target must be stopped once its run is terminal")
	}
}

// 收敛失败时不写回 stopped，避免 stopped 目标下仍挂着活动运行。
func TestDaemonReconcileStopsKeepsStoppingWhenRunSurvives(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	if _, _, err := harness.store.CreateSummaryRun(context.Background(), target.ID(), target.SwitchVersion(), Cursor{}); err != nil {
		t.Fatal(err)
	}
	harness.store.disable(t, target.ID())
	harness.store.mu.Lock()
	harness.store.failFinishErr = errors.New("run store is unavailable")
	harness.store.mu.Unlock()

	stopped, _, err := harness.daemon.reconcileStops(context.Background())
	if err == nil {
		t.Fatal("an unfinished run must surface as a convergence failure")
	}
	if len(stopped) != 0 {
		t.Fatalf("no target may be reported stopped, got %+v", stopped)
	}
	if harness.store.target(target.ID()).Actual() != ActualStopping {
		t.Fatal("target must stay in stopping until its run is terminal")
	}
}

// 遗留运行的处置规则：只有目标仍启用、开关一致且未超寿命的运行才保留续点。
func TestRecoverRunDisposition(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	run, _, err := harness.store.CreateSummaryRun(context.Background(), target.ID(), target.SwitchVersion(), Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := NewDetailRun(DetailRunInput{
		ID: 9001, Platform: PlatformSteam, AppID: 730, Side: market.SideAsk, ProductID: 1,
		RunSequence: 1, State: RunPending, CreatedAt: scheduleNow(),
	})
	if err != nil {
		t.Fatal(err)
	}
	owners := map[TargetID]Target{target.ID(): harness.store.target(target.ID())}
	floor := scheduleNow().Add(-time.Hour)
	stale := scheduleNow().Add(time.Hour)

	cases := []struct {
		name   string
		run    Run
		owners map[TargetID]Target
		floor  time.Time
		action recoveryAction
		state  RunState
		reason RunReason
	}{
		{"detail run is not scheduler owned", detail, owners, floor, recoveryIgnore, RunStopped, RunReasonNone},
		{"missing target", run, map[TargetID]Target{}, floor, recoveryFinish, RunFailed, RunReasonProcessRestarted},
		{"live target resumes", run, owners, floor, recoveryResume, RunSucceeded, RunReasonNone},
		{"stale run restarts", run, owners, stale, recoveryFinish, RunFailed, RunReasonProcessRestarted},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			action, state, reason := recoverRun(testCase.run, testCase.owners, testCase.floor)
			if action != testCase.action || state != testCase.state || reason != testCase.reason {
				t.Fatalf("got %v/%s/%s, want %v/%s/%s", action, state, reason,
					testCase.action, testCase.state, testCase.reason)
			}
		})
	}

	// 目标被禁用后，同一条运行必须改判为停止。
	harness.store.disable(t, target.ID())
	disabled := map[TargetID]Target{target.ID(): harness.store.target(target.ID())}
	action, state, reason := recoverRun(run, disabled, floor)
	if action != recoveryFinish || state != RunStopped || reason != RunReasonSwitchDisabled {
		t.Fatalf("a disabled target's run must stop, got %v/%s/%s", action, state, reason)
	}
}

// 状态被其他所有者接管时放弃收敛，但必须留下可观测的痕迹。
func TestDaemonReportsSkippedTargets(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, target.ID())
	harness.store.mu.Lock()
	harness.store.conflictTargets = map[TargetID]bool{target.ID(): true}
	harness.store.mu.Unlock()

	report, err := harness.daemon.Recover(context.Background())
	if err != nil || report.Failures != nil {
		t.Fatalf("a superseded target is not a failure, got err=%v failures=%v", err, report.Failures)
	}
	if len(report.SkippedTargets) != 1 || report.SkippedTargets[0] != target.ID() {
		t.Fatalf("the superseded target must be reported, got %+v", report.SkippedTargets)
	}
	if len(report.StoppedTargets) != 0 {
		t.Fatalf("a superseded target must not be reported stopped, got %+v", report.StoppedTargets)
	}

	stopped, skipped, err := harness.daemon.reconcileStops(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 0 || len(skipped) != 1 || skipped[0] != target.ID() {
		t.Fatalf("the cycle path must report the skip, stopped=%+v skipped=%+v", stopped, skipped)
	}
}

// 启动恢复的结果必须上报，否则逐对象失败无处可查。
func TestDaemonReportsRecoveryToObserver(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	if _, _, err := harness.store.CreateSummaryRun(context.Background(), target.ID(), target.SwitchVersion(), Cursor{}); err != nil {
		t.Fatal(err)
	}
	harness.store.disable(t, target.ID())
	harness.store.mu.Lock()
	harness.store.failFinishErr = errors.New("run store is unavailable")
	harness.store.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// 关闭收敛同样会撞上失效的存储，这里只关心恢复结果是否上报。
	_ = harness.daemon.Run(ctx)
	report, ok := harness.recoveryReport()
	if !ok {
		t.Fatal("recovery must be reported before the loop starts")
	}
	if report.Failures == nil {
		t.Fatal("per-target recovery failures must reach the observer")
	}
}

// 超过续点寿命的遗留运行必须结束并从空游标重来：偏移量型游标在长时间停机后
// 会移位，跨越过长停机的运行不能再声称覆盖完整。
func TestDaemonRecoverFinishesStaleRun(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, func(config *SchedulerConfig) {})
	harness.daemon.config.MaxResumeAge = time.Nanosecond
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	stale, _, err := harness.store.CreateSummaryRun(context.Background(), target.ID(), target.SwitchVersion(), Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.store.BeginRun(context.Background(), stale.ID()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)

	report, err := harness.daemon.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ResumedRuns) != 0 {
		t.Fatalf("a stale run must not be resumed, got %+v", report.ResumedRuns)
	}
	if len(report.FinishedRuns) != 1 || report.FinishedRuns[0] != stale.ID() {
		t.Fatalf("a stale run must be finished, got %+v", report.FinishedRuns)
	}
	stored, _ := harness.store.run(stale.ID())
	if stored.State() != RunFailed || stored.Reason() != RunReasonProcessRestarted {
		t.Fatalf("a stale run must record the restart, got %s/%s", stored.State(), stored.Reason())
	}
}

// 关闭时补齐已受理但未完成的关闭请求，否则目标既停不干净也无法重新启用。
func TestDaemonConvergesPendingStopsOnShutdown(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	stop := harness.start(t)
	waitFor(t, "the first cycle to finish", func() bool { return len(harness.recorded()) >= 1 })
	// 常驻循环正在等待下一周期时受理关闭，收敛只能发生在关闭路径上。
	harness.store.disable(t, target.ID())
	stop()

	if harness.store.target(target.ID()).Actual() != ActualStopped {
		t.Fatal("a pending stop must converge before the daemon exits")
	}
}

// 调度周期本身失败时必须记入 DaemonCycle.Err，而关闭收敛照常完成。
func TestDaemonReportsScheduleFailure(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, target.ID())
	// 只让本周期的第一次目标读取（调度器的那次）失败，收敛的那次照常成功。
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

// 关闭收敛失败时同样记入 DaemonCycle.Err，而调度周期照常完成。
func TestDaemonReportsStopConvergenceFailure(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	running := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	stopping := harness.store.addSummaryTarget(t, PlatformSteam, 252490, market.SideAsk, DesiredEnabled)
	harness.store.disable(t, stopping.ID())
	// ActiveRuns 只被关闭收敛使用，调度周期不受影响。
	harness.store.mu.Lock()
	harness.store.failActiveRunsErr = errors.New("run store is unavailable")
	harness.store.mu.Unlock()

	cycle := harness.daemon.runCycle(context.Background())
	if cycle.Err == nil {
		t.Fatal("a failed stop convergence must be reported")
	}
	if len(cycle.Stopped) != 0 {
		t.Fatalf("no target may be reported stopped, got %+v", cycle.Stopped)
	}
	dispatched := false
	for _, outcome := range cycle.Report.Targets {
		if outcome.TargetID == running.ID() && outcome.Dispatched {
			dispatched = true
		}
	}
	if !dispatched {
		t.Fatal("the schedule cycle itself must still run")
	}
}

// 常驻循环不因单个周期失败退出：存储恢复后继续派发。
func TestDaemonSurvivesCycleFailure(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, func(config *SchedulerConfig) {
		config.SummaryPeriod = time.Millisecond
	})
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	stop := harness.start(t)
	waitFor(t, "the first cycle", func() bool { return len(harness.recorded()) >= 1 })
	// 启动恢复完成后再让目标读取失败，周期失败不得终止常驻循环。
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
			for _, target := range cycle.Report.Targets {
				for _, outcome := range target.Runs {
					if outcome.State == RunSucceeded {
						return true
					}
				}
			}
		}
		return false
	})
	stop()
}

// 活动运行读取失败同样是恢复的全局读取失败，常驻循环不得启动。
func TestDaemonRunFailsWhenActiveRunsUnreadable(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.mu.Lock()
	harness.store.failActiveRunsErr = errors.New("run store is unavailable")
	harness.store.mu.Unlock()

	if err := harness.daemon.Run(context.Background()); err == nil {
		t.Fatal("daemon must not start when active runs cannot be read")
	}
}

// 实例锁失效意味着可能已有接替进程，常驻循环必须立即停止写入并退出。
func TestDaemonExitsWhenInstanceLockIsLost(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
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

// 另一个常驻进程持有实例锁时拒绝启动，退出后释放锁供接替者取得。
func TestDaemonRefusesToRunWithoutInstanceLock(t *testing.T) {
	harness := newDaemonHarness(t, 5*time.Millisecond, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	stop := harness.start(t)
	waitFor(t, "the running daemon to take the instance lock", func() bool {
		return len(harness.recorded()) >= 1
	})

	// 共用同一栅栏的第二个常驻实例必须被拒绝，且不得改动任何状态。
	second, err := NewDaemon(harness.scheduler, DaemonConfig{Interval: time.Millisecond,
		ShutdownTimeout: time.Second, LockVerifyTimeout: time.Second, MaxResumeAge: time.Hour, Guard: harness.guard})
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
	// 锁已释放，接替进程可以正常启动。
	third, err := NewDaemon(harness.scheduler, DaemonConfig{Interval: time.Hour,
		ShutdownTimeout: time.Second, LockVerifyTimeout: time.Second, MaxResumeAge: time.Hour, Guard: harness.guard})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := third.Run(ctx); err != nil {
		t.Fatalf("successor must start after the lock is released, got %v", err)
	}
}

// 失去独占后不得再写库：关闭收敛在复验失败时必须被跳过。
func TestDaemonSkipsShutdownConvergenceWithoutInstanceLock(t *testing.T) {
	harness := newDaemonHarness(t, time.Hour, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- harness.daemon.Run(ctx) }()
	waitFor(t, "the first cycle", func() bool { return len(harness.recorded()) >= 1 })
	// 常驻循环等待下一周期期间受理关闭，同时实例锁失效。
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

// 复验必须受超时约束，否则半开连接会把常驻循环阻塞到内核超时。
func TestDaemonBoundsInstanceLockVerify(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	guard := &fakeInstanceGuard{blockVerify: true}
	daemon, err := NewDaemon(harness.scheduler, DaemonConfig{Interval: time.Hour,
		ShutdownTimeout: time.Second, LockVerifyTimeout: 20 * time.Millisecond,
		MaxResumeAge: time.Hour, Guard: guard})
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

// 取得实例锁失败时启动失败。
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
		LockVerifyTimeout: time.Second, MaxResumeAge: time.Hour, Guard: &fakeInstanceGuard{}}
	if _, err := NewDaemon(nil, valid); err == nil {
		t.Fatal("nil scheduler must be rejected")
	}
	broken := valid
	broken.Interval = 0
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("non-positive interval must be rejected")
	}
	broken = valid
	broken.ShutdownTimeout = 0
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("non-positive shutdown timeout must be rejected")
	}
	broken = valid
	broken.LockVerifyTimeout = 0
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("non-positive lock verify timeout must be rejected")
	}
	broken = valid
	broken.MaxResumeAge = 0
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("non-positive resume age must be rejected")
	}
	broken = valid
	broken.Guard = nil
	if _, err := NewDaemon(harness.scheduler, broken); err == nil {
		t.Fatal("missing instance guard must be rejected")
	}
}
