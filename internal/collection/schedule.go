package collection

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
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
	// ErrFence 表示页面来自被禁用目标或旧开关版本。
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

// AttemptWrite 是一条行情事实，可携带尚未解析的平台身份。
type AttemptWrite struct {
	ProductID      catalog.ProductID // 0 表示尚未解析
	PlatformItemID string            // 可空（0B 前可能没有稳定键）
	ExactName      string            // Steam/平台展示名，逐字节
	Observation    market.Observation
	ReasonCode     string
	// Media 是商品的展示元数据，只用于控制台呈现，不参与行情判定。
	Media catalog.ProductMedia
}

// SummaryPageCommit 是一次成功抓取后写入的当前页。
type SummaryPageCommit struct {
	TargetID       TargetID
	TaskID         TaskID
	ExpectedSwitch Revision
	CursorBefore   Cursor
	CursorAfter    Cursor
	CollectedAt    time.Time
	Attempts       []AttemptWrite
	Payload        []byte
	AccountID      int64
	ExitAddress    netip.Addr
	AskTotal       int64
}

// ProductCatalog 给求购补货提供目录窗口。
type ProductCatalog interface {
	ListSteamProductsAfter(ctx context.Context, appID int64, after catalog.ProductID, limit int) ([]catalog.SteamProduct, error)
}

// ScheduleStore 是调度器需要的持久化端口。
type ScheduleStore interface {
	Targets(ctx context.Context) ([]Target, error)
	TransitionTarget(ctx context.Context, id TargetID, expected, expectedSwitch Revision, transition TargetTransition) (Target, error)
	QueueDepth(ctx context.Context, id TargetID) (int, error)
	EnqueueTasks(ctx context.Context, id TargetID, expectedSwitch Revision, specs []EnqueueSpec, cursor Cursor, total int64) error
	ClearTargetQueue(ctx context.Context, id TargetID) error
	ClaimTask(ctx context.Context, combinationID resource.CombinationID, platform Platform) (Task, Target, bool, error)
	ReleaseStaleClaims(ctx context.Context, olderThan time.Duration) (int, error)
	ReleaseAllClaims(ctx context.Context) (int, error)
	CompleteTask(ctx context.Context, id TaskID, combinationID resource.CombinationID) error
	RequeueTask(ctx context.Context, id TaskID, combinationID resource.CombinationID) error
	CommitSummaryPage(ctx context.Context, input SummaryPageCommit) (Page, bool, error)
	ListCombinationsFor(ctx context.Context, platform Platform) ([]resource.AccountNodeCombination, error)
	CombinationResources(ctx context.Context, id resource.CombinationID) (resource.CombinationResources, bool, error)
	ListWorkers(ctx context.Context) ([]WorkerSnapshot, error)
}

// RateLimitAdmitter 是限频准入端口，由 storage/postgres 的 Store 满足。
type RateLimitAdmitter interface {
	AdmitRateLimit(ctx context.Context, request ratelimit.Request) (ratelimit.Decision, error)
	ApplyRateLimitFeedback(ctx context.Context, admission ratelimit.Admission, scopes []ratelimit.Scope, reason ratelimit.ReasonCode, cooldown time.Duration) error
}

// PageFetch 是一次页面请求。页参数来自队列 payload，凭据由租约打开。
type PageFetch struct {
	TaskType   TaskType
	Platform   Platform
	AppID      int64
	Side       market.Side
	Kind       TaskKind
	Payload    []byte
	Lease      resource.Lease
	Sort        SortOrder
	PriceRange  PriceRange
	SteamFacets SteamFacets
}

