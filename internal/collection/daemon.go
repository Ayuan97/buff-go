package collection

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrInstanceLocked 表示另一个常驻调度进程已经持有同一份采集状态。
var ErrInstanceLocked = errors.New("another resident scheduler owns the collection state")

// DaemonStore 是常驻循环所需的持久化端口。
type DaemonStore interface {
	ScheduleStore
}

// InstanceLock 是常驻实例锁的持有凭据。
type InstanceLock interface {
	Verify(ctx context.Context) error
	Release(ctx context.Context) error
}

// InstanceGuard 保证同一份持久采集状态同时只有一个常驻调度进程。
type InstanceGuard interface {
	AcquireInstanceLock(ctx context.Context) (InstanceLock, bool, error)
}

// DaemonConfig 是常驻调度参数。
type DaemonConfig struct {
	Interval          time.Duration
	ShutdownTimeout   time.Duration
	LockVerifyTimeout time.Duration
	Guard             InstanceGuard
	Observer          func(DaemonCycle)
	RecoveryObserver  func(RecoveryReport)
}

// DaemonCycle 是一次常驻周期的结果。
type DaemonCycle struct {
	Report  CycleReport
	Stopped []TargetID
	Skipped []TargetID
	Err     error
}

// RecoveryReport 记录启动时收敛关闭请求的结果。
type RecoveryReport struct {
	ReleasedClaims int
	StoppedTargets []TargetID
	SkippedTargets []TargetID
	Failures       error
}

// Daemon 在单进程内常驻执行调度周期，并负责关闭收敛。
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
	if config.Interval <= 0 || config.ShutdownTimeout <= 0 || config.LockVerifyTimeout <= 0 {
		return nil, fmt.Errorf("daemon durations must be positive")
	}
	if config.Guard == nil {
		return nil, fmt.Errorf("instance guard is required")
	}
	store, ok := scheduler.store.(DaemonStore)
	if !ok {
		return nil, fmt.Errorf("scheduler store does not support resident recovery")
	}
	return &Daemon{scheduler: scheduler, store: store, config: config}, nil
}

// Run 取得实例锁后先回队全部认领并收敛关闭，再按固定间隔执行调度周期。
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
	if err := d.verifyLock(context.WithoutCancel(ctx), lock); err != nil {
		return err
	}
	return d.finalize(ctx)
}

func (d *Daemon) verifyLock(ctx context.Context, lock InstanceLock) error {
	verifyCtx, cancel := context.WithTimeout(ctx, d.config.LockVerifyTimeout)
	defer cancel()
	if err := lock.Verify(verifyCtx); err != nil {
		return fmt.Errorf("verify resident instance lock: %w", err)
	}
	return nil
}

// Recover 回队全部认领（重启后没有活租约），并把已受理的关闭收敛为 stopped。
func (d *Daemon) Recover(ctx context.Context) (RecoveryReport, error) {
	if ctx == nil {
		return RecoveryReport{}, fmt.Errorf("nil recovery context")
	}
	var report RecoveryReport
	released, err := d.store.ReleaseAllClaims(ctx)
	if err != nil {
		return report, fmt.Errorf("release stale claims: %w", err)
	}
	report.ReleasedClaims = released
	stopped, skipped, stopErr := d.reconcileStops(ctx)
	report.StoppedTargets = stopped
	report.SkippedTargets = skipped
	report.Failures = stopErr
	return report, nil
}

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

func (d *Daemon) finalize(ctx context.Context) error {
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.config.ShutdownTimeout)
	defer cancel()
	stopped, skipped, err := d.reconcileStops(shutdown)
	if err != nil {
		err = fmt.Errorf("converge stops on shutdown: %w", err)
	}
	if d.config.Observer != nil {
		d.config.Observer(DaemonCycle{Stopped: stopped, Skipped: skipped, Err: err})
	}
	return err
}

func (d *Daemon) reconcileStops(ctx context.Context) ([]TargetID, []TargetID, error) {
	targets, err := d.store.Targets(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list collection targets: %w", err)
	}
	stopped := make([]TargetID, 0)
	skipped := make([]TargetID, 0)
	var failures error
	for _, target := range targets {
		if target.Desired() != DesiredDisabled || target.Actual() != ActualStopping {
			continue
		}
		if err := d.store.ClearTargetQueue(ctx, target.ID()); err != nil {
			failures = errors.Join(failures, fmt.Errorf("clear target queue: %w", err))
			continue
		}
		_, err := d.store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(),
			TargetTransition{State: ActualStopped})
		switch {
		case errors.Is(err, ErrConflict):
			skipped = append(skipped, target.ID())
		case err != nil:
			failures = errors.Join(failures, fmt.Errorf("mark target stopped: %w", err))
		default:
			stopped = append(stopped, target.ID())
		}
	}
	return stopped, skipped, failures
}

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
