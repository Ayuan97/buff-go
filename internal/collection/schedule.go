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

const schedulerCleanupTimeout = 5 * time.Second

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
	TargetID        TargetID
	TaskID          TaskID
	CombinationID   resource.CombinationID
	ClaimGeneration int64
	ExpectedSwitch  Revision
	CursorBefore    Cursor
	CursorAfter     Cursor
	CollectedAt     time.Time
	Attempts        []AttemptWrite
	Payload         []byte
	AccountID       int64
	ExitAddress     netip.Addr
	AskTotal        int64
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
	EnqueueTasksFenced(ctx context.Context, expected Target, specs []EnqueueSpec, cursor Cursor, total int64) error
	ClearTargetQueue(ctx context.Context, id TargetID) error
	ClaimTaskForTargets(ctx context.Context, combinationID resource.CombinationID, platform Platform, eligibleTargets []TargetID) (Task, Target, bool, error)
	ReleaseStaleClaims(ctx context.Context, olderThan time.Duration) (int, error)
	ReleaseStaleClaimsExcept(ctx context.Context, olderThan time.Duration, activeTasks []TaskID) (int, error)
	ReleaseAllClaims(ctx context.Context) (int, error)
	CompleteTask(ctx context.Context, id TaskID, combinationID resource.CombinationID, claimGeneration int64) error
	RequeueTask(ctx context.Context, id TaskID, combinationID resource.CombinationID, claimGeneration int64) error
	CommitSummaryPage(ctx context.Context, input SummaryPageCommit) (Page, bool, error)
	ListCombinationsFor(ctx context.Context, platform Platform) ([]resource.AccountNodeCombination, error)
	CombinationResources(ctx context.Context, id resource.CombinationID) (resource.CombinationResources, bool, error)
	ListWorkers(ctx context.Context) ([]WorkerSnapshot, error)
}

// CombinationResourceBatchStore 是调度器可选的平台级组合资源批量端口。
type CombinationResourceBatchStore interface {
	ListCombinationResourcesFor(ctx context.Context, platform Platform) ([]resource.CombinationResources, error)
}

// RateLimitAdmitter 是限频准入端口，由 storage/postgres 的 Store 满足。
type RateLimitAdmitter interface {
	AdmitRateLimit(ctx context.Context, request ratelimit.Request) (ratelimit.Decision, error)
	ApplyRateLimitFeedback(ctx context.Context, admission ratelimit.Admission, scopes []ratelimit.Scope, reason ratelimit.ReasonCode, cooldown time.Duration) error
}

// PageFetch 是一次页面请求。页参数来自队列 payload，凭据由租约打开。
type PageFetch struct {
	TaskType    TaskType
	Platform    Platform
	AppID       int64
	Side        market.Side
	Kind        TaskKind
	Payload     []byte
	Lease       resource.Lease
	Sort        SortOrder
	PriceRange  PriceRange
	SteamFacets SteamFacets
	// AdmitRequest 必须在每次真实平台 HTTP 请求前调用。
	AdmitRequest func(context.Context) (ratelimit.Admission, error)
	// RequestStarted 必须紧贴每次真实平台 HTTP 的发送动作调用。
	RequestStarted func()
}

// FetchedPage 是一次成功页面响应的规整结果。
type FetchedPage struct {
	Attempts   []AttemptWrite
	Payload    []byte
	TotalCount int64
	// CollectedAt is the persistent admission time immediately before HTTP.
	CollectedAt time.Time
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
	Admission ratelimit.Admission
	Scopes    []ratelimit.Scope
	Reason    ratelimit.ReasonCode
	Cooldown  time.Duration
}

// Error 只输出固定文案，不携带平台原文。
func (signal *RateLimitSignal) Error() string { return "platform signaled rate limiting" }

// RateLimitDeferred 表示本页超时前无法等到下一次准入。
type RateLimitDeferred struct {
	RetryAt time.Time
}

// Error 只输出固定文案，不携带策略细节。
func (deferred *RateLimitDeferred) Error() string { return "platform request is rate limited" }

// PlatformProfile 描述一个平台的调度事实：目标线路与摘要接口类别。
type PlatformProfile struct {
	TargetRegion    resource.TargetRegion
	SummaryEndpoint ratelimit.EndpointClass
	BidEndpoint     ratelimit.EndpointClass
	// RequestInterval 是单个接口、账号与出口 IP 组合的固定请求间隔。
	// 调度器只用它平滑独立工人的起跑时间，持久准入仍是限频权威。
	RequestInterval time.Duration
}

type requestLane struct {
	platform Platform
	endpoint ratelimit.EndpointClass
}

type requestPacer interface {
	Wait(context.Context, requestLane, workerRateKey, time.Duration, int) error
	SetReadyAt(workerRateKey, time.Time)
}

type smoothRequestPacer struct {
	mu           sync.Mutex
	nextSlot     map[requestLane]time.Time
	nextIdentity map[workerRateKey]time.Time
	lastCleanup  time.Time
}

func newSmoothRequestPacer() *smoothRequestPacer {
	return &smoothRequestPacer{
		nextSlot: make(map[requestLane]time.Time), nextIdentity: make(map[workerRateKey]time.Time),
	}
}

func (pacer *smoothRequestPacer) reserve(
	lane requestLane,
	now time.Time,
	interval time.Duration,
	workers int,
) time.Time {
	spacing := interval / time.Duration(workers)
	if spacing <= 0 {
		spacing = time.Nanosecond
	}
	pacer.mu.Lock()
	defer pacer.mu.Unlock()
	slot := now
	if next := pacer.nextSlot[lane]; next.After(slot) {
		slot = next
	}
	pacer.nextSlot[lane] = slot.Add(spacing)
	if pacer.lastCleanup.IsZero() || now.Sub(pacer.lastCleanup) >= time.Minute {
		for key, readyAt := range pacer.nextIdentity {
			if !readyAt.After(now) {
				delete(pacer.nextIdentity, key)
			}
		}
		pacer.lastCleanup = now
	}
	return slot
}

