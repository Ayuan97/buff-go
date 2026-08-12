package collection

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/market"
	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

// 采集存储契约错误。规范值定义在领域包；storage/postgres 以同值变量对外保留
// 既有名称，errors.Is 语义一致。
var (
	// ErrInvalidInput 表示调用方输入不合法。
	ErrInvalidInput = errors.New("collection input is invalid")
	// ErrNotFound 表示目标、运行或页面不存在。
	ErrNotFound = errors.New("collection record was not found")
	// ErrTargetDisabled 表示目标已被禁用。
	ErrTargetDisabled = errors.New("collection target is disabled")
	// ErrConflict 表示 revision、switch 或状态 CAS 冲突。
	ErrConflict = errors.New("collection state conflicts with expected revision")
	// ErrIntegrity 表示持久状态与领域约束不一致。
	ErrIntegrity = errors.New("collection state is inconsistent")
	// ErrStorage 表示数据库或基础设施失败。
	ErrStorage = errors.New("collection storage operation failed")
	// ErrFence 表示页面来自被禁用目标、旧开关版本或非 running 运行。
	ErrFence = errors.New("collection page is fenced")
	// ErrPageOrder 表示页面跳序或游标不衔接。
	ErrPageOrder = errors.New("collection page is out of order")
	// ErrPageConflict 表示同序页面重试时内容发生变化。
	ErrPageConflict = errors.New("collection page conflicts with stored facts")
)

// TargetTransition 是调度器拥有的一次显式实际状态转换。
type TargetTransition struct {
	State     ActualState
	Reason    TargetReason
	RecheckAt *time.Time
}

// AttemptWrite 是一条以内部商品身份声明的显式行情事实。
type AttemptWrite struct {
	ProductID   catalog.ProductID
	Observation market.Observation
	ReasonCode  string
}

// SummaryPageCommit 是一个显式摘要页面。作用域与因果顺序由持久运行派生，
// 不接受调用方提交。
type SummaryPageCommit struct {
	RunID        RunID
	PageSequence Sequence
	CursorBefore Cursor
	CursorAfter  Cursor
	CollectedAt  time.Time
	Attempts     []AttemptWrite
}

// CatalogPageCommit 是一个显式目录页面清单。目录页当前只记录覆盖证据与游标，
// 商品内容写入由目录同步实现补充。
type CatalogPageCommit struct {
	RunID        RunID
	PageSequence Sequence
	CursorBefore Cursor
	CursorAfter  Cursor
	CollectedAt  time.Time
}

// ScheduleStore 是调度器需要的持久化端口，由 storage/postgres 的 Store 满足。
type ScheduleStore interface {
	Targets(ctx context.Context) ([]Target, error)
	TransitionTarget(ctx context.Context, id TargetID, expected, expectedSwitch Revision, transition TargetTransition) (Target, error)
	CreateCatalogRun(ctx context.Context, targetID TargetID, expectedSwitch Revision, initialCursor Cursor) (Run, bool, error)
	CreateSummaryRun(ctx context.Context, targetID TargetID, appID int64, expectedSwitch Revision, initialCursor Cursor) (Run, bool, error)
	BeginRun(ctx context.Context, id RunID) (Run, error)
	FinishRun(ctx context.Context, id RunID, state RunState, completeness Completeness, reason RunReason) (Run, error)
	CommitSummaryPage(ctx context.Context, input SummaryPageCommit) (Page, bool, error)
	CommitCatalogPage(ctx context.Context, input CatalogPageCommit) (Page, bool, error)
	ListCombinations(ctx context.Context) ([]resource.AccountNodeCombination, error)
}

// RateLimitAdmitter 是限频准入端口，由 storage/postgres 的 Store 满足。
type RateLimitAdmitter interface {
	AdmitRateLimit(ctx context.Context, request ratelimit.Request) (ratelimit.Decision, error)
	ApplyRateLimitFeedback(ctx context.Context, admission ratelimit.Admission, scopes []ratelimit.Scope, reason ratelimit.ReasonCode, cooldown time.Duration) error
}

// PageFetch 是一次页面请求。凭据由适配器通过租约按需打开，不进入该结构。
type PageFetch struct {
	TaskType     TaskType
	Platform     Platform
	AppID        int64
	Side         market.Side
	PageSequence Sequence
	Cursor       Cursor
	Lease        resource.Lease
}

