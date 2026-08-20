package collection

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrInstanceLocked 表示另一个常驻调度进程已经持有同一份采集状态。
var ErrInstanceLocked = errors.New("another resident scheduler owns the collection state")

const (
	priceTickMaintenanceTimeout = 30 * time.Second
	priceTickMaintenanceRetry   = time.Minute
	residentLockVerifyInterval  = 2 * time.Second
)

// DaemonStore 是常驻循环所需的持久化端口。
type DaemonStore interface {
	ScheduleStore
	PurgeExpiredPriceTicks(ctx context.Context, now time.Time) error
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
	Interval                     time.Duration
	PriceTickMaintenanceInterval time.Duration
	ShutdownTimeout              time.Duration
	LockVerifyTimeout            time.Duration
	Guard                        InstanceGuard
	Observer                     func(DaemonCycle)
	RecoveryObserver             func(RecoveryReport)
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

type instanceLockWatchdog struct {
	mu   sync.Mutex
	err  error
	done chan struct{}
}

func (watchdog *instanceLockWatchdog) fail(err error) {
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	if watchdog.err == nil {
		watchdog.err = err
	}
}

func (watchdog *instanceLockWatchdog) Err() error {
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	return watchdog.err
}

// NewDaemon 校验常驻参数并返回可运行的常驻调度器。
func NewDaemon(scheduler *Scheduler, config DaemonConfig) (*Daemon, error) {
	if scheduler == nil {
		return nil, fmt.Errorf("scheduler is required")
	}
	if config.Interval <= 0 || config.PriceTickMaintenanceInterval <= 0 ||
		config.ShutdownTimeout <= 0 || config.LockVerifyTimeout <= 0 {
		return nil, fmt.Errorf("daemon durations must be positive")
	}
	if config.Guard == nil {
		return nil, fmt.Errorf("instance guard is required")
	}
	store, ok := scheduler.store.(DaemonStore)
	if !ok {
		return nil, fmt.Errorf("scheduler store does not support resident daemon operations")
	}
	return &Daemon{scheduler: scheduler, store: store, config: config}, nil
}

// Run 取得实例锁后先回队全部认领并收敛关闭，再按固定间隔执行调度周期。
func (d *Daemon) Run(ctx context.Context) error {
	return d.run(ctx, nil)
}

// RunReady 在实例锁和启动恢复完成后报告一次启动结果，并继续持有锁运行。
func (d *Daemon) RunReady(ctx context.Context, ready chan<- error) error {
	if ready == nil {
		return fmt.Errorf("daemon ready channel is required")
	}
	return d.run(ctx, ready)
}

func (d *Daemon) run(ctx context.Context, ready chan<- error) (runErr error) {
	readySent := ready == nil
	reportReady := func(err error) {
		if readySent {
			return
		}
		ready <- err
		readySent = true
	}
	defer func() {
		if !readySent {
			reportReady(runErr)
		}
	}()
	if ctx == nil {
		return fmt.Errorf("nil daemon context")
	}
	lock, acquired, err := d.config.Guard.AcquireInstanceLock(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("acquire resident instance lock: %w", err)
	}
	if !acquired {
		return ErrInstanceLocked
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), d.config.LockVerifyTimeout)
		defer cancel()
		if err := lock.Release(releaseCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("release resident instance lock: %w", err))
		}
	}()

	if err := d.verifyLock(ctx, lock); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	guardCtx, cancelGuard := context.WithCancel(context.WithoutCancel(ctx))
	watchdog := &instanceLockWatchdog{done: make(chan struct{})}
	go d.watchInstanceLock(guardCtx, lock, watchdog, cancelRun, cancelGuard)
	defer func() {
		cancelGuard()
		<-watchdog.done
		runErr = errors.Join(runErr, watchdog.Err())
	}()

	report, err := d.Recover(runCtx)
	if d.config.RecoveryObserver != nil {
		d.config.RecoveryObserver(report)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if lockErr := watchdog.Err(); lockErr != nil {
			return lockErr
		}
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	if lockErr := watchdog.Err(); lockErr != nil {
		return lockErr
	}
	dispatcher, err := d.scheduler.newResidentDispatcher(runCtx)
	if err != nil {
		return err
	}
	workersClosed := false
	closeWorkers := func() error {
		if workersClosed {
			return nil
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), d.config.ShutdownTimeout)
		err := dispatcher.Close(closeCtx)
		cancel()
		if outcomes := dispatcher.Drain(); len(outcomes) > 0 && d.config.Observer != nil {
			d.config.Observer(DaemonCycle{Report: CycleReport{StartedAt: d.scheduler.now(), Workers: outcomes}})
		}
		if err != nil {
			return fmt.Errorf("stop resident workers: %w", err)
		}
		workersClosed = true
		return nil
	}
	defer func() {
		if !workersClosed {
			runErr = errors.Join(runErr, closeWorkers())
		}
	}()
	if ctx.Err() != nil {
		return nil
	}
	if lockErr := watchdog.Err(); lockErr != nil {
		return lockErr
	}
	reportReady(nil)
	nextPriceTickMaintenance := time.Time{}
	for runCtx.Err() == nil {
		startedAt := d.scheduler.now()
		cycle := d.runResidentCycle(runCtx, dispatcher)
		var maintenanceErr error
		nextPriceTickMaintenance, maintenanceErr = d.maintainPriceTicks(runCtx, nextPriceTickMaintenance)
		cycle.Err = errors.Join(cycle.Err, maintenanceErr)
		if d.config.Observer != nil {
			d.config.Observer(cycle)
		}
		advance, err := d.waitResidentInterval(
			runCtx, dispatcher, d.config.Interval-d.scheduler.now().Sub(startedAt),
		)
		if err != nil {
			return err
		}
		if !advance {
			break
		}
	}
	if err := closeWorkers(); err != nil {
		return err
	}
	if err := watchdog.Err(); err != nil {
		return err
	}
	if err := d.verifyLock(guardCtx, lock); err != nil {
		return err
	}
	if err := d.finalize(guardCtx); err != nil {
		return err
	}
	if err := watchdog.Err(); err != nil {
		return err
	}
	return d.verifyLock(guardCtx, lock)
}