func (pacer *smoothRequestPacer) Wait(
	ctx context.Context,
	lane requestLane,
	identity workerRateKey,
	interval time.Duration,
	workers int,
) error {
	if ctx == nil {
		return fmt.Errorf("nil request pacing context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if workers < 1 {
		return fmt.Errorf("request pacing workers must be positive")
	}
	pacer.mu.Lock()
	identityReadyAt := pacer.nextIdentity[identity]
	pacer.mu.Unlock()
	if err := waitForRequestSlot(ctx, identityReadyAt); err != nil {
		return err
	}
	slot := pacer.reserve(lane, time.Now(), interval, workers)
	return waitForRequestSlot(ctx, slot)
}

func (pacer *smoothRequestPacer) SetReadyAt(identity workerRateKey, readyAt time.Time) {
	pacer.mu.Lock()
	defer pacer.mu.Unlock()
	if current := pacer.nextIdentity[identity]; readyAt.After(current) {
		pacer.nextIdentity[identity] = readyAt
	}
}

func waitForRequestSlot(ctx context.Context, slot time.Time) error {
	wait := time.Until(slot)
	if wait <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
		if profile.RequestInterval < 0 ||
			(profile.RequestInterval > 0 && profile.RequestInterval%time.Microsecond != 0) {
			return fmt.Errorf("profile %s request interval must be a non-negative whole number of microseconds", platform)
		}
		if profile.RequestInterval > 0 &&
			(profile.RequestInterval >= config.PageTimeout || profile.RequestInterval >= config.ClaimTimeout) {
			return fmt.Errorf("profile %s request interval must be shorter than page and claim timeouts", platform)
		}
		if profile.RequestInterval > ratelimit.MaxDuration {
			return fmt.Errorf("profile %s request interval is too long", platform)
		}
		if profile.RequestInterval > 0 && profile.SummaryEndpoint == "" {
			return fmt.Errorf("profile %s request pacing requires a summary endpoint", platform)
		}
	}
	if config.PageTimeout <= 0 || config.TransientRetry <= 0 || config.ClaimTimeout <= 0 {
		return fmt.Errorf("scheduler durations must be positive")
	}
	return nil
}

// Scheduler 执行一次调度周期：规划补货，再让空闲工人领任务。
type Scheduler struct {
	store                 ScheduleStore
	combinationBatchStore CombinationResourceBatchStore
	catalog               ProductCatalog
	coordinator           *resource.Coordinator
	admitter              RateLimitAdmitter
	fetcher               PageFetcher
	config                SchedulerConfig
	pacer                 requestPacer
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
	combinationBatchStore, _ := store.(CombinationResourceBatchStore)
	return &Scheduler{
		store:                 store,
		combinationBatchStore: combinationBatchStore,
		catalog:               catalog,
		coordinator:           coordinator,
		admitter:              admitter,
		fetcher:               fetcher,
		config:                config,
		pacer:                 newSmoothRequestPacer(),
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
	AccountID     resource.AccountID
	NodeID        resource.NodeID
	ExitAddress   netip.Addr
	TaskID        TaskID
	TargetID      TargetID
	AppID         int64
	Platform      Platform
	Side          market.Side
	Endpoint      ratelimit.EndpointClass
	Committed     bool
	RetryAt       time.Time
	Err           error
	retryKind     workerRetryKind
	retryReason   WorkerWaitReason
}

type workerRetryKind uint8

const (
	workerRetryRate workerRetryKind = iota + 1
	workerRetryNode
)

// CycleReport 汇总一次调度周期。
type CycleReport struct {
	StartedAt time.Time
	Targets   []TargetOutcome
	Workers   []WorkerOutcome
}

// cycleCombinationResources 保存单个 RunCycle 的平台资源快照。
type cycleCombinationResources struct {
	store  ScheduleStore
	batch  CombinationResourceBatchStore
	loaded map[Platform][]resource.CombinationResources
	errors map[Platform]error
}

func newCycleCombinationResources(store ScheduleStore, batch CombinationResourceBatchStore) *cycleCombinationResources {
	return &cycleCombinationResources{
		store:  store,
		batch:  batch,
		loaded: make(map[Platform][]resource.CombinationResources),
		errors: make(map[Platform]error),
	}
}

// load 按平台只加载一次，并缓存该平台的错误结果。
func (cache *cycleCombinationResources) load(ctx context.Context, platform Platform) ([]resource.CombinationResources, error) {
	if resources, ok := cache.loaded[platform]; ok {
		return resources, nil
	}
	if err, ok := cache.errors[platform]; ok {
		return nil, err
	}

	var (
		resources []resource.CombinationResources
		err       error
	)
	if cache.batch != nil {
		resources, err = cache.batch.ListCombinationResourcesFor(ctx, platform)
	} else {
		var combinations []resource.AccountNodeCombination
		combinations, err = cache.store.ListCombinationsFor(ctx, platform)
		if err == nil {
			resources = make([]resource.CombinationResources, 0, len(combinations))
			for _, combination := range combinations {
				current, found, currentErr := cache.store.CombinationResources(ctx, combination.ID)
				if currentErr != nil {
					err = currentErr
					break
				}
				if found {
					resources = append(resources, current)
				}
			}
		}
	}
	if err != nil {
		cache.errors[platform] = err
		return nil, err
	}
	cache.loaded[platform] = resources
	return resources, nil
}

func (cache *cycleCombinationResources) combinationIDs() map[resource.CombinationID]struct{} {
	ids := make(map[resource.CombinationID]struct{})
	for _, resources := range cache.loaded {
		for _, current := range resources {
			ids[current.Combination.ID] = struct{}{}
		}
	}
	return ids
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
		return CycleReport{StartedAt: startedAt}, fmt.Errorf("release stale claims: %w", err)
	}
	report, targets, combinationResources, err := s.planCycle(ctx)
	if err != nil {
		return report, err
	}
	component, err := s.coordinator.RegisterComponent()
	if err != nil {
		return report, fmt.Errorf("register scheduler component: %w", err)
	}
	defer func() { _ = s.coordinator.CancelComponent(context.Background(), component) }()
	report.Workers, err = s.dispatchWorkers(ctx, component, targets, combinationResources)
	return report, err
}

func (s *Scheduler) planCycle(
	ctx context.Context,
) (CycleReport, []Target, *cycleCombinationResources, error) {
	startedAt := s.now()
	targets, err := s.store.Targets(ctx)
	if err != nil {
		return CycleReport{StartedAt: startedAt}, nil, nil, fmt.Errorf("list collection targets: %w", err)
	}
	combinationResources := newCycleCombinationResources(s.store, s.combinationBatchStore)

	planned := make([]TargetOutcome, 0, len(targets))
	for _, target := range targets {
		if target.Desired() != DesiredEnabled {
			continue
		}
		planned = append(planned, s.planTarget(ctx, target, combinationResources))
	}
	sort.Slice(planned, func(left, right int) bool {
		return planned[left].TargetID < planned[right].TargetID
	})

	return CycleReport{StartedAt: startedAt, Targets: planned}, targets, combinationResources, nil
}

func (s *Scheduler) planResidentCycle(
	ctx context.Context,
	activeTasks []TaskID,
) (CycleReport, []Target, *cycleCombinationResources, error) {
	startedAt := s.now()
	if _, err := s.store.ReleaseStaleClaimsExcept(ctx, s.config.ClaimTimeout, activeTasks); err != nil {
		return CycleReport{StartedAt: startedAt}, nil, nil, fmt.Errorf("release stale claims: %w", err)
	}
	return s.planCycle(ctx)
}