// FetchedPage 是一次成功页面响应的规整结果。
type FetchedPage struct {
	Attempts   []AttemptWrite
	Payload    []byte
	TotalCount int64
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

// RateLimitSignal 表示平台返回了限频信号。
type RateLimitSignal struct {
	Scopes   []ratelimit.Scope
	Reason   ratelimit.ReasonCode
	Cooldown time.Duration
}

// Error 只输出固定文案，不携带平台原文。
func (signal *RateLimitSignal) Error() string { return "platform signaled rate limiting" }

// PlatformProfile 描述一个平台的调度事实：目标线路与摘要接口类别。
type PlatformProfile struct {
	TargetRegion    resource.TargetRegion
	SummaryEndpoint ratelimit.EndpointClass
	BidEndpoint     ratelimit.EndpointClass
}

// SchedulerConfig 是单周期调度参数。
type SchedulerConfig struct {
	Profiles map[Platform]PlatformProfile
	// PageTimeout 是单个页面请求的最长执行时间。
	PageTimeout time.Duration
	// TransientRetry 是临时失败与资源阻塞后的自动复查间隔。
	TransientRetry time.Duration
	// ClaimTimeout 是认领后未完成则回队的时限。
	ClaimTimeout time.Duration
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
		if profile.SummaryEndpoint != "" {
			if err := profile.SummaryEndpoint.Validate(); err != nil {
				return fmt.Errorf("profile %s summary endpoint: %w", platform, err)
			}
		}
		if profile.BidEndpoint != "" {
			if err := profile.BidEndpoint.Validate(); err != nil {
				return fmt.Errorf("profile %s bid endpoint: %w", platform, err)
			}
		}
	}
	if config.PageTimeout <= 0 || config.TransientRetry <= 0 || config.ClaimTimeout <= 0 {
		return fmt.Errorf("scheduler durations must be positive")
	}
	return nil
}

// Scheduler 执行一次调度周期：规划补货，再让空闲工人领任务。
type Scheduler struct {
	store       ScheduleStore
	catalog     ProductCatalog
	coordinator *resource.Coordinator
	admitter    RateLimitAdmitter
	fetcher     PageFetcher
	config      SchedulerConfig
}

// NewScheduler 校验依赖并创建单周期调度器。
func NewScheduler(
	store ScheduleStore,
	catalog ProductCatalog,
	coordinator *resource.Coordinator,
	admitter RateLimitAdmitter,
	fetcher PageFetcher,
	config SchedulerConfig,
) (*Scheduler, error) {
	if store == nil {
		return nil, fmt.Errorf("nil schedule store")
	}
	if catalog == nil {
		return nil, fmt.Errorf("nil product catalog")
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
		catalog:     catalog,
		coordinator: coordinator,
		admitter:    admitter,
		fetcher:     fetcher,
		config:      config,
	}, nil
}

// TargetOutcome 记录一个目标在本周期的规划结果。
type TargetOutcome struct {
	TargetID   TargetID
	TaskType   TaskType
	Platform   Platform
	Enqueued   int
	Superseded bool
	Target     Target
	Err        error
}

// WorkerOutcome 记录一个工人在本周期的领取结果。
type WorkerOutcome struct {
	CombinationID resource.CombinationID
	TaskID        TaskID
	TargetID      TargetID
	Committed     bool
	Err           error
}

// CycleReport 汇总一次调度周期。
type CycleReport struct {
	StartedAt time.Time
	Targets   []TargetOutcome
	Workers   []WorkerOutcome
}

func (s *Scheduler) now() time.Time {
	return s.config.Clock().UTC().Truncate(time.Microsecond)
}

// RunCycle 先回队超时认领，再规划补货，最后让空闲健康工人各领一条。
func (s *Scheduler) RunCycle(ctx context.Context) (CycleReport, error) {
	if ctx == nil {
		return CycleReport{}, fmt.Errorf("nil cycle context")
	}
	startedAt := s.now()
	if _, err := s.store.ReleaseStaleClaims(ctx, s.config.ClaimTimeout); err != nil {
		return CycleReport{}, fmt.Errorf("release stale claims: %w", err)
	}
	targets, err := s.store.Targets(ctx)
	if err != nil {
		return CycleReport{}, fmt.Errorf("list collection targets: %w", err)
	}
	component, err := s.coordinator.RegisterComponent()
	if err != nil {
		return CycleReport{}, fmt.Errorf("register scheduler component: %w", err)
	}
	defer func() { _ = s.coordinator.CancelComponent(context.Background(), component) }()

	planned := make([]TargetOutcome, 0, len(targets))
	for _, target := range targets {
		if target.Desired() != DesiredEnabled {
			continue
		}
		planned = append(planned, s.planTarget(ctx, target))
	}
	sort.Slice(planned, func(left, right int) bool {
		return planned[left].TargetID < planned[right].TargetID
	})

	workers, err := s.dispatchWorkers(ctx, component, targets)
	if err != nil {
		return CycleReport{StartedAt: startedAt, Targets: planned}, err
	}
	return CycleReport{StartedAt: startedAt, Targets: planned, Workers: workers}, nil
}