// FetchedPage 是一次成功页面响应的规整结果。页面采集时间由调度器在响应
// 处理完成时记录。
type FetchedPage struct {
	CursorAfter Cursor
	Attempts    []AttemptWrite
	Final       bool
}

// PageFetcher 由平台适配器实现，必须遵守请求 context 的取消与超时。
type PageFetcher interface {
	FetchPage(ctx context.Context, request PageFetch) (FetchedPage, error)
}

var (
	// ErrFetchSessionInvalid 表示平台登录会话已失效，需要人工替换。
	ErrFetchSessionInvalid = errors.New("platform session is invalid")
	// ErrFetchNetwork 表示请求在网络层失败。
	ErrFetchNetwork = errors.New("platform request failed on the network")
)

// RateLimitSignal 表示平台返回了限频信号。Scopes 只能选择本次准入实际
// 应用且有证据支持的范围；Cooldown 为零时使用策略的 fallback 冷却。
type RateLimitSignal struct {
	Scopes   []ratelimit.Scope
	Reason   ratelimit.ReasonCode
	Cooldown time.Duration
}

// Error 只输出固定文案，不携带平台原文。
func (signal *RateLimitSignal) Error() string { return "platform signaled rate limiting" }

// PlatformProfile 描述一个平台的调度事实：目标线路与接口类别。
// 具体取值由平台证据 Goal 确认后在装配层提供，这里不预置任何平台数字。
type PlatformProfile struct {
	TargetRegion    resource.TargetRegion
	CatalogEndpoint ratelimit.EndpointClass
	SummaryEndpoint ratelimit.EndpointClass
}

// SchedulerConfig 是单周期调度参数。
type SchedulerConfig struct {
	// Profiles 按平台提供线路与接口类别；缺失的平台目标会被标记为
	// invalid_config 并等待人工修复。
	Profiles map[Platform]PlatformProfile
	// PageTimeout 是单个页面请求的最长执行时间。
	PageTimeout time.Duration
	// ResourceWait 是单个页面等待可用组合的最长时间。
	ResourceWait time.Duration
	// PollInterval 是组合全部被占用时的重试间隔。
	PollInterval time.Duration
	// TransientRetry 是临时失败与资源阻塞后的自动复查间隔。
	TransientRetry time.Duration
	// SummaryPeriod 是摘要目标成功后的下一周期间隔。
	SummaryPeriod time.Duration
	// MaxParallelRuns 是一个周期内并行运行的上限。
	MaxParallelRuns int
	// Clock 缺省为 time.Now，仅用于测试注入。
	Clock func() time.Time
}

func (config SchedulerConfig) validate() error {
	for platform, profile := range config.Profiles {
		if err := platform.Validate(); err != nil {
			return fmt.Errorf("profile platform: %w", err)
		}
		if err := profile.TargetRegion.Validate(); err != nil {
			return fmt.Errorf("profile %s target region: %w", platform, err)
		}
		if profile.CatalogEndpoint != "" {
			if err := profile.CatalogEndpoint.Validate(); err != nil {
				return fmt.Errorf("profile %s catalog endpoint: %w", platform, err)
			}
		}
		if profile.SummaryEndpoint != "" {
			if err := profile.SummaryEndpoint.Validate(); err != nil {
				return fmt.Errorf("profile %s summary endpoint: %w", platform, err)
			}
		}
	}
	if config.PageTimeout <= 0 || config.ResourceWait <= 0 || config.PollInterval <= 0 ||
		config.TransientRetry <= 0 || config.SummaryPeriod <= 0 {
		return fmt.Errorf("scheduler durations must be positive")
	}
	if config.MaxParallelRuns < 1 {
		return fmt.Errorf("max parallel runs must be at least 1")
	}
	return nil
}

// Scheduler 执行一次调度周期：评估目标、派发运行、按页占用资源并逐页提交。
// 它不实现常驻循环、停止流转或重启恢复。
type Scheduler struct {
	store       ScheduleStore
	coordinator *resource.Coordinator
	admitter    RateLimitAdmitter
	fetcher     PageFetcher
	config      SchedulerConfig
}