func (s *Scheduler) planTarget(ctx context.Context, target Target, combinationResources *cycleCombinationResources) TargetOutcome {
	outcome := TargetOutcome{
		TargetID: target.ID(),
		TaskType: target.TaskType(),
		Platform: target.Platform(),
		Target:   target,
	}
	if !targetReadyAt(target, s.now()) {
		return outcome
	}
	profile, hasProfile := s.config.Profiles[target.Platform()]
	if !hasProfile || profile.TargetRegion.Validate() != nil {
		outcome.Target, outcome.Superseded, outcome.Err =
			s.applyDisposition(ctx, target, blockedDisposition(TargetReasonInvalidConfig, time.Time{}))
		return outcome
	}
	resources, err := combinationResources.load(ctx, target.Platform())
	if err != nil {
		outcome.Err = err
		return outcome
	}
	healthy := s.healthyWorkerCount(resources, profile.TargetRegion)
	if healthy == 0 {
		reason := unavailableCombinationReason(resources)
		recheckAt := time.Time{}
		if reason != TargetReasonSessionInvalid {
			recheckAt = s.now().Add(s.config.TransientRetry)
		}
		outcome.Target, outcome.Superseded, outcome.Err =
			s.applyDisposition(ctx, target, blockedDisposition(reason, recheckAt))
		return outcome
	}
	depth, err := s.store.QueueDepth(ctx, target.ID())
	if err != nil {
		outcome.Err = err
		return outcome
	}
	desiredDepth := QueueWatermark
	if healthy > desiredDepth {
		desiredDepth = healthy
	}
	if depth >= desiredDepth {
		outcome.Target, outcome.Superseded, outcome.Err =
			s.applyDisposition(ctx, target, runningDisposition())
		return outcome
	}
	specs, cursor, total, err := s.buildRefill(ctx, target, desiredDepth-depth)
	if err != nil {
		outcome.Err = err
		return outcome
	}
	if len(specs) > 0 {
		if err := s.store.EnqueueTasksFenced(ctx, target, specs, cursor, total); err != nil {
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
	if total == 0 && target.WriteSeq() > 0 {
		if need < QueueWatermark {
			return nil, target.RefillCursor(), total, nil
		}
		start = 0
		need = 1
	}
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
	startAfter := after
	specs := make([]EnqueueSpec, 0, need)
	products, err := s.catalog.ListSteamProductsAfter(ctx, appID, after, need)
	if err != nil {
		return nil, Cursor{}, 0, err
	}
	appendProducts := func(products []catalog.SteamProduct) error {
		for _, product := range products {
			if len(specs) == need {
				break
			}
			payload, err := EncodeBidBatch(after, BidBatchSize)
			if err != nil {
				return err
			}
			specs = append(specs, EnqueueSpec{Kind: TaskKindBidBatch, Payload: payload})
			after = product.ProductID
		}
		return nil
	}
	if err := appendProducts(products); err != nil {
		return nil, Cursor{}, 0, err
	}

	cycle := products
	if len(specs) < need && startAfter != 0 {
		cycle, err = s.catalog.ListSteamProductsAfter(ctx, appID, 0, need-len(specs))
		if err != nil {
			return nil, Cursor{}, 0, err
		}
	}
	// 非空 cycle 每轮都推进 specs，目录再小也会有限填满 need。
	for len(specs) < need && len(cycle) > 0 {
		after = 0
		if err := appendProducts(cycle); err != nil {
			return nil, Cursor{}, 0, err
		}
	}
	if len(specs) == 0 {
		after = 0
	}
	cursor, err := EncodeBidRefill(after)
	if err != nil {
		return nil, Cursor{}, 0, err
	}
	return specs, cursor, 0, nil
}

func (s *Scheduler) healthyWorkerCount(resources []resource.CombinationResources, region resource.TargetRegion) int {
	now := s.now()
	healthy := make([]resource.CombinationResources, 0, len(resources))
	for _, current := range resources {
		if current.ValidateForUse(now, region) == nil {
			healthy = append(healthy, current)
		}
	}
	return len(maximumIndependentCombinations(healthy))
}

// unavailableCombinationReason reports the furthest prerequisite reached by
// any combination. A usable session makes egress the remaining blocker.
func unavailableCombinationReason(resources []resource.CombinationResources) TargetReason {
	if len(resources) == 0 {
		return TargetReasonNoCombination
	}
	for _, current := range resources {
		if current.Account.SessionState == resource.AccountSessionStateValid ||
			current.Account.SessionState == resource.AccountSessionStateUnverified {
			return TargetReasonEgressUnavailable
		}
	}
	return TargetReasonSessionInvalid
}

func (s *Scheduler) dispatchWorkers(
	ctx context.Context,
	component resource.ComponentID,
	targets []Target,
	combinationResources *cycleCombinationResources,
) ([]WorkerOutcome, error) {
	outcomes, prepared, err := s.prepareWorkers(ctx, component, targets, combinationResources, workerExclusions{})
	if err != nil {
		return nil, err
	}
	workersByPlatform := make(map[Platform]int)
	for _, worker := range prepared {
		workersByPlatform[worker.target.Platform()]++
	}
	var group sync.WaitGroup
	for _, worker := range prepared {
		group.Add(1)
		go func(worker preparedWorker) {
			defer group.Done()
			defer func() { _ = s.coordinator.Release(worker.lease.Token) }()
			outcomes[worker.slot] = s.executeTask(
				ctx, worker.lease, worker.task, worker.target, worker.outcome,
				workersByPlatform[worker.target.Platform()],
			)
		}(worker)
	}
	group.Wait()
	sort.Slice(outcomes, func(left, right int) bool {
		return outcomes[left].CombinationID < outcomes[right].CombinationID
	})
	return outcomes, nil
}

type workerExclusions struct {
	combinations map[resource.CombinationID]struct{}
	rateLanes    map[workerRateKey]struct{}
	lastLanes    map[resource.CombinationID]requestLane
	accounts     map[resource.AccountID]struct{}
	nodes        map[workerNodeKey]struct{}
}

type workerNodeKey struct {
	nodeID   resource.NodeID
	platform resource.Platform
}

type workerRateKey struct {
	accountID   resource.AccountID
	exitAddress netip.Addr
	lane        requestLane
}

func (excluded workerExclusions) contains(resources resource.CombinationResources) bool {
	if _, found := excluded.combinations[resources.Combination.ID]; found {
		return true
	}
	if _, found := excluded.accounts[resources.Account.ID]; found {
		return true
	}
	_, found := excluded.nodes[workerNodeKey{
		nodeID: resources.Node.ID, platform: resources.Combination.Platform,
	}]
	return found
}

func (s *Scheduler) prepareWorkers(
	ctx context.Context,
	component resource.ComponentID,
	targets []Target,
	combinationResources *cycleCombinationResources,
	excluded workerExclusions,
) ([]WorkerOutcome, []preparedWorker, error) {
	platforms := make(map[Platform]struct{})
	now := s.now()
	for _, target := range targets {
		if target.Desired() == DesiredEnabled && targetReadyAt(target, now) {
			platforms[target.Platform()] = struct{}{}
		}
	}
	seen := make(map[resource.CombinationID]struct{})
	candidates := make([]resource.CombinationResources, 0)
	for platform := range platforms {
		resources, err := combinationResources.load(ctx, platform)
		if err != nil {
			return nil, nil, err
		}
		profile := s.config.Profiles[platform]
		for _, current := range resources {
			combination := current.Combination
			if _, exists := seen[combination.ID]; exists {
				continue
			}
			if current.ValidateForUse(now, profile.TargetRegion) != nil {
				continue
			}
			if excluded.contains(current) {
				continue
			}
			if !s.hasRunnableLane(current, targets, excluded.rateLanes, now) {
				continue
			}
			seen[combination.ID] = struct{}{}
			candidates = append(candidates, current)
		}
	}
	jobs := workerCombinationOrder(candidates)
	outcomes := make([]WorkerOutcome, len(jobs))
	prepared := make([]preparedWorker, 0, len(jobs))
	for index, combination := range jobs {
		worker, outcome, ready := s.prepareWorker(
			ctx, component, combination, targets, excluded.rateLanes, excluded.lastLanes, index,
		)
		outcomes[index] = outcome
		if ready {
			prepared = append(prepared, worker)
		}
	}
	return outcomes, prepared, nil
}

func (s *Scheduler) hasRunnableLane(
	resources resource.CombinationResources,
	targets []Target,
	blocked map[workerRateKey]struct{},
	now time.Time,
) bool {
	if resources.Node.ExitVerification == nil {
		return true
	}
	// Sticky nodes may rotate to a new exit between cached planning snapshots.
	// Their authoritative lease is the only safe place to apply an exact exit cooldown.
	if resources.Node.EgressMode == resource.EgressModeSticky {
		return true
	}
	for _, target := range targets {
		if target.Platform() != Platform(resources.Combination.Platform) ||
			target.Desired() != DesiredEnabled || !targetReadyAt(target, now) {
			continue
		}
		key := workerRateKey{
			accountID:   resources.Account.ID,
			exitAddress: resources.Node.ExitVerification.Address,
			lane:        s.requestLane(target),
		}
		if _, found := blocked[key]; !found {
			return true
		}
	}
	return false
}

func (s *Scheduler) eligibleWorkerTargets(
	targets []Target,
	lease resource.Lease,
	blocked map[workerRateKey]struct{},
	lastLanes map[resource.CombinationID]requestLane,
) ([]TargetID, []TargetID) {
	if blocked == nil {
		return nil, nil
	}
	now := s.now()
	byLane := make(map[requestLane][]TargetID)
	lanes := make([]requestLane, 0)
	for _, target := range targets {
		if target.Platform() != Platform(lease.Snapshot.Platform) || target.Desired() != DesiredEnabled ||
			!targetReadyAt(target, now) {
			continue
		}
		lane := s.requestLane(target)
		key := workerRateKey{
			accountID: lease.Snapshot.AccountID, exitAddress: lease.Snapshot.ExitAddress,
			lane: lane,
		}
		if _, found := blocked[key]; !found {
			if _, exists := byLane[lane]; !exists {
				lanes = append(lanes, lane)
			}
			byLane[lane] = append(byLane[lane], target.ID())
		}
	}
	if len(lanes) == 0 {
		return []TargetID{}, nil
	}
	sort.Slice(lanes, func(left, right int) bool {
		if lanes[left].platform != lanes[right].platform {
			return lanes[left].platform < lanes[right].platform
		}
		return lanes[left].endpoint < lanes[right].endpoint
	})
	selected := int(lease.Snapshot.CombinationID) % len(lanes)
	if previous, found := lastLanes[lease.Snapshot.CombinationID]; found {
		for index, lane := range lanes {
			if lane == previous {
				selected = (index + 1) % len(lanes)
				break
			}
		}
	}
	preferred := append([]TargetID(nil), byLane[lanes[selected]]...)
	if len(lanes) == 1 {
		return preferred, nil
	}
	fallback := make([]TargetID, 0, len(targets)-len(preferred))
	for offset := 1; offset < len(lanes); offset++ {
		fallback = append(fallback, byLane[lanes[(selected+offset)%len(lanes)]]...)
	}
	return preferred, fallback
}

func workerCombinationOrder(candidates []resource.CombinationResources) []resource.AccountNodeCombination {
	selected := maximumIndependentCombinations(candidates)
	selectedIDs := make(map[resource.CombinationID]struct{}, len(selected))
	for _, combination := range selected {
		selectedIDs[combination.ID] = struct{}{}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].Combination.ID < candidates[right].Combination.ID
	})
	ordered := append([]resource.AccountNodeCombination(nil), selected...)
	for _, candidate := range candidates {
		if _, found := selectedIDs[candidate.Combination.ID]; !found {
			ordered = append(ordered, candidate.Combination)
		}
	}
	return ordered
}