func (s *Scheduler) planTarget(ctx context.Context, target Target) TargetOutcome {
	outcome := TargetOutcome{
		TargetID: target.ID(),
		TaskType: target.TaskType(),
		Platform: target.Platform(),
		Target:   target,
	}
	profile, hasProfile := s.config.Profiles[target.Platform()]
	if !hasProfile || profile.TargetRegion.Validate() != nil {
		outcome.Target, outcome.Superseded, outcome.Err =
			s.applyDisposition(ctx, target, blockedDisposition(TargetReasonInvalidConfig, time.Time{}))
		return outcome
	}
	healthy, err := s.healthyWorkerCount(ctx, target.Platform(), profile.TargetRegion)
	if err != nil {
		outcome.Err = err
		return outcome
	}
	if healthy == 0 {
		outcome.Target, outcome.Superseded, outcome.Err =
			s.applyDisposition(ctx, target, blockedDisposition(TargetReasonNoCombination, s.now().Add(s.config.TransientRetry)))
		return outcome
	}
	depth, err := s.store.QueueDepth(ctx, target.ID())
	if err != nil {
		outcome.Err = err
		return outcome
	}
	if depth >= QueueWatermark {
		outcome.Target, outcome.Superseded, outcome.Err =
			s.applyDisposition(ctx, target, runningDisposition())
		return outcome
	}
	specs, cursor, total, err := s.buildRefill(ctx, target, QueueWatermark-depth)
	if err != nil {
		outcome.Err = err
		return outcome
	}
	if len(specs) > 0 {
		if err := s.store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), specs, cursor, total); err != nil {
			if errors.Is(err, ErrFence) || errors.Is(err, ErrConflict) || errors.Is(err, ErrTargetDisabled) {
				outcome.Superseded = true
				return outcome
			}
			outcome.Err = err
			return outcome
		}
		outcome.Enqueued = len(specs)
	}
	outcome.Target, outcome.Superseded, outcome.Err =
		s.applyDisposition(ctx, target, runningDisposition())
	return outcome
}

func (s *Scheduler) buildRefill(ctx context.Context, target Target, need int) ([]EnqueueSpec, Cursor, int64, error) {
	side, ok := target.Side()
	if !ok {
		return nil, Cursor{}, 0, fmt.Errorf("target side is required")
	}
	appID, ok := target.AppID()
	if !ok {
		return nil, Cursor{}, 0, fmt.Errorf("target appid is required")
	}
	if side == market.SideAsk {
		return s.buildAskRefill(target, need)
	}
	return s.buildBidRefill(ctx, target, appID, need)
}

func (s *Scheduler) buildAskRefill(target Target, need int) ([]EnqueueSpec, Cursor, int64, error) {
	start, err := DecodeAskRefill(target.RefillCursor())
	if err != nil {
		return nil, Cursor{}, 0, err
	}
	total := target.RefillTotal()
	specs := make([]EnqueueSpec, 0, need)
	for i := 0; i < need; i++ {
		payload, err := EncodeAskPage(start, AskPageSize)
		if err != nil {
			return nil, Cursor{}, 0, err
		}
		specs = append(specs, EnqueueSpec{Kind: TaskKindAskPage, Payload: payload})
		start += AskPageSize
		if total > 0 && int64(start) >= total {
			start = 0
		}
	}
	cursor, err := EncodeAskRefill(start)
	if err != nil {
		return nil, Cursor{}, 0, err
	}
	return specs, cursor, total, nil
}