// NewScheduler 校验依赖并创建单周期调度器。
func NewScheduler(
	store ScheduleStore,
	coordinator *resource.Coordinator,
	admitter RateLimitAdmitter,
	fetcher PageFetcher,
	config SchedulerConfig,
) (*Scheduler, error) {
	if store == nil {
		return nil, fmt.Errorf("nil schedule store")
	}
	if coordinator == nil {
		return nil, fmt.Errorf("nil resource coordinator")
	}
	if admitter == nil {
		return nil, fmt.Errorf("nil rate-limit admitter")
	}
	if fetcher == nil {
		return nil, fmt.Errorf("nil page fetcher")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Scheduler{
		store:       store,
		coordinator: coordinator,
		admitter:    admitter,
		fetcher:     fetcher,
		config:      config,
	}, nil
}

// RunOutcome 记录一个运行在本周期的结果。
type RunOutcome struct {
	RunID          RunID
	AppID          int64
	State          RunState
	Completeness   Completeness
	Reason         RunReason
	PagesCommitted int
	// LeftActive 表示运行被保留为续点，等待下一周期继续。
	LeftActive bool
	Err        error
}

// TargetOutcome 记录一个目标在本周期的结果。
type TargetOutcome struct {
	TargetID   TargetID
	TaskType   TaskType
	Platform   Platform
	Dispatched bool
	// Superseded 表示目标状态在周期内被其他所有者（例如禁用请求）接管，
	// 调度器放弃写回。
	Superseded bool
	// Target 是本周期结束时已知的最新目标状态。
	Target Target
	Runs   []RunOutcome
	Err    error
}

// CycleReport 汇总一次调度周期。
type CycleReport struct {
	StartedAt time.Time
	Targets   []TargetOutcome
}

func (s *Scheduler) now() time.Time {
	return s.config.Clock().UTC().Truncate(time.Microsecond)
}

// RunCycle 执行一次完整调度周期并等待全部派发的运行退出。
func (s *Scheduler) RunCycle(ctx context.Context) (CycleReport, error) {
	if ctx == nil {
		return CycleReport{}, fmt.Errorf("nil cycle context")
	}
	startedAt := s.now()
	targets, err := s.store.Targets(ctx)
	if err != nil {
		return CycleReport{}, fmt.Errorf("list collection targets: %w", err)
	}
	component, err := s.coordinator.RegisterComponent()
	if err != nil {
		return CycleReport{}, fmt.Errorf("register scheduler component: %w", err)
	}
	// 周期结束时所有页面租约都已释放，取消只回收组件登记。
	defer func() { _ = s.coordinator.CancelComponent(context.Background(), component) }()

	appIDs := catalogAppIDs(targets)
	now := s.now()
	due := make([]Target, 0, len(targets))
	for _, target := range targets {
		if targetDue(target, now) {
			due = append(due, target)
		}
	}

	semaphore := make(chan struct{}, s.config.MaxParallelRuns)
	outcomes := make([]TargetOutcome, len(due))
	var group sync.WaitGroup
	for index, target := range due {
		group.Add(1)
		go func(slot int, target Target) {
			defer group.Done()
			outcomes[slot] = s.processTarget(ctx, component, target, appIDs, semaphore)
		}(index, target)
	}
	group.Wait()

	sort.Slice(outcomes, func(left, right int) bool {
		return outcomes[left].TargetID < outcomes[right].TargetID
	})
	return CycleReport{StartedAt: startedAt, Targets: outcomes}, nil
}

// catalogAppIDs 返回全部目录目标声明的游戏集合。目录目标的存在定义系统
// 跟踪哪些游戏；目录开关只控制目录同步本身，不控制摘要方向。
func catalogAppIDs(targets []Target) []int64 {
	seen := make(map[int64]struct{})
	ids := make([]int64, 0)
	for _, target := range targets {
		if target.TaskType() != TaskTypeCatalog {
			continue
		}
		appID, ok := target.AppID()
		if !ok || appID < 1 {
			continue
		}
		if _, exists := seen[appID]; exists {
			continue
		}
		seen[appID] = struct{}{}
		ids = append(ids, appID)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}

// targetDue 判断目标是否应在本周期派发。actual 为 running 的目标同样派发：
// 单进程串行周期下没有并发所有者；若运行仍活动则续点同一运行，若运行已
// 终结、仅处置写回失败，则立即新建运行重跑（宁可提前重采也不把目标永久
// 钉死在 running）。
func targetDue(target Target, now time.Time) bool {
	if target.Desired() != DesiredEnabled {
		return false
	}
	switch target.Actual() {
	case ActualStarting, ActualRunning:
		return true
	case ActualWaiting, ActualBlocked:
		if target.Recovery() != RecoveryAutomatic {
			return false
		}
		recheckAt, ok := target.RecheckAt()
		return ok && !now.Before(recheckAt)
	default:
		return false
	}
}

type dispositionKind int

const (
	dispositionCompleted dispositionKind = iota
	dispositionWaiting
	dispositionBlocked
	dispositionError
	// dispositionDetached 表示目标状态归其他所有者（禁用流程或恢复流程），
	// 本周期不再改写。
	dispositionDetached
)

type disposition struct {
	kind      dispositionKind
	reason    TargetReason
	recheckAt time.Time
}

func waitingDisposition(reason TargetReason, recheckAt time.Time) disposition {
	return disposition{kind: dispositionWaiting, reason: reason, recheckAt: recheckAt}
}

func blockedDisposition(reason TargetReason, recheckAt time.Time) disposition {
	return disposition{kind: dispositionBlocked, reason: reason, recheckAt: recheckAt}
}

func errorDisposition(reason TargetReason) disposition {
	return disposition{kind: dispositionError, reason: reason}
}

type runSpec struct {
	appID    int64
	endpoint ratelimit.EndpointClass
	catalog  bool
}

func (s *Scheduler) processTarget(
	ctx context.Context,
	component resource.ComponentID,
	target Target,
	appIDs []int64,
	semaphore chan struct{},
) TargetOutcome {
	outcome := TargetOutcome{
		TargetID: target.ID(),
		TaskType: target.TaskType(),
		Platform: target.Platform(),
		Target:   target,
	}
	profile, hasProfile := s.config.Profiles[target.Platform()]
	specs, specErr := buildRunSpecs(target, profile, hasProfile, appIDs)
	if specErr != nil {
		outcome.Target, outcome.Superseded, outcome.Err =
			s.applyDisposition(ctx, target, blockedDisposition(TargetReasonInvalidConfig, time.Time{}))
		return outcome
	}
	if len(specs) == 0 {
		// 摘要目标没有任何目录目标声明的游戏可采：等待下一周期。
		outcome.Target, outcome.Superseded, outcome.Err = s.applyDisposition(ctx, target,
			waitingDisposition(TargetReasonNextCycle, s.now().Add(s.config.SummaryPeriod)))
		return outcome
	}

	updated, err := s.store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(),
		TargetTransition{State: ActualRunning})
	if errors.Is(err, ErrConflict) {
		// 目标被并发修改（例如被禁用），本周期放弃该目标。
		outcome.Superseded = true
		return outcome
	}
	if err != nil {
		outcome.Err = fmt.Errorf("mark target running: %w", err)
		return outcome
	}
	target = updated
	outcome.Target = target
	outcome.Dispatched = true

	runOutcomes := make([]RunOutcome, len(specs))
	dispositions := make([]disposition, len(specs))
	var group sync.WaitGroup
	for index, spec := range specs {
		group.Add(1)
		go func(slot int, spec runSpec) {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			runOutcomes[slot], dispositions[slot] = s.executeRun(ctx, component, target, spec)
		}(index, spec)
	}
	group.Wait()
	outcome.Runs = runOutcomes

	for _, runOutcome := range runOutcomes {
		outcome.Err = errors.Join(outcome.Err, runOutcome.Err)
	}
	final := s.aggregateDispositions(target, dispositions)
	if final.kind == dispositionDetached {
		return outcome
	}
	var applyErr error
	outcome.Target, outcome.Superseded, applyErr = s.applyDisposition(ctx, target, final)
	outcome.Err = errors.Join(outcome.Err, applyErr)
	return outcome
}

func buildRunSpecs(target Target, profile PlatformProfile, hasProfile bool, appIDs []int64) ([]runSpec, error) {
	if !hasProfile {
		return nil, fmt.Errorf("platform profile is missing")
	}
	switch target.TaskType() {
	case TaskTypeCatalog:
		if profile.CatalogEndpoint == "" {
			return nil, fmt.Errorf("catalog endpoint class is missing")
		}
		appID, ok := target.AppID()
		if !ok {
			return nil, fmt.Errorf("catalog target appid is missing")
		}
		return []runSpec{{appID: appID, endpoint: profile.CatalogEndpoint, catalog: true}}, nil
	case TaskTypeSummary:
		if profile.SummaryEndpoint == "" {
			return nil, fmt.Errorf("summary endpoint class is missing")
		}
		specs := make([]runSpec, 0, len(appIDs))
		for _, appID := range appIDs {
			specs = append(specs, runSpec{appID: appID, endpoint: profile.SummaryEndpoint})
		}
		return specs, nil
	default:
		return nil, fmt.Errorf("detail targets are not schedulable")
	}
}

// aggregateDispositions 取多游戏运行结果中最严重的目标处置。
func (s *Scheduler) aggregateDispositions(target Target, dispositions []disposition) disposition {
	final := disposition{kind: dispositionCompleted}
	rank := func(kind dispositionKind) int {
		switch kind {
		case dispositionDetached:
			return 4
		case dispositionError:
			return 3
		case dispositionBlocked:
			return 2
		case dispositionWaiting:
			return 1
		default:
			return 0
		}
	}
	for _, candidate := range dispositions {
		if rank(candidate.kind) > rank(final.kind) {
			final = candidate
			continue
		}
		if candidate.kind != final.kind {
			continue
		}
		switch candidate.kind {
		case dispositionBlocked:
			// 手动阻塞优先于自动阻塞；同为自动时取最晚复查时间。
			if final.recheckAt.IsZero() {
				continue
			}
			if candidate.recheckAt.IsZero() || candidate.recheckAt.After(final.recheckAt) {
				final = candidate
			}
		case dispositionWaiting:
			if candidate.recheckAt.After(final.recheckAt) {
				final = candidate
			}
		}
	}
	if final.kind == dispositionCompleted {
		now := s.now()
		interval := s.config.SummaryPeriod
		if period, ok := target.Period(); ok {
			interval = period
		}
		final = waitingDisposition(TargetReasonNextCycle, now.Add(interval))
	}
	return final
}

// applyDisposition 把处置写回目标状态；CAS 冲突表示状态已被其他所有者接管，
// 返回 superseded 供报告观测。写回失败时目标留在 running，下一周期照常派发。
func (s *Scheduler) applyDisposition(ctx context.Context, target Target, final disposition) (Target, bool, error) {
	transition := TargetTransition{}
	switch final.kind {
	case dispositionWaiting:
		recheck := s.ensureFuture(final.recheckAt)
		transition = TargetTransition{State: ActualWaiting, Reason: final.reason, RecheckAt: &recheck}
	case dispositionBlocked:
		transition = TargetTransition{State: ActualBlocked, Reason: final.reason}
		if recovery, ok := blockedRecovery(final.reason); ok && recovery == RecoveryAutomatic {
			recheck := s.ensureFuture(final.recheckAt)
			transition.RecheckAt = &recheck
		}
	case dispositionError:
		transition = TargetTransition{State: ActualError, Reason: final.reason}
	default:
		return target, false, fmt.Errorf("disposition cannot be applied")
	}
	updated, err := s.store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), transition)
	if errors.Is(err, ErrConflict) {
		return target, true, nil
	}
	if err != nil {
		return target, false, fmt.Errorf("apply target disposition: %w", err)
	}
	return updated, false, nil
}