func maximumIndependentCombinations(
	candidates []resource.CombinationResources,
) []resource.AccountNodeCombination {
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].Combination.ID < candidates[right].Combination.ID
	})
	byAccount := make(map[resource.AccountID][]resource.CombinationResources)
	accounts := make([]resource.AccountID, 0)
	for _, candidate := range candidates {
		accountID := candidate.Account.ID
		if _, found := byAccount[accountID]; !found {
			accounts = append(accounts, accountID)
		}
		byAccount[accountID] = append(byAccount[accountID], candidate)
	}
	sort.Slice(accounts, func(left, right int) bool { return accounts[left] < accounts[right] })
	matchedNodes := make(map[workerNodeKey]resource.CombinationResources)
	var match func(resource.AccountID, map[workerNodeKey]struct{}) bool
	match = func(accountID resource.AccountID, visited map[workerNodeKey]struct{}) bool {
		for _, candidate := range byAccount[accountID] {
			node := workerNodeKey{
				nodeID: candidate.Node.ID, platform: candidate.Combination.Platform,
			}
			if _, seen := visited[node]; seen {
				continue
			}
			visited[node] = struct{}{}
			previous, occupied := matchedNodes[node]
			if !occupied || match(previous.Account.ID, visited) {
				matchedNodes[node] = candidate
				return true
			}
		}
		return false
	}
	for _, accountID := range accounts {
		match(accountID, make(map[workerNodeKey]struct{}))
	}
	selected := make([]resource.AccountNodeCombination, 0, len(matchedNodes))
	for _, candidate := range matchedNodes {
		selected = append(selected, candidate.Combination)
	}
	sort.Slice(selected, func(left, right int) bool { return selected[left].ID < selected[right].ID })
	return selected
}

type preparedWorker struct {
	slot    int
	lane    requestLane
	lease   resource.Lease
	task    Task
	target  Target
	outcome WorkerOutcome
}

type residentFlight struct {
	lane      requestLane
	accountID resource.AccountID
	node      workerNodeKey
	claim     WorkerRuntimeClaim
}

type workerRetry struct {
	retryAt time.Time
	reason  WorkerWaitReason
	side    market.Side
}

// residentDispatcher lets each leased combination finish and refill
// independently while the daemon keeps planning on its own interval.
type residentDispatcher struct {
	scheduler *Scheduler
	component resource.ComponentID
	ctx       context.Context
	cancel    context.CancelFunc
	wake      chan struct{}

	mu               sync.Mutex
	closed           bool
	inFlight         map[resource.CombinationID]residentFlight
	rateRetryAt      map[workerRateKey]workerRetry
	nodeRetryAt      map[workerNodeKey]workerRetry
	transientRetryAt map[resource.CombinationID]workerRetry
	lastLanes        map[resource.CombinationID]requestLane
	targets          []Target
	resources        *cycleCombinationResources
	completed        []WorkerOutcome
	wg               sync.WaitGroup
}

