package collection

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrInstanceLocked 表示另一个常驻调度进程已经持有同一份采集状态。
var ErrInstanceLocked = errors.New("another resident scheduler owns the collection state")

// DaemonStore 是常驻恢复所需的持久化端口：在单周期能力之上增加残留运行读取。
type DaemonStore interface {
	ScheduleStore
	// ActiveRuns 返回调度器拥有的全部非终态运行（目录与摘要），不含详情运行。
	ActiveRuns(ctx context.Context) ([]Run, error)
}

// InstanceLock 是常驻实例锁的持有凭据。
type InstanceLock interface {
	// Verify 确认锁仍由本进程持有。持有凭据失效后必须返回错误，常驻循环据此
	// 立即停止写入，避免与接替进程同时操作同一份状态。
	Verify(ctx context.Context) error
	// Release 释放实例锁。调用方在常驻循环退出后必须调用一次。
	Release(ctx context.Context) error
}

// InstanceGuard 保证同一份持久采集状态同时只有一个常驻调度进程。进程内的
// 资源占用只在单个 Coordinator 实例内有效，因此双进程会同时占用同一账号与
// 节点，并使「启动时的活动运行都属于上一进程」的恢复前提失效。
type InstanceGuard interface {
	// AcquireInstanceLock 尝试取得实例锁；已被其他进程持有时返回 false。
	AcquireInstanceLock(ctx context.Context) (InstanceLock, bool, error)
}

// DaemonConfig 是常驻调度参数。
type DaemonConfig struct {
	// Interval 是两次周期开始之间的最小间隔。
	Interval time.Duration
	// ShutdownTimeout 是关闭时收敛已受理关闭请求的时间上限。
	ShutdownTimeout time.Duration
	// LockVerifyTimeout 是每次复验实例锁的时间上限，避免半开连接把常驻循环
	// 阻塞到内核超时。
	LockVerifyTimeout time.Duration
	// MaxResumeAge 是遗留运行可被续点的最大**总寿命**（自运行开始起算，包含
	// 有效采集时间与停机时间）。超过此寿命的运行会被结束并从空游标重来：偏移量
	// 型游标经历过长的时间跨度后会移位，运行不能再声称覆盖完整。这个上界因此
	// 无法单独约束停机时长，必须设得大于一次完整走查的耗时，否则长目录在每次
	// 重启后都会被判超龄而永远跑不完。
	MaxResumeAge time.Duration
	// Guard 是单实例栅栏，常驻循环必须独占运行。
	Guard InstanceGuard
	// Observer 在每个周期结束后同步接收该周期结果，可以为空；实现不得阻塞，
	// 否则会推迟下一周期。
	Observer func(DaemonCycle)
	// RecoveryObserver 在启动恢复结束后同步接收恢复结果，可以为空。恢复的
	// 逐对象失败只出现在这里，不通过 Run 的返回值上报。
	RecoveryObserver func(RecoveryReport)
}

// DaemonCycle 是一次常驻周期的结果。
type DaemonCycle struct {
	Report CycleReport
	// Stopped 是本周期内完成关闭的目标。
	Stopped []TargetID
	// Skipped 是关闭收敛时状态已被其他所有者接管、本周期放弃收敛的目标。
	Skipped []TargetID
	// Err 是本周期的失败。常驻循环不因单个周期失败退出，失败状态由目标自身表达。
	Err error
}

// RecoveryReport 记录一次启动恢复的结果。
type RecoveryReport struct {
	// ResumedRuns 是保留续点的遗留运行，下一周期从存量游标继续。
	ResumedRuns []RunID
	// FinishedRuns 是无法继续因而被结束的遗留运行。
	FinishedRuns []RunID
	// StoppedTargets 是补齐关闭的目标。
	StoppedTargets []TargetID
	// SkippedTargets 是状态已被其他所有者接管、本次放弃收敛的目标。
	SkippedTargets []TargetID
	// Failures 汇总按运行和目标隔离的失败。受影响的对象留待后续周期处理，
	// 不会阻止常驻循环启动。
	Failures error
}

// Daemon 在单进程内常驻执行调度周期，并负责启动恢复与关闭收敛。
// 同一个 Daemon 不能并发运行，独占性由 InstanceGuard 保证。
type Daemon struct {
	scheduler *Scheduler
	store     DaemonStore
	config    DaemonConfig
}

// NewDaemon 校验常驻参数并返回可运行的常驻调度器。
func NewDaemon(scheduler *Scheduler, config DaemonConfig) (*Daemon, error) {
	if scheduler == nil {
		return nil, fmt.Errorf("scheduler is required")
	}
	if config.Interval <= 0 || config.ShutdownTimeout <= 0 ||
		config.LockVerifyTimeout <= 0 || config.MaxResumeAge <= 0 {
		return nil, fmt.Errorf("daemon durations must be positive")
	}
	if config.Guard == nil {
		return nil, fmt.Errorf("instance guard is required")
	}
	// 常驻恢复与调度必须操作同一份持久状态，因此复用调度器自己的 Store。
	store, ok := scheduler.store.(DaemonStore)
	if !ok {
		return nil, fmt.Errorf("scheduler store does not support resident recovery")
	}
	return &Daemon{scheduler: scheduler, store: store, config: config}, nil
}