func (s *Scheduler) ensureFuture(at time.Time) time.Time {
	now := s.now()
	if at.After(now) {
		return at.UTC().Truncate(time.Microsecond)
	}
	return now.Add(s.config.PollInterval)
}

func (s *Scheduler) executeRun(
	ctx context.Context,
	component resource.ComponentID,
	target Target,
	spec runSpec,
) (RunOutcome, disposition) {
	outcome := RunOutcome{AppID: spec.appID}
	var run Run
	var err error
	if spec.catalog {
		run, _, err = s.store.CreateCatalogRun(ctx, target.ID(), target.SwitchVersion(), Cursor{})
	} else {
		run, _, err = s.store.CreateSummaryRun(ctx, target.ID(), spec.appID, target.SwitchVersion(), Cursor{})
	}
	if err != nil {
		if errors.Is(err, ErrTargetDisabled) || errors.Is(err, ErrConflict) {
			return outcome, disposition{kind: dispositionDetached}
		}
		outcome.Err = fmt.Errorf("create run: %w", err)
		return outcome, waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry))
	}
	outcome.RunID = run.ID()
	if run.State() == RunPending {
		run, err = s.store.BeginRun(ctx, run.ID())
		if err != nil {
			if errors.Is(err, ErrTargetDisabled) || errors.Is(err, ErrConflict) {
				return outcome, disposition{kind: dispositionDetached}
			}
			outcome.Err = fmt.Errorf("begin run: %w", err)
			outcome.LeftActive = true
			return outcome, waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry))
		}
	}
	outcome.State = run.State()

	for {
		if ctx.Err() != nil {
			return s.stopRun(ctx, run, outcome)
		}
		lease, acquireDisp, acquired := s.acquireLease(ctx, component, target.Platform())
		if !acquired {
			if acquireDisp.kind == dispositionDetached {
				return s.stopRun(ctx, run, outcome)
			}
			outcome.State = run.State()
			outcome.LeftActive = true
			return outcome, acquireDisp
		}
		pageOutcome := s.executePage(ctx, target, spec, run, lease)
		run = pageOutcome.run
		if pageOutcome.committed {
			outcome.PagesCommitted++
		}
		if pageOutcome.err != nil {
			outcome.Err = pageOutcome.err
		}
		if !pageOutcome.stop {
			continue
		}
		if pageOutcome.finished {
			outcome.State = pageOutcome.state
			outcome.Completeness = pageOutcome.completeness
			outcome.Reason = pageOutcome.reason
		} else {
			outcome.State = run.State()
			outcome.LeftActive = true
		}
		return outcome, pageOutcome.disp
	}
}