func (s *Scheduler) newResidentDispatcher(ctx context.Context) (*residentDispatcher, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil resident dispatcher context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.config.ClaimTimeout-s.config.PageTimeout <= 2*schedulerCleanupTimeout {
		return nil, fmt.Errorf("resident claim timeout must leave time for commit and cleanup")
	}
	component, err := s.coordinator.RegisterComponent()
	if err != nil {
		return nil, fmt.Errorf("register resident scheduler component: %w", err)
	}
	executionCtx, cancel := context.WithCancel(ctx)
	return &residentDispatcher{
		scheduler:        s,
		component:        component,
		ctx:              executionCtx,
		cancel:           cancel,
		wake:             make(chan struct{}, 1),
		inFlight:         make(map[resource.CombinationID]residentFlight),
		rateRetryAt:      make(map[workerRateKey]workerRetry),
		nodeRetryAt:      make(map[workerNodeKey]workerRetry),
		transientRetryAt: make(map[resource.CombinationID]workerRetry),
		lastLanes:        make(map[resource.CombinationID]requestLane),
	}, nil
}

func (dispatcher *residentDispatcher) Wake() <-chan struct{} { return dispatcher.wake }

func (dispatcher *residentDispatcher) Drain() []WorkerOutcome {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	outcomes := append([]WorkerOutcome(nil), dispatcher.completed...)
	dispatcher.completed = dispatcher.completed[:0]
	sort.Slice(outcomes, func(left, right int) bool {
		return outcomes[left].CombinationID < outcomes[right].CombinationID
	})
	return outcomes
}

func (dispatcher *residentDispatcher) BusyTargets() map[TargetID]struct{} {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	targets := make(map[TargetID]struct{})
	for _, flight := range dispatcher.inFlight {
		targets[flight.claim.TargetID] = struct{}{}
	}
	return targets
}

func (dispatcher *residentDispatcher) ActiveTasks() []TaskID {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	tasks := make([]TaskID, 0, len(dispatcher.inFlight))
	for _, flight := range dispatcher.inFlight {
		tasks = append(tasks, flight.claim.TaskID)
	}
	sort.Slice(tasks, func(left, right int) bool { return tasks[left] < tasks[right] })
	return tasks
}

func (dispatcher *residentDispatcher) NextRetryAt() time.Time {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	var next time.Time
	for _, retry := range dispatcher.rateRetryAt {
		if next.IsZero() || retry.retryAt.Before(next) {
			next = retry.retryAt
		}
	}
	for _, retry := range dispatcher.nodeRetryAt {
		if next.IsZero() || retry.retryAt.Before(next) {
			next = retry.retryAt
		}
	}
	for _, retry := range dispatcher.transientRetryAt {
		if next.IsZero() || retry.retryAt.Before(next) {
			next = retry.retryAt
		}
	}
	return next
}

// RuntimeSnapshot returns active claims and retry fences under one dispatcher lock.
func (dispatcher *residentDispatcher) RuntimeSnapshot() WorkerRuntimeSnapshot {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	now := dispatcher.scheduler.now()
	snapshot := WorkerRuntimeSnapshot{
		Claims: make([]WorkerRuntimeClaim, 0, len(dispatcher.inFlight)),
		Waits:  make([]WorkerWait, 0, len(dispatcher.rateRetryAt)+len(dispatcher.nodeRetryAt)+len(dispatcher.transientRetryAt)),
	}
	for _, flight := range dispatcher.inFlight {
		snapshot.Claims = append(snapshot.Claims, flight.claim)
	}
	for key, retry := range dispatcher.rateRetryAt {
		if !now.Before(retry.retryAt) {
			continue
		}
		snapshot.Waits = append(snapshot.Waits, WorkerWait{
			Scope: WorkerWaitScopeRate, Reason: retry.reason, RetryAt: retry.retryAt,
			Platform: key.lane.platform, Endpoint: key.lane.endpoint, Side: retry.side,
			AccountID: key.accountID, ExitAddress: key.exitAddress.String(),
		})
	}
	for key, retry := range dispatcher.nodeRetryAt {
		if !now.Before(retry.retryAt) {
			continue
		}
		snapshot.Waits = append(snapshot.Waits, WorkerWait{
			Scope: WorkerWaitScopeNode, Reason: retry.reason, RetryAt: retry.retryAt,
			Platform: Platform(key.platform), Side: retry.side, NodeID: key.nodeID,
		})
	}
	for combinationID, retry := range dispatcher.transientRetryAt {
		if !now.Before(retry.retryAt) {
			continue
		}
		lane := dispatcher.lastLanes[combinationID]
		snapshot.Waits = append(snapshot.Waits, WorkerWait{
			Scope: WorkerWaitScopeCombination, Reason: retry.reason, RetryAt: retry.retryAt,
			Platform: lane.platform, Endpoint: lane.endpoint, Side: retry.side,
			CombinationID: combinationID,
		})
	}
	sort.Slice(snapshot.Claims, func(left, right int) bool {
		return snapshot.Claims[left].CombinationID < snapshot.Claims[right].CombinationID
	})
	sort.Slice(snapshot.Waits, func(left, right int) bool {
		return runtimeWaitLess(snapshot.Waits[left], snapshot.Waits[right])
	})
	return snapshot
}

func runtimeWaitLess(left, right WorkerWait) bool {
	if left.Scope != right.Scope {
		return left.Scope < right.Scope
	}
	if left.Platform != right.Platform {
		return left.Platform < right.Platform
	}
	if left.Endpoint != right.Endpoint {
		return left.Endpoint < right.Endpoint
	}
	if left.AccountID != right.AccountID {
		return left.AccountID < right.AccountID
	}
	if left.ExitAddress != right.ExitAddress {
		return left.ExitAddress < right.ExitAddress
	}
	if left.NodeID != right.NodeID {
		return left.NodeID < right.NodeID
	}
	if left.CombinationID != right.CombinationID {
		return left.CombinationID < right.CombinationID
	}
	if !left.RetryAt.Equal(right.RetryAt) {
		return left.RetryAt.Before(right.RetryAt)
	}
	if left.Reason != right.Reason {
		return left.Reason < right.Reason
	}
	return left.Side < right.Side
}

func (dispatcher *residentDispatcher) Dispatch(
	ctx context.Context,
	targets []Target,
	combinationResources *cycleCombinationResources,
) ([]WorkerOutcome, error) {
	currentCombinations := combinationResources.combinationIDs()
	dispatcher.mu.Lock()
	dispatcher.targets = append(dispatcher.targets[:0], targets...)
	dispatcher.resources = combinationResources
	for combinationID := range dispatcher.lastLanes {
		if _, found := currentCombinations[combinationID]; !found {
			delete(dispatcher.lastLanes, combinationID)
		}
	}
	dispatcher.mu.Unlock()
	return dispatcher.dispatch(ctx, targets, combinationResources)
}