// Run 取得实例锁后先执行启动恢复，再按固定间隔常驻执行调度周期，直到 ctx
// 取消；退出前再收敛一次已受理的关闭请求。恢复的全局读取失败与实例锁失效
// 都视为启动或运行失败；单个周期失败只记录在周期结果中，不终止常驻循环。
func (d *Daemon) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nil daemon context")
	}
	lock, acquired, err := d.config.Guard.AcquireInstanceLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire resident instance lock: %w", err)
	}
	if !acquired {
		return ErrInstanceLocked
	}
	// 关闭时 ctx 已取消，释放必须使用不受取消影响的 context。
	defer func() { _ = lock.Release(context.WithoutCancel(ctx)) }()

	report, err := d.Recover(ctx)
	if d.config.RecoveryObserver != nil {
		d.config.RecoveryObserver(report)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	for ctx.Err() == nil {
		if err := d.verifyLock(ctx, lock); err != nil {
			if ctx.Err() != nil {
				break
			}
			return err
		}
		startedAt := d.scheduler.now()
		cycle := d.runCycle(ctx)
		if d.config.Observer != nil {
			d.config.Observer(cycle)
		}
		if !waitInterval(ctx, d.config.Interval-d.scheduler.now().Sub(startedAt)) {
			break
		}
	}
	// 关闭收敛同样要写库，写之前必须仍然独占状态。
	if err := d.verifyLock(context.WithoutCancel(ctx), lock); err != nil {
		return err
	}
	return d.finalize(ctx)
}

// verifyLock 在限定时间内复验实例锁。锁失效意味着本进程可能已不再独占状态，
// 必须立刻停止一切写入。
func (d *Daemon) verifyLock(ctx context.Context, lock InstanceLock) error {
	verifyCtx, cancel := context.WithTimeout(ctx, d.config.LockVerifyTimeout)
	defer cancel()
	if err := lock.Verify(verifyCtx); err != nil {
		return fmt.Errorf("verify resident instance lock: %w", err)
	}
	return nil
}

// Recover 收敛上一进程遗留的状态。取得实例锁后本进程没有任何在途运行，
// 因此数据库中的活动运行都属于上一进程：仍可继续的运行保留续点，其余结束；
// 已受理但未完成的关闭在此补齐。单个运行或目标失败只记入报告，不中止恢复。
func (d *Daemon) Recover(ctx context.Context) (RecoveryReport, error) {
	if ctx == nil {
		return RecoveryReport{}, fmt.Errorf("nil recovery context")
	}
	var report RecoveryReport
	orphans, err := d.store.ActiveRuns(ctx)
	if err != nil {
		return report, fmt.Errorf("list active runs: %w", err)
	}
	targets, err := d.store.Targets(ctx)
	if err != nil {
		return report, fmt.Errorf("list collection targets: %w", err)
	}
	owners := make(map[TargetID]Target, len(targets))
	for _, target := range targets {
		owners[target.ID()] = target
	}

	// 未能结束的运行仍然活动，关闭收敛必须看到它们，否则会把仍有活动运行的
	// 目标写成 stopped。
	unresolved := make([]Run, 0)
	resumeFloor := d.scheduler.now().Add(-d.config.MaxResumeAge)
	for _, run := range orphans {
		action, state, reason := recoverRun(run, owners, resumeFloor)
		switch action {
		case recoveryIgnore:
			continue
		case recoveryResume:
			report.ResumedRuns = append(report.ResumedRuns, run.ID())
			continue
		}
		if _, err := d.store.FinishRun(ctx, run.ID(), state, CompletenessPartial, reason); err != nil {
			report.Failures = errors.Join(report.Failures, fmt.Errorf("finish orphaned run: %w", err))
			unresolved = append(unresolved, run)
			continue
		}
		report.FinishedRuns = append(report.FinishedRuns, run.ID())
	}

	for _, target := range targets {
		if target.Desired() != DesiredDisabled || target.Actual() != ActualStopping {
			continue
		}
		stopped, err := d.stopTarget(ctx, target, unresolved)
		switch {
		case err != nil:
			report.Failures = errors.Join(report.Failures, err)
		case stopped:
			report.StoppedTargets = append(report.StoppedTargets, target.ID())
		default:
			report.SkippedTargets = append(report.SkippedTargets, target.ID())
		}
	}
	return report, nil
}

type recoveryAction int

const (
	// recoveryResume 保留运行，由下一周期从存量游标继续。
	recoveryResume recoveryAction = iota
	// recoveryFinish 结束运行。
	recoveryFinish
	// recoveryIgnore 表示运行不归调度器所有。
	recoveryIgnore
)