// stopRun 在周期取消时结束运行；目标状态留给恢复流程。
func (s *Scheduler) stopRun(ctx context.Context, run Run, outcome RunOutcome) (RunOutcome, disposition) {
	finished, err := s.store.FinishRun(context.WithoutCancel(ctx), run.ID(), RunStopped, CompletenessPartial, RunReasonCancelled)
	if err != nil {
		outcome.Err = fmt.Errorf("stop cancelled run: %w", err)
		outcome.LeftActive = true
		return outcome, disposition{kind: dispositionDetached}
	}
	outcome.State = finished.State()
	outcome.Completeness = finished.Completeness()
	outcome.Reason = finished.Reason()
	return outcome, disposition{kind: dispositionDetached}
}

type pageOutcome struct {
	run          Run
	committed    bool
	stop         bool
	finished     bool
	state        RunState
	completeness Completeness
	reason       RunReason
	disp         disposition
	err          error
}

// executePage 在一次租约占用内完成准入、请求与提交；返回前释放租约。
func (s *Scheduler) executePage(
	ctx context.Context,
	target Target,
	spec runSpec,
	run Run,
	lease resource.Lease,
) pageOutcome {
	defer func() { _ = s.coordinator.Release(lease.Token) }()
	outcome := pageOutcome{run: run}

	request, err := ratelimit.RequestFromLease(lease, spec.endpoint)
	if err != nil {
		outcome.stop = true
		outcome.disp = errorDisposition(TargetReasonSchedulerFailure)
		outcome.err = fmt.Errorf("build rate-limit request: %w", err)
		return outcome
	}
	decision, err := s.admitter.AdmitRateLimit(ctx, request)
	if err != nil {
		outcome.stop = true
		if errors.Is(err, ratelimit.ErrPolicyUnavailable) {
			outcome.disp = blockedDisposition(TargetReasonMissingRatePolicy, time.Time{})
			return outcome
		}
		outcome.disp = waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry))
		outcome.err = fmt.Errorf("rate-limit admission: %w", err)
		return outcome
	}
	if !decision.Allowed() {
		outcome.stop = true
		outcome.disp = blockedDisposition(TargetReasonCooldown, decision.RetryAt())
		return outcome
	}
	admission := decision.Admission()

	pageSequence := Sequence(run.LastPageSequence() + 1)
	side, _ := target.Side()
	fetchCtx, cancel := context.WithTimeout(lease.Context(), s.config.PageTimeout)
	fetched, err := s.fetcher.FetchPage(fetchCtx, PageFetch{
		TaskType:     target.TaskType(),
		Platform:     target.Platform(),
		AppID:        spec.appID,
		Side:         side,
		PageSequence: pageSequence,
		Cursor:       run.CurrentCursor(),
		Lease:        lease,
	})
	deadlineHit := errors.Is(fetchCtx.Err(), context.DeadlineExceeded)
	cancel()
	if err != nil {
		return s.mapFetchError(ctx, run, admission, err, deadlineHit, outcome)
	}

	// 页面与 attempt 的采集时间都以运行开始时间为下界，避免应用与数据库
	// 时钟偏斜把合法页面判成非法输入。
	collectedAt := s.now()
	attempts := fetched.Attempts
	if startedAt, started := run.StartedAt(); started {
		if collectedAt.Before(startedAt) {
			collectedAt = startedAt
		}
		attempts = clampAttemptTimes(attempts, startedAt)
	}
	var page Page
	if spec.catalog {
		page, _, err = s.store.CommitCatalogPage(ctx, CatalogPageCommit{
			RunID:        run.ID(),
			PageSequence: pageSequence,
			CursorBefore: run.CurrentCursor(),
			CursorAfter:  fetched.CursorAfter,
			CollectedAt:  collectedAt,
		})
	} else {
		page, _, err = s.store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID:        run.ID(),
			PageSequence: pageSequence,
			CursorBefore: run.CurrentCursor(),
			CursorAfter:  fetched.CursorAfter,
			CollectedAt:  collectedAt,
			Attempts:     attempts,
		})
	}
	if err != nil {
		return s.mapCommitError(ctx, run, err, outcome)
	}
	advanced, err := run.CommitPage(page)
	if err != nil {
		outcome.err = fmt.Errorf("advance run cursor: %w", err)
		return s.finishRun(ctx, run, RunFailed, RunReasonInternalError, errorDisposition(TargetReasonStateIntegrity), outcome)
	}
	outcome.run = advanced
	outcome.committed = true
	if fetched.Final {
		return s.succeedRun(ctx, advanced, outcome)
	}
	return outcome
}