func (dispatcher *residentDispatcher) DispatchCached(ctx context.Context) ([]WorkerOutcome, error) {
	dispatcher.mu.Lock()
	targets := append([]Target(nil), dispatcher.targets...)
	resources := dispatcher.resources
	dispatcher.mu.Unlock()
	if resources == nil {
		return nil, nil
	}
	return dispatcher.dispatch(ctx, targets, resources)
}

func (dispatcher *residentDispatcher) dispatch(
	ctx context.Context,
	targets []Target,
	combinationResources *cycleCombinationResources,
) ([]WorkerOutcome, error) {
	dispatcher.mu.Lock()
	if dispatcher.closed {
		dispatcher.mu.Unlock()
		return nil, context.Canceled
	}
	excluded := workerExclusions{
		combinations: make(map[resource.CombinationID]struct{}, len(dispatcher.transientRetryAt)),
		rateLanes:    make(map[workerRateKey]struct{}, len(dispatcher.rateRetryAt)),
		lastLanes:    make(map[resource.CombinationID]requestLane, len(dispatcher.lastLanes)),
		accounts:     make(map[resource.AccountID]struct{}, len(dispatcher.inFlight)),
		nodes:        make(map[workerNodeKey]struct{}, len(dispatcher.inFlight)+len(dispatcher.nodeRetryAt)),
	}
	now := dispatcher.scheduler.now()
	for key, retry := range dispatcher.rateRetryAt {
		if now.Before(retry.retryAt) {
			excluded.rateLanes[key] = struct{}{}
		} else {
			delete(dispatcher.rateRetryAt, key)
		}
	}
	for combinationID, retry := range dispatcher.transientRetryAt {
		if now.Before(retry.retryAt) {
			excluded.combinations[combinationID] = struct{}{}
		} else {
			delete(dispatcher.transientRetryAt, combinationID)
		}
	}
	for key, retry := range dispatcher.nodeRetryAt {
		if now.Before(retry.retryAt) {
			excluded.nodes[key] = struct{}{}
		} else {
			delete(dispatcher.nodeRetryAt, key)
		}
	}
	for combinationID, lane := range dispatcher.lastLanes {
		excluded.lastLanes[combinationID] = lane
	}
	for _, flight := range dispatcher.inFlight {
		excluded.accounts[flight.accountID] = struct{}{}
		excluded.nodes[flight.node] = struct{}{}
	}
	dispatcher.mu.Unlock()

	outcomes, prepared, err := dispatcher.scheduler.prepareWorkers(
		ctx, dispatcher.component, targets, combinationResources, excluded,
	)
	if err != nil {
		return nil, err
	}

	dispatcher.mu.Lock()
	if dispatcher.closed {
		dispatcher.mu.Unlock()
		for _, worker := range prepared {
			_ = dispatcher.scheduler.requeueTask(ctx, worker.task, worker.lease.Snapshot.CombinationID)
			_ = dispatcher.scheduler.coordinator.Release(worker.lease.Token)
		}
		return nil, context.Canceled
	}
	workersByPlatform := make(map[Platform]int)
	for _, flight := range dispatcher.inFlight {
		workersByPlatform[flight.lane.platform]++
	}
	for _, worker := range prepared {
		workersByPlatform[worker.target.Platform()]++
		side, _ := worker.target.Side()
		appID, _ := worker.target.AppID()
		claimedAt, ok := worker.task.ClaimedAt()
		if !ok {
			claimedAt = dispatcher.scheduler.now()
		}
		dispatcher.inFlight[worker.lease.Snapshot.CombinationID] = residentFlight{
			lane: worker.lane, accountID: worker.lease.Snapshot.AccountID,
			node: workerNodeKey{
				nodeID: worker.lease.Snapshot.NodeID, platform: worker.lease.Snapshot.Platform,
			},
			claim: WorkerRuntimeClaim{
				CombinationID: worker.lease.Snapshot.CombinationID,
				TargetID:      worker.target.ID(), TaskID: worker.task.ID(), AppID: appID,
				Side: side, Platform: worker.target.Platform(), Kind: worker.task.Kind(),
				Endpoint: worker.lane.endpoint, ClaimedAt: claimedAt,
			},
		}
		dispatcher.lastLanes[worker.lease.Snapshot.CombinationID] = worker.lane
	}
	dispatcher.wg.Add(len(prepared))
	dispatcher.mu.Unlock()

	started := make(map[int]struct{}, len(prepared))
	for _, worker := range prepared {
		started[worker.slot] = struct{}{}
		go dispatcher.execute(worker, workersByPlatform[worker.target.Platform()])
	}
	immediate := make([]WorkerOutcome, 0)
	for index, outcome := range outcomes {
		if _, running := started[index]; !running && outcome.Err != nil {
			immediate = append(immediate, outcome)
		}
	}
	return immediate, nil
}

func (dispatcher *residentDispatcher) execute(worker preparedWorker, activeWorkers int) {
	defer dispatcher.wg.Done()
	executionCtx, cancel := context.WithTimeout(
		dispatcher.ctx, dispatcher.scheduler.config.ClaimTimeout-2*schedulerCleanupTimeout,
	)
	outcome := dispatcher.scheduler.executeTask(
		executionCtx, worker.lease, worker.task, worker.target, worker.outcome,
		activeWorkers,
	)
	cancel()
	if err := dispatcher.scheduler.coordinator.Release(worker.lease.Token); err != nil &&
		!errors.Is(err, resource.ErrLeaseNotHeld) {
		outcome.Err = errors.Join(outcome.Err, err)
	}
	dispatcher.mu.Lock()
	delete(dispatcher.inFlight, worker.lease.Snapshot.CombinationID)
	rateKey := workerRateKey{
		accountID: worker.lease.Snapshot.AccountID, exitAddress: worker.lease.Snapshot.ExitAddress,
		lane: worker.lane,
	}
	nodeKey := workerNodeKey{
		nodeID: worker.lease.Snapshot.NodeID, platform: worker.lease.Snapshot.Platform,
	}
	if outcome.RetryAt.IsZero() && outcome.Err != nil {
		outcome.RetryAt = dispatcher.scheduler.now().Add(dispatcher.scheduler.config.TransientRetry)
	}
	if outcome.retryReason == "" && outcome.Err != nil {
		outcome.retryReason = WorkerWaitReasonTransient
	}
	if outcome.RetryAt.After(dispatcher.scheduler.now()) {
		side, _ := worker.target.Side()
		retry := workerRetry{retryAt: outcome.RetryAt, reason: outcome.retryReason, side: side}
		switch outcome.retryKind {
		case workerRetryRate:
			dispatcher.rateRetryAt[rateKey] = retry
		case workerRetryNode:
			dispatcher.nodeRetryAt[nodeKey] = retry
		default:
			dispatcher.transientRetryAt[worker.lease.Snapshot.CombinationID] = retry
		}
	} else {
		delete(dispatcher.rateRetryAt, rateKey)
		delete(dispatcher.nodeRetryAt, nodeKey)
		delete(dispatcher.transientRetryAt, worker.lease.Snapshot.CombinationID)
	}
	dispatcher.completed = append(dispatcher.completed, outcome)
	dispatcher.mu.Unlock()
	select {
	case dispatcher.wake <- struct{}{}:
	default:
	}
}