func (s *Scheduler) buildBidRefill(ctx context.Context, target Target, appID int64, need int) ([]EnqueueSpec, Cursor, int64, error) {
	after, err := DecodeBidRefill(target.RefillCursor())
	if err != nil {
		return nil, Cursor{}, 0, err
	}
	specs := make([]EnqueueSpec, 0, need)
	wrapped := false
	for i := 0; i < need; i++ {
		products, err := s.catalog.ListSteamProductsAfter(ctx, appID, after, BidBatchSize)
		if err != nil {
			return nil, Cursor{}, 0, err
		}
		if len(products) == 0 {
			if after == 0 || wrapped {
				break
			}
			after = 0
			wrapped = true
			products, err = s.catalog.ListSteamProductsAfter(ctx, appID, 0, BidBatchSize)
			if err != nil {
				return nil, Cursor{}, 0, err
			}
			if len(products) == 0 {
				break
			}
		}
		payload, err := EncodeBidBatch(after, BidBatchSize)
		if err != nil {
			return nil, Cursor{}, 0, err
		}
		specs = append(specs, EnqueueSpec{Kind: TaskKindBidBatch, Payload: payload})
		after = products[len(products)-1].ProductID
	}
	cursor, err := EncodeBidRefill(after)
	if err != nil {
		return nil, Cursor{}, 0, err
	}
	return specs, cursor, 0, nil
}

func (s *Scheduler) healthyWorkerCount(ctx context.Context, platform Platform, region resource.TargetRegion) (int, error) {
	combinations, err := s.store.ListCombinationsFor(ctx, platform)
	if err != nil {
		return 0, err
	}
	now := s.now()
	count := 0
	for _, combination := range combinations {
		resources, found, err := s.store.CombinationResources(ctx, combination.ID)
		if err != nil || !found {
			if err != nil {
				return 0, err
			}
			continue
		}
		if resources.ValidateForUse(now, region) == nil {
			count++
		}
	}
	return count, nil
}

func (s *Scheduler) dispatchWorkers(
	ctx context.Context,
	component resource.ComponentID,
	targets []Target,
) ([]WorkerOutcome, error) {
	platforms := make(map[Platform]struct{})
	for _, target := range targets {
		if target.Desired() == DesiredEnabled {
			platforms[target.Platform()] = struct{}{}
		}
	}
	seen := make(map[resource.CombinationID]struct{})
	jobs := make([]resource.AccountNodeCombination, 0)
	for platform := range platforms {
		combinations, err := s.store.ListCombinationsFor(ctx, platform)
		if err != nil {
			return nil, err
		}
		profile := s.config.Profiles[platform]
		now := s.now()
		for _, combination := range combinations {
			if _, exists := seen[combination.ID]; exists {
				continue
			}
			resources, found, err := s.store.CombinationResources(ctx, combination.ID)
			if err != nil || !found {
				if err != nil {
					return nil, err
				}
				continue
			}
			if resources.ValidateForUse(now, profile.TargetRegion) != nil {
				continue
			}
			seen[combination.ID] = struct{}{}
			jobs = append(jobs, combination)
		}
	}
	outcomes := make([]WorkerOutcome, len(jobs))
	var group sync.WaitGroup
	for index, combination := range jobs {
		group.Add(1)
		go func(slot int, combination resource.AccountNodeCombination) {
			defer group.Done()
			outcomes[slot] = s.runWorker(ctx, component, combination)
		}(index, combination)
	}
	group.Wait()
	sort.Slice(outcomes, func(left, right int) bool {
		return outcomes[left].CombinationID < outcomes[right].CombinationID
	})
	return outcomes, nil
}