// clampAttemptTimes 把早于运行开始时间的 attempt 观测时间抬到下界，
// 与页面采集时间的钳制口径一致；没有越界时直接复用原切片。
func clampAttemptTimes(attempts []AttemptWrite, floor time.Time) []AttemptWrite {
	needsClamp := false
	for _, attempt := range attempts {
		if attempt.Observation.CollectedAt.Before(floor) {
			needsClamp = true
			break
		}
	}
	if !needsClamp {
		return attempts
	}
	clamped := make([]AttemptWrite, len(attempts))
	copy(clamped, attempts)
	for i := range clamped {
		if clamped[i].Observation.CollectedAt.Before(floor) {
			clamped[i].Observation.CollectedAt = floor
		}
	}
	return clamped
}

func (s *Scheduler) mapFetchError(
	ctx context.Context,
	run Run,
	admission ratelimit.Admission,
	fetchErr error,
	deadlineHit bool,
	outcome pageOutcome,
) pageOutcome {
	var signal *RateLimitSignal
	switch {
	case errors.As(fetchErr, &signal):
		if err := s.admitter.ApplyRateLimitFeedback(ctx, admission, signal.Scopes, signal.Reason, signal.Cooldown); err != nil {
			outcome.err = fmt.Errorf("apply rate-limit feedback: %w", err)
		}
		recheck := s.now().Add(s.config.TransientRetry)
		if signal.Cooldown > 0 {
			recheck = s.now().Add(signal.Cooldown)
		}
		outcome.stop = true
		outcome.disp = blockedDisposition(TargetReasonCooldown, recheck)
		return outcome
	case errors.Is(fetchErr, ErrFetchSessionInvalid):
		return s.finishRun(ctx, run, RunFailed, RunReasonLoginInvalid, blockedDisposition(TargetReasonSessionInvalid, time.Time{}), outcome)
	case ctx.Err() != nil:
		stopped, disp := s.stopRun(ctx, run, RunOutcome{})
		outcome.stop = true
		outcome.finished = !stopped.LeftActive
		outcome.state = stopped.State
		outcome.completeness = stopped.Completeness
		outcome.reason = stopped.Reason
		outcome.disp = disp
		outcome.err = stopped.Err
		return outcome
	case deadlineHit || errors.Is(fetchErr, context.DeadlineExceeded):
		return s.finishRun(ctx, run, RunFailed, RunReasonTimeout,
			waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry)), outcome)
	case errors.Is(fetchErr, ErrFetchNetwork):
		return s.finishRun(ctx, run, RunFailed, RunReasonNetworkError,
			waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry)), outcome)
	default:
		return s.finishRun(ctx, run, RunFailed, RunReasonPlatformError,
			waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry)), outcome)
	}
}