func (d *Daemon) waitResidentInterval(
	ctx context.Context,
	dispatcher *residentDispatcher,
	duration time.Duration,
) (bool, error) {
	if duration <= 0 {
		return ctx.Err() == nil, nil
	}
	cycleDeadline := time.Now().Add(duration)
	for {
		wait := time.Until(cycleDeadline)
		retryWake := false
		if retryAt := dispatcher.NextRetryAt(); !retryAt.IsZero() {
			if retryWait := time.Until(retryAt); retryWait < wait {
				wait = retryWait
				retryWake = true
			}
		}
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, nil
		case <-timer.C:
			if !time.Now().Before(cycleDeadline) {
				return true, nil
			}
			if !retryWake {
				continue
			}
			cycle := d.runResidentDispatch(ctx, dispatcher)
			if d.config.Observer != nil {
				d.config.Observer(cycle)
			}
		case <-dispatcher.Wake():
			timer.Stop()
			if ctx.Err() != nil {
				return false, nil
			}
			cycle := d.runResidentDispatch(ctx, dispatcher)
			if d.config.Observer != nil {
				d.config.Observer(cycle)
			}
		}
	}
}

func (d *Daemon) watchInstanceLock(
	ctx context.Context,
	lock InstanceLock,
	watchdog *instanceLockWatchdog,
	cancelRun context.CancelFunc,
	cancelGuard context.CancelFunc,
) {
	defer close(watchdog.done)
	ticker := time.NewTicker(residentLockVerifyInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.verifyLock(ctx, lock); err != nil {
				if ctx.Err() != nil {
					return
				}
				watchdog.fail(err)
				cancelRun()
				cancelGuard()
				return
			}
		}
	}
}

func (d *Daemon) maintainPriceTicks(ctx context.Context, nextAt time.Time) (time.Time, error) {
	if ctx.Err() != nil {
		return nextAt, nil
	}
	now := d.scheduler.now()
	if !nextAt.IsZero() && now.Before(nextAt) {
		return nextAt, nil
	}
	maintenanceCtx, cancel := context.WithTimeout(ctx, priceTickMaintenanceTimeout)
	err := d.store.PurgeExpiredPriceTicks(maintenanceCtx, now)
	cancel()
	if err != nil {
		retry := priceTickMaintenanceRetry
		if retry > d.config.PriceTickMaintenanceInterval {
			retry = d.config.PriceTickMaintenanceInterval
		}
		return now.Add(retry), fmt.Errorf("purge expired price ticks: %w", err)
	}
	return now.Add(d.config.PriceTickMaintenanceInterval), nil
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
		report.Failures = fmt.Errorf("release stale claims: %w", err)
		return report, report.Failures
	}
	report.ReleasedClaims = released
	stopped, skipped, stopErr := d.reconcileStops(ctx)
	report.StoppedTargets = stopped
	report.SkippedTargets = skipped
	report.Failures = stopErr
	return report, stopErr
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

func (d *Daemon) runResidentCycle(ctx context.Context, dispatcher *residentDispatcher) DaemonCycle {
	report, targets, resources, err := d.scheduler.planResidentCycle(ctx, dispatcher.ActiveTasks())
	report.Workers = append(report.Workers, dispatcher.Drain()...)
	if err == nil {
		var immediate []WorkerOutcome
		immediate, err = dispatcher.Dispatch(ctx, targets, resources)
		report.Workers = append(report.Workers, immediate...)
	}
	cycle := DaemonCycle{Report: report, Err: err}
	if ctx.Err() != nil {
		return cycle
	}
	stopped, skipped, stopErr := d.reconcileStopsExcept(ctx, dispatcher.BusyTargets())
	cycle.Stopped = stopped
	cycle.Skipped = skipped
	cycle.Err = errors.Join(cycle.Err, stopErr)
	return cycle
}

func (d *Daemon) runResidentDispatch(ctx context.Context, dispatcher *residentDispatcher) DaemonCycle {
	report := CycleReport{StartedAt: d.scheduler.now(), Workers: dispatcher.Drain()}
	immediate, err := dispatcher.DispatchCached(ctx)
	report.Workers = append(report.Workers, immediate...)
	cycle := DaemonCycle{Report: report, Err: err}
	if ctx.Err() != nil {
		return cycle
	}
	stopped, skipped, stopErr := d.reconcileStopsExcept(ctx, dispatcher.BusyTargets())
	cycle.Stopped = stopped
	cycle.Skipped = skipped
	cycle.Err = errors.Join(cycle.Err, stopErr)
	return cycle
}

func (d *Daemon) finalize(ctx context.Context) error {
	shutdown, cancel := context.WithTimeout(ctx, d.config.ShutdownTimeout)
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
	return d.reconcileStopsExcept(ctx, nil)
}

func (d *Daemon) reconcileStopsExcept(
	ctx context.Context,
	busy map[TargetID]struct{},
) ([]TargetID, []TargetID, error) {
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
		if _, found := busy[target.ID()]; found {
			skipped = append(skipped, target.ID())
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