func (s *Scheduler) runWorker(
	ctx context.Context,
	component resource.ComponentID,
	combination resource.AccountNodeCombination,
) WorkerOutcome {
	outcome := WorkerOutcome{CombinationID: combination.ID}
	profile, ok := s.config.Profiles[Platform(combination.Platform)]
	if !ok {
		return outcome
	}
	lease, err := s.coordinator.AcquireCombination(ctx, component, combination.ID, profile.TargetRegion, s.now())
	if err != nil {
		if !errors.Is(err, resource.ErrResourceOccupied) &&
			!errors.Is(err, resource.ErrAccountSessionUnusable) &&
			!errors.Is(err, resource.ErrResourceUnusable) {
			outcome.Err = err
		}
		return outcome
	}
	defer func() { _ = s.coordinator.Release(lease.Token) }()

	task, target, found, err := s.store.ClaimTask(ctx, combination.ID, Platform(combination.Platform))
	if err != nil {
		outcome.Err = err
		return outcome
	}
	if !found {
		return outcome
	}
	outcome.TaskID = task.ID()
	outcome.TargetID = target.ID()
	return s.executeTask(ctx, lease, task, target, outcome)
}

func (s *Scheduler) executeTask(
	ctx context.Context,
	lease resource.Lease,
	task Task,
	target Target,
	outcome WorkerOutcome,
) WorkerOutcome {
	side, _ := target.Side()
	appID, _ := target.AppID()
	profile := s.config.Profiles[target.Platform()]
	endpoint := profile.SummaryEndpoint
	if side == market.SideBid && profile.BidEndpoint != "" {
		endpoint = profile.BidEndpoint
	}
	request, err := ratelimit.RequestFromLease(lease, endpoint)
	if err != nil {
		_ = s.store.RequeueTask(context.WithoutCancel(ctx), task.ID(), lease.Snapshot.CombinationID)
		outcome.Err = err
		return outcome
	}
	decision, err := s.admitter.AdmitRateLimit(ctx, request)
	if err != nil {
		_ = s.store.RequeueTask(context.WithoutCancel(ctx), task.ID(), lease.Snapshot.CombinationID)
		outcome.Err = err
		return outcome
	}
	if !decision.Allowed() {
		_ = s.store.RequeueTask(context.WithoutCancel(ctx), task.ID(), lease.Snapshot.CombinationID)
		return outcome
	}
	admission := decision.Admission()

	fetchCtx, cancel := context.WithTimeout(lease.Context(), s.config.PageTimeout)
	fetched, err := s.fetcher.FetchPage(fetchCtx, PageFetch{
		TaskType:   target.TaskType(),
		Platform:   target.Platform(),
		AppID:      appID,
		Side:       side,
		Kind:       task.Kind(),
		Payload:    task.Payload(),
		Lease:      lease,
		Sort:        target.Sort(),
		PriceRange:  target.PriceRange(),
		SteamFacets: target.SteamFacets(),
	})
	deadlineHit := errors.Is(fetchCtx.Err(), context.DeadlineExceeded)
	cancel()
	if err != nil {
		return s.mapFetchError(ctx, lease, task, target, admission, err, deadlineHit, outcome)
	}

	cursorBefore, err := taskCursor(task)
	if err != nil {
		_ = s.store.RequeueTask(context.WithoutCancel(ctx), task.ID(), lease.Snapshot.CombinationID)
		outcome.Err = err
		return outcome
	}
	collectedAt := s.now()
	_, _, err = s.store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID:       target.ID(),
		TaskID:         task.ID(),
		ExpectedSwitch: target.SwitchVersion(),
		CursorBefore:   cursorBefore,
		CursorAfter:    Cursor{},
		CollectedAt:    collectedAt,
		Attempts:       fetched.Attempts,
		Payload:        fetched.Payload,
		AccountID:      int64(lease.Snapshot.AccountID),
		ExitAddress:    lease.Snapshot.ExitAddress,
		AskTotal:       fetched.TotalCount,
	})
	if err != nil {
		if errors.Is(err, ErrFence) || errors.Is(err, ErrTargetDisabled) || errors.Is(err, ErrConflict) {
			_ = s.store.RequeueTask(context.WithoutCancel(ctx), task.ID(), lease.Snapshot.CombinationID)
			return outcome
		}
		_ = s.store.RequeueTask(context.WithoutCancel(ctx), task.ID(), lease.Snapshot.CombinationID)
		outcome.Err = err
		return outcome
	}
	if err := s.store.CompleteTask(ctx, task.ID(), lease.Snapshot.CombinationID); err != nil {
		outcome.Err = err
		return outcome
	}
	outcome.Committed = true
	return outcome
}