func (s *Scheduler) mapCommitError(ctx context.Context, run Run, commitErr error, outcome pageOutcome) pageOutcome {
	switch {
	case errors.Is(commitErr, ErrFence), errors.Is(commitErr, ErrTargetDisabled):
		return s.finishRun(ctx, run, RunStopped, RunReasonSwitchDisabled, disposition{kind: dispositionDetached}, outcome)
	case errors.Is(commitErr, ErrInvalidInput):
		outcome.err = fmt.Errorf("commit page: %w", commitErr)
		return s.finishRun(ctx, run, RunFailed, RunReasonSemanticError,
			waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry)), outcome)
	case errors.Is(commitErr, ErrPageOrder), errors.Is(commitErr, ErrPageConflict), errors.Is(commitErr, ErrIntegrity):
		outcome.err = fmt.Errorf("commit page: %w", commitErr)
		return s.finishRun(ctx, run, RunFailed, RunReasonInternalError, errorDisposition(TargetReasonStateIntegrity), outcome)
	default:
		outcome.err = fmt.Errorf("commit page: %w", commitErr)
		outcome.stop = true
		outcome.disp = waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry))
		return outcome
	}
}

func (s *Scheduler) finishRun(
	ctx context.Context,
	run Run,
	state RunState,
	reason RunReason,
	disp disposition,
	outcome pageOutcome,
) pageOutcome {
	finished, err := s.store.FinishRun(context.WithoutCancel(ctx), run.ID(), state, CompletenessPartial, reason)
	outcome.stop = true
	outcome.disp = disp
	if err != nil {
		if outcome.err == nil {
			outcome.err = fmt.Errorf("finish run: %w", err)
		}
		return outcome
	}
	outcome.finished = true
	outcome.state = finished.State()
	outcome.completeness = finished.Completeness()
	outcome.reason = finished.Reason()
	outcome.run = finished
	return outcome
}