func (dispatcher *residentDispatcher) Close(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nil resident dispatcher close context")
	}
	dispatcher.mu.Lock()
	dispatcher.closed = true
	dispatcher.mu.Unlock()
	dispatcher.cancel()
	if err := dispatcher.scheduler.coordinator.CancelComponent(ctx, dispatcher.component); err != nil {
		return err
	}
	dispatcher.wg.Wait()
	return nil
}

func (s *Scheduler) prepareWorker(
	ctx context.Context,
	component resource.ComponentID,
	combination resource.AccountNodeCombination,
	targets []Target,
	blockedRateLanes map[workerRateKey]struct{},
	lastLanes map[resource.CombinationID]requestLane,
	slot int,
) (preparedWorker, WorkerOutcome, bool) {
	outcome := WorkerOutcome{CombinationID: combination.ID}
	profile, ok := s.config.Profiles[Platform(combination.Platform)]
	if !ok {
		return preparedWorker{}, outcome, false
	}
	lease, err := s.coordinator.AcquireCombination(ctx, component, combination.ID, profile.TargetRegion, s.now())
	if err != nil {
		if !errors.Is(err, resource.ErrResourceOccupied) &&
			!errors.Is(err, resource.ErrAccountSessionUnusable) &&
			!errors.Is(err, resource.ErrResourceUnusable) {
			outcome.Err = err
		}
		return preparedWorker{}, outcome, false
	}
	outcome.AccountID = lease.Snapshot.AccountID
	outcome.NodeID = lease.Snapshot.NodeID
	outcome.ExitAddress = lease.Snapshot.ExitAddress
	outcome.Platform = Platform(lease.Snapshot.Platform)
	eligibleTargets, fallbackTargets := s.eligibleWorkerTargets(targets, lease, blockedRateLanes, lastLanes)
	if eligibleTargets != nil && len(eligibleTargets) == 0 {
		_ = s.coordinator.Release(lease.Token)
		return preparedWorker{}, outcome, false
	}

	task, target, found, err := s.store.ClaimTaskForTargets(
		ctx, combination.ID, Platform(combination.Platform), eligibleTargets,
	)
	if err == nil && !found && len(fallbackTargets) > 0 {
		task, target, found, err = s.store.ClaimTaskForTargets(
			ctx, combination.ID, Platform(combination.Platform), fallbackTargets,
		)
	}
	if err != nil {
		outcome.Err = err
		_ = s.coordinator.Release(lease.Token)
		return preparedWorker{}, outcome, false
	}
	if !found {
		_ = s.coordinator.Release(lease.Token)
		return preparedWorker{}, outcome, false
	}
	outcome.TaskID = task.ID()
	outcome.TargetID = target.ID()
	outcome.AppID, _ = target.AppID()
	outcome.Side, _ = target.Side()
	outcome.Platform = target.Platform()
	outcome.Endpoint = s.requestLane(target).endpoint
	return preparedWorker{
		slot: slot, lane: s.requestLane(target),
		lease: lease, task: task, target: target, outcome: outcome,
	}, outcome, true
}

func (s *Scheduler) requestLane(target Target) requestLane {
	profile := s.config.Profiles[target.Platform()]
	endpoint := profile.SummaryEndpoint
	if side, ok := target.Side(); ok && side == market.SideBid && profile.BidEndpoint != "" {
		endpoint = profile.BidEndpoint
	}
	return requestLane{platform: target.Platform(), endpoint: endpoint}
}

func (s *Scheduler) executeTask(
	ctx context.Context,
	lease resource.Lease,
	task Task,
	target Target,
	outcome WorkerOutcome,
	activeWorkers int,
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
		requeueErr := s.requeueTask(ctx, task, lease.Snapshot.CombinationID)
		outcome.Err = errors.Join(err, requeueErr)
		return outcome
	}
	if err := lease.Context().Err(); err != nil {
		requeueErr := s.requeueTask(ctx, task, lease.Snapshot.CombinationID)
		outcome.Err = errors.Join(err, requeueErr)
		return outcome
	}
	fetchCtx, cancel := context.WithTimeout(ctx, s.config.PageTimeout)
	stopLeaseCancel := context.AfterFunc(lease.Context(), cancel)
	fetched, err := s.fetcher.FetchPage(fetchCtx, PageFetch{
		TaskType:    target.TaskType(),
		Platform:    target.Platform(),
		AppID:       appID,
		Side:        side,
		Kind:        task.Kind(),
		Payload:     task.Payload(),
		Lease:       lease,
		Sort:        target.Sort(),
		PriceRange:  target.PriceRange(),
		SteamFacets: target.SteamFacets(),
		AdmitRequest: func(requestCtx context.Context) (ratelimit.Admission, error) {
			return s.admitRequest(requestCtx, request, profile.RequestInterval, activeWorkers)
		},
		RequestStarted: func() {
			if profile.RequestInterval > 0 {
				s.pacer.SetReadyAt(workerRateKey{
					accountID: lease.Snapshot.AccountID, exitAddress: lease.Snapshot.ExitAddress,
					lane: s.requestLane(target),
				}, time.Now().Add(profile.RequestInterval))
			}
		},
	})
	fetchContextErr := fetchCtx.Err()
	deadlineHit := errors.Is(fetchContextErr, context.DeadlineExceeded)
	stopLeaseCancel()
	leaseContextErr := lease.Context().Err()
	cancel()
	if err != nil {
		return s.mapFetchError(ctx, lease, task, target, err, deadlineHit, outcome)
	}
	if fetchContextErr != nil || leaseContextErr != nil || ctx.Err() != nil {
		requeueErr := s.requeueTask(ctx, task, lease.Snapshot.CombinationID)
		outcome.Err = errors.Join(fetchContextErr, leaseContextErr, ctx.Err(), requeueErr)
		if errors.Is(fetchContextErr, context.DeadlineExceeded) {
			outcome.retryKind = workerRetryNode
			outcome.retryReason = WorkerWaitReasonTimeout
			outcome.RetryAt = s.now().Add(s.config.TransientRetry)
		}
		return outcome
	}

	cursorBefore, err := taskCursor(task)
	if err != nil {
		requeueErr := s.requeueTask(ctx, task, lease.Snapshot.CombinationID)
		outcome.Err = errors.Join(err, requeueErr)
		return outcome
	}
	collectedAt := fetched.CollectedAt
	if collectedAt.IsZero() {
		collectedAt = s.now()
	}
	_, _, err = s.store.CommitSummaryPage(ctx, SummaryPageCommit{
		TargetID:        target.ID(),
		TaskID:          task.ID(),
		CombinationID:   lease.Snapshot.CombinationID,
		ClaimGeneration: task.ClaimGeneration(),
		ExpectedSwitch:  target.SwitchVersion(),
		CursorBefore:    cursorBefore,
		CursorAfter:     Cursor{},
		CollectedAt:     collectedAt,
		Attempts:        fetched.Attempts,
		Payload:         fetched.Payload,
		AccountID:       int64(lease.Snapshot.AccountID),
		ExitAddress:     lease.Snapshot.ExitAddress,
		AskTotal:        fetched.TotalCount,
	})
	if err != nil {
		requeueErr := s.requeueTask(ctx, task, lease.Snapshot.CombinationID)
		if errors.Is(err, ErrFence) || errors.Is(err, ErrTargetDisabled) || errors.Is(err, ErrConflict) {
			outcome.Err = requeueErr
			return outcome
		}
		outcome.Err = errors.Join(err, requeueErr)
		return outcome
	}
	if err := s.store.CompleteTask(ctx, task.ID(), lease.Snapshot.CombinationID, task.ClaimGeneration()); err != nil {
		outcome.Err = err
		return outcome
	}
	outcome.Committed = true
	return outcome
}