func taskCursor(task Task) (Cursor, error) {
	switch task.Kind() {
	case TaskKindAskPage:
		page, err := task.AskPage()
		if err != nil {
			return Cursor{}, err
		}
		return EncodeAskRefill(page.Start)
	case TaskKindBidBatch:
		batch, err := task.BidBatch()
		if err != nil {
			return Cursor{}, err
		}
		return EncodeBidRefill(batch.AfterID)
	default:
		return Cursor{}, fmt.Errorf("unknown task kind")
	}
}

func (s *Scheduler) mapFetchError(
	ctx context.Context,
	lease resource.Lease,
	task Task,
	target Target,
	admission ratelimit.Admission,
	fetchErr error,
	deadlineHit bool,
	outcome WorkerOutcome,
) WorkerOutcome {
	_ = s.store.RequeueTask(context.WithoutCancel(ctx), task.ID(), lease.Snapshot.CombinationID)
	var signal *RateLimitSignal
	switch {
	case errors.As(fetchErr, &signal):
		if err := s.admitter.ApplyRateLimitFeedback(ctx, admission, signal.Scopes, signal.Reason, signal.Cooldown); err != nil {
			outcome.Err = err
		}
	case errors.Is(fetchErr, ErrFetchSessionInvalid):
		// 账号失效由适配器落库；任务已回队，本工人本周期停领。
	case deadlineHit, errors.Is(fetchErr, context.DeadlineExceeded), errors.Is(fetchErr, ErrFetchNetwork):
		_, _, _ = s.applyDisposition(ctx, target, waitingDisposition(TargetReasonTransientFailure, s.now().Add(s.config.TransientRetry)))
	default:
		outcome.Err = fetchErr
	}
	return outcome
}

type dispositionKind int

const (
	dispositionRunning dispositionKind = iota
	dispositionWaiting
	dispositionBlocked
	dispositionError
	dispositionDetached
)

type disposition struct {
	kind      dispositionKind
	reason    TargetReason
	recheckAt time.Time
}

func runningDisposition() disposition {
	return disposition{kind: dispositionRunning}
}

func waitingDisposition(reason TargetReason, recheckAt time.Time) disposition {
	return disposition{kind: dispositionWaiting, reason: reason, recheckAt: recheckAt}
}

func blockedDisposition(reason TargetReason, recheckAt time.Time) disposition {
	return disposition{kind: dispositionBlocked, reason: reason, recheckAt: recheckAt}
}

func (s *Scheduler) applyDisposition(ctx context.Context, target Target, disp disposition) (Target, bool, error) {
	if target.Desired() != DesiredEnabled {
		return target, true, nil
	}
	var transition TargetTransition
	switch disp.kind {
	case dispositionRunning:
		transition = TargetTransition{State: ActualRunning}
	case dispositionWaiting:
		recheck := disp.recheckAt
		transition = TargetTransition{State: ActualWaiting, Reason: disp.reason, RecheckAt: &recheck}
	case dispositionBlocked:
		transition = TargetTransition{State: ActualBlocked, Reason: disp.reason}
		if !disp.recheckAt.IsZero() {
			recheck := disp.recheckAt
			transition.RecheckAt = &recheck
		}
	case dispositionError:
		transition = TargetTransition{State: ActualError, Reason: disp.reason}
	default:
		return target, true, nil
	}
	next, err := s.store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), transition)
	if errors.Is(err, ErrConflict) {
		return target, true, nil
	}
	if err != nil {
		return target, false, err
	}
	return next, false, nil
}