func (s *Scheduler) succeedRun(ctx context.Context, run Run, outcome pageOutcome) pageOutcome {
	finished, err := s.store.FinishRun(context.WithoutCancel(ctx), run.ID(), RunSucceeded, CompletenessComplete, RunReasonNone)
	outcome.stop = true
	outcome.disp = disposition{kind: dispositionCompleted}
	if err != nil {
		outcome.err = fmt.Errorf("finish successful run: %w", err)
		outcome.disp = waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry))
		return outcome
	}
	outcome.finished = true
	outcome.state = finished.State()
	outcome.completeness = finished.Completeness()
	outcome.reason = finished.Reason()
	outcome.run = finished
	return outcome
}

// acquireLease 为一个页面请求占用一个可用组合。被占用时按 PollInterval
// 有界等待；没有任何组合或全部不可用时返回对应阻塞处置。
func (s *Scheduler) acquireLease(
	ctx context.Context,
	component resource.ComponentID,
	platform Platform,
) (resource.Lease, disposition, bool) {
	region := s.config.Profiles[platform].TargetRegion
	deadline := time.Now().Add(s.config.ResourceWait)
	for {
		if ctx.Err() != nil {
			return resource.Lease{}, disposition{kind: dispositionDetached}, false
		}
		combinations, err := s.store.ListCombinations(ctx)
		if err != nil {
			return resource.Lease{}, waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry)), false
		}
		candidates := make([]resource.CombinationID, 0, len(combinations))
		for _, combination := range combinations {
			if string(combination.Platform) == string(platform) {
				candidates = append(candidates, combination.ID)
			}
		}
		if len(candidates) == 0 {
			return resource.Lease{}, blockedDisposition(TargetReasonNoCombination, s.now().Add(s.config.TransientRetry)), false
		}
		occupied := false
		infrastructure := false
		for _, id := range candidates {
			lease, err := s.coordinator.AcquireCombination(ctx, component, id, region, s.now())
			if err == nil {
				return lease, disposition{}, true
			}
			switch {
			case errors.Is(err, resource.ErrResourceOccupied):
				occupied = true
			case errors.Is(err, resource.ErrComponentUnavailable), ctx.Err() != nil:
				return resource.Lease{}, disposition{kind: dispositionDetached}, false
			case errors.Is(err, resource.ErrResourceUnusable), errors.Is(err, resource.ErrCombinationNotFound):
				// 组合不可用：出口过期、正在重新验证或已被删除。
			default:
				infrastructure = true
			}
		}
		if !occupied {
			if infrastructure {
				return resource.Lease{}, waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry)), false
			}
			return resource.Lease{}, blockedDisposition(TargetReasonEgressUnavailable, s.now().Add(s.config.TransientRetry)), false
		}
		if time.Now().After(deadline) {
			return resource.Lease{}, waitingDisposition(TargetReasonSchedulerOpportunity, s.now().Add(s.config.TransientRetry)), false
		}
		select {
		case <-ctx.Done():
			return resource.Lease{}, disposition{kind: dispositionDetached}, false
		case <-time.After(s.config.PollInterval):
		}
	}
}