func (s *Scheduler) admitRequest(
	ctx context.Context,
	request ratelimit.Request,
	interval time.Duration,
	activeWorkers int,
) (ratelimit.Admission, error) {
	lane := requestLane{platform: Platform(request.Platform()), endpoint: request.EndpointClass()}
	identity := workerRateKey{
		accountID: request.AccountID(), exitAddress: request.ExitAddress(), lane: lane,
	}
	for {
		if interval > 0 {
			if s.pacer == nil {
				return ratelimit.Admission{}, fmt.Errorf("request pacer is unavailable")
			}
			if err := s.pacer.Wait(ctx, lane, identity, interval, activeWorkers); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return ratelimit.Admission{}, &RateLimitDeferred{RetryAt: s.now().Add(interval)}
				}
				return ratelimit.Admission{}, err
			}
		}
		decision, err := s.admitter.AdmitRateLimit(ctx, request)
		if err != nil {
			return ratelimit.Admission{}, err
		}
		if decision.Allowed() {
			return decision.Admission(), nil
		}
		retryAt := decision.RetryAt()
		if interval > 0 {
			s.pacer.SetReadyAt(identity, retryAt)
		}
		for _, blocker := range decision.Blockers() {
			if blocker.Reason() == ratelimit.BlockReasonCooldown ||
				blocker.Reason() == ratelimit.BlockReasonWarmup {
				return ratelimit.Admission{}, &RateLimitDeferred{RetryAt: retryAt}
			}
		}
		if deadline, ok := ctx.Deadline(); ok && !retryAt.Before(deadline) {
			return ratelimit.Admission{}, &RateLimitDeferred{RetryAt: retryAt}
		}
		wait := time.Until(retryAt)
		if wait < 100*time.Millisecond {
			wait = 100 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return ratelimit.Admission{}, &RateLimitDeferred{RetryAt: retryAt}
			}
			return ratelimit.Admission{}, ctx.Err()
		case <-timer.C:
		}
	}
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
	fetchErr error,
	deadlineHit bool,
	outcome WorkerOutcome,
) WorkerOutcome {
	requeueErr := s.requeueTask(ctx, task, lease.Snapshot.CombinationID)
	apply := func(disposition disposition) {
		_, _, err := s.applyDisposition(ctx, target, disposition)
		outcome.Err = errors.Join(outcome.Err, err)
	}
	var signal *RateLimitSignal
	var deferred *RateLimitDeferred
	switch {
	case errors.As(fetchErr, &signal):
		outcome.retryKind = workerRetryRate
		outcome.retryReason = WorkerWaitReasonRateLimit
		if err := s.applyRateLimitFeedback(ctx, signal.Admission, signal.Scopes, signal.Reason, signal.Cooldown); err != nil {
			outcome.Err = errors.Join(outcome.Err, err)
			outcome.RetryAt = s.now().Add(s.config.TransientRetry)
		} else {
			outcome.RetryAt = s.workerRetryAt(target.Platform(), time.Time{})
		}
	case errors.As(fetchErr, &deferred):
		// 冷却属于精确工人桶；任务回队即可，不能停止同目标的其他工人。
		outcome.retryKind = workerRetryRate
		outcome.retryReason = WorkerWaitReasonDeferred
		outcome.RetryAt = s.workerRetryAt(target.Platform(), deferred.RetryAt)
	case errors.Is(fetchErr, ratelimit.ErrPolicyUnavailable):
		apply(blockedDisposition(TargetReasonMissingRatePolicy, time.Time{}))
	case errors.Is(fetchErr, ErrFetchSessionInvalid):
		// 账号失效由适配器落库；任务已回队，本工人本周期停领。
	case deadlineHit, errors.Is(fetchErr, context.DeadlineExceeded):
		// 请求超时按节点与平台退避，但保留独立原因便于诊断。
		outcome.retryKind = workerRetryNode
		outcome.retryReason = WorkerWaitReasonTimeout
		outcome.Err = errors.Join(outcome.Err, fetchErr)
		outcome.RetryAt = s.now().Add(s.config.TransientRetry)
	case errors.Is(fetchErr, ErrFetchNetwork):
		// 网络故障按节点与平台退避，避免同一出口上的其他账号立即重试。
		outcome.retryKind = workerRetryNode
		outcome.retryReason = WorkerWaitReasonNetwork
		outcome.Err = errors.Join(outcome.Err, fetchErr)
		outcome.RetryAt = s.now().Add(s.config.TransientRetry)
	default:
		outcome.retryReason = WorkerWaitReasonTransient
		outcome.Err = errors.Join(outcome.Err, fetchErr)
		outcome.RetryAt = s.now().Add(s.config.TransientRetry)
	}
	outcome.Err = errors.Join(outcome.Err, requeueErr)
	return outcome
}

func (s *Scheduler) workerRetryAt(platform Platform, candidate time.Time) time.Time {
	now := s.now()
	if candidate.After(now) {
		return candidate
	}
	delay := s.config.Profiles[platform].RequestInterval
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}
	return now.Add(delay)
}

func (s *Scheduler) requeueTask(ctx context.Context, task Task, combinationID resource.CombinationID) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), schedulerCleanupTimeout)
	defer cancel()
	return s.store.RequeueTask(cleanupCtx, task.ID(), combinationID, task.ClaimGeneration())
}

func (s *Scheduler) applyRateLimitFeedback(
	ctx context.Context,
	admission ratelimit.Admission,
	scopes []ratelimit.Scope,
	reason ratelimit.ReasonCode,
	cooldown time.Duration,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), schedulerCleanupTimeout)
	defer cancel()
	return s.admitter.ApplyRateLimitFeedback(cleanupCtx, admission, scopes, reason, cooldown)
}

func targetReadyAt(target Target, now time.Time) bool {
	if target.Desired() != DesiredEnabled || target.Recovery() == RecoveryManual {
		return false
	}
	switch target.Actual() {
	case ActualStarting, ActualRunning:
		return true
	case ActualWaiting, ActualBlocked:
		recheckAt, ok := target.RecheckAt()
		return ok && !now.Before(recheckAt)
	default:
		return false
	}
}

type dispositionKind int

const (
	dispositionRunning dispositionKind = iota
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