// recoverRun 判断上一进程遗留的运行如何处置，返回的状态与原因只在结束时有效。
// 目标仍启用、开关版本一致且运行未超过续点寿命时保留续点，避免重启丢弃已提交
// 的分页并重复消耗限频预算；开关已推进或目标已禁用的运行必须结束，否则活动运行
// 唯一性会永久挡住该目标新建运行。
func recoverRun(run Run, owners map[TargetID]Target, resumeFloor time.Time) (recoveryAction, RunState, RunReason) {
	targetID, owned := run.TargetID()
	if !owned {
		return recoveryIgnore, RunStopped, RunReasonNone
	}
	target, found := owners[targetID]
	if !found {
		return recoveryFinish, RunFailed, RunReasonProcessRestarted
	}
	if target.Desired() != DesiredEnabled {
		return recoveryFinish, RunStopped, RunReasonSwitchDisabled
	}
	// 开关版本落后说明这条运行属于上一个启用周期，是被运维的关闭终止的。
	switchVersion, present := run.SwitchVersion()
	if !present || switchVersion != target.SwitchVersion() {
		return recoveryFinish, RunStopped, RunReasonSwitchDisabled
	}
	if runStartedAt(run).Before(resumeFloor) {
		return recoveryFinish, RunFailed, RunReasonProcessRestarted
	}
	return recoveryResume, RunSucceeded, RunReasonNone
}

// runStartedAt 返回运行的起始时刻，未开始的运行退回创建时刻。这是运行最早一页
// 的时间下界，因此也是覆盖窗口跨度的下界。
func runStartedAt(run Run) time.Time {
	if startedAt, ok := run.StartedAt(); ok {
		return startedAt
	}
	return run.CreatedAt()
}

// runCycle 执行一次调度周期并在周期结束后收敛关闭请求。周期返回时本周期
// 派发的运行都已退出、租约都已释放，因此此刻收敛不会打断在途运行。
func (d *Daemon) runCycle(ctx context.Context) DaemonCycle {
	report, err := d.scheduler.RunCycle(ctx)
	cycle := DaemonCycle{Report: report, Err: err}
	if ctx.Err() != nil {
		return cycle
	}
	stopped, skipped, stopErr := d.reconcileStops(ctx)
	cycle.Stopped = stopped
	cycle.Skipped = skipped
	cycle.Err = errors.Join(cycle.Err, stopErr)
	return cycle
}

// finalize 在关闭时用不受取消影响的有界 context 再收敛一次关闭请求，避免
// 已受理的关闭停在 stopping：此时既停不干净，也无法重新启用。
func (d *Daemon) finalize(ctx context.Context) error {
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.config.ShutdownTimeout)
	defer cancel()
	stopped, skipped, err := d.reconcileStops(shutdown)
	if err != nil {
		err = fmt.Errorf("converge stops on shutdown: %w", err)
	}
	// 关闭路径的收敛结果同样要可观测，否则运维无从知道退出时收敛了什么。
	if d.config.Observer != nil {
		d.config.Observer(DaemonCycle{Stopped: stopped, Skipped: skipped, Err: err})
	}
	return err
}

// reconcileStops 把已受理的关闭请求收敛为 stopped：目标任务退出后才算停止。
// 只处理已禁用目标：仍启用目标的活动运行可能是本进程刻意保留的续点，不能结束。
func (d *Daemon) reconcileStops(ctx context.Context) ([]TargetID, []TargetID, error) {
	targets, err := d.store.Targets(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list collection targets: %w", err)
	}
	pending := make([]Target, 0)
	for _, target := range targets {
		if target.Desired() == DesiredDisabled && target.Actual() == ActualStopping {
			pending = append(pending, target)
		}
	}
	if len(pending) == 0 {
		return nil, nil, nil
	}
	// 正常路径下调度器已经结束了被禁用目标的运行；这里只兜底写回失败留下的残留。
	active, err := d.store.ActiveRuns(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list active runs: %w", err)
	}
	stopped := make([]TargetID, 0, len(pending))
	skipped := make([]TargetID, 0)
	var failures error
	for _, target := range pending {
		done, err := d.stopTarget(ctx, target, active)
		switch {
		case err != nil:
			failures = errors.Join(failures, err)
		case done:
			stopped = append(stopped, target.ID())
		default:
			skipped = append(skipped, target.ID())
		}
	}
	return stopped, skipped, failures
}

// stopTarget 先结束目标的残留活动运行，再把 stopping 收敛为 stopped；
// 残留运行未能结束时不写回停止状态，避免 stopped 目标下仍挂着活动运行。
func (d *Daemon) stopTarget(ctx context.Context, target Target, active []Run) (bool, error) {
	for _, run := range active {
		id, ok := run.TargetID()
		if !ok || id != target.ID() {
			continue
		}
		if _, err := d.store.FinishRun(ctx, run.ID(), RunStopped, CompletenessPartial, RunReasonSwitchDisabled); err != nil {
			return false, fmt.Errorf("stop target run: %w", err)
		}
	}
	_, err := d.store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(),
		TargetTransition{State: ActualStopped})
	if errors.Is(err, ErrConflict) {
		// 状态已被其他所有者接管，下一周期重新评估。
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mark target stopped: %w", err)
	}
	return true, nil
}

// waitInterval 等待到间隔结束；ctx 取消时立即返回 false。
func waitInterval(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
