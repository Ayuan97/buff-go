package collection

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"testing"
	"time"

	"buff-go/internal/market"
	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

func scheduleNow() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

// ---- fake schedule store ----

type fakeScheduleStore struct {
	mu                sync.Mutex
	targets           map[TargetID]Target
	runs              map[RunID]Run
	pages             map[RunID][]Page
	attempts          map[RunID][][]AttemptWrite
	combinations      []resource.AccountNodeCombination
	combinationMeta   map[resource.CombinationID]combinationFilter
	nextTargetID      int64
	nextRunID         int64
	runSequences      map[TargetID]int64
	activeRuns        map[TargetID]RunID
	failListErr       error
	failFinishErr     error
	failTargetsErr    error
	failTargetsCalls  int
	targetsCalls      int
	failActiveRunsErr error
	conflictTargets   map[TargetID]bool
}

type combinationFilter struct {
	platform resource.Platform
	appID    int64
	side     market.Side
}

func newFakeScheduleStore() *fakeScheduleStore {
	return &fakeScheduleStore{
		targets:         make(map[TargetID]Target),
		runs:            make(map[RunID]Run),
		pages:           make(map[RunID][]Page),
		attempts:        make(map[RunID][][]AttemptWrite),
		combinationMeta: make(map[resource.CombinationID]combinationFilter),
		runSequences:    make(map[TargetID]int64),
		activeRuns:      make(map[TargetID]RunID),
	}
}

func (store *fakeScheduleStore) addSummaryTarget(t *testing.T, platform Platform, appID int64, side market.Side, desired DesiredState) Target {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextTargetID++
	actual := ActualStarting
	if desired == DesiredDisabled {
		actual = ActualStopped
	}
	target, err := NewSummaryTarget(SummaryTargetInput{
		ID: TargetID(store.nextTargetID), Revision: 1, Platform: platform, AppID: appID, Side: side,
		Desired: desired, Actual: actual, SwitchVersion: 1, ChangedAt: scheduleNow(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.targets[target.ID()] = target
	return target
}

func (store *fakeScheduleStore) target(id TargetID) Target {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.targets[id]
}

func (store *fakeScheduleStore) run(id RunID) (Run, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	run, ok := store.runs[id]
	return run, ok
}

func (store *fakeScheduleStore) pageCount(id RunID) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.pages[id])
}

// latestRunForTarget 返回某个目标最近创建的运行。
func (store *fakeScheduleStore) latestRunForTarget(id TargetID) (Run, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var latest Run
	found := false
	for _, run := range store.runs {
		targetID, ok := run.TargetID()
		if !ok || targetID != id {
			continue
		}
		if !found || run.ID() > latest.ID() {
			latest, found = run, true
		}
	}
	return latest, found
}

// disable flips desired state through the domain Disable transition.
func (store *fakeScheduleStore) disable(t *testing.T, id TargetID) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	target, err := store.targets[id].Disable(scheduleNow())
	if err != nil {
		t.Fatal(err)
	}
	store.targets[id] = target
}

// markStopped 完成一次已受理的关闭。
func (store *fakeScheduleStore) markStopped(t *testing.T, id TargetID) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	target, err := store.targets[id].MarkStopped(scheduleNow())
	if err != nil {
		t.Fatal(err)
	}
	store.targets[id] = target
}

// enable 重新启用已停止的目标并推进开关版本。
func (store *fakeScheduleStore) enable(t *testing.T, id TargetID) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	target, err := store.targets[id].Enable(scheduleNow())
	if err != nil {
		t.Fatal(err)
	}
	store.targets[id] = target
}

func (store *fakeScheduleStore) Targets(ctx context.Context) ([]Target, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.targetsCalls++
	// failTargetsCalls 只让最初若干次读取失败，用来把单个周期内不同调用方的
	// 失败路径区分开。
	if store.failTargetsErr != nil && (store.failTargetsCalls == 0 || store.targetsCalls <= store.failTargetsCalls) {
		return nil, store.failTargetsErr
	}
	targets := make([]Target, 0, len(store.targets))
	for _, target := range store.targets {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(left, right int) bool { return targets[left].ID() < targets[right].ID() })
	return targets, nil
}

func (store *fakeScheduleStore) TransitionTarget(ctx context.Context, id TargetID, expected, expectedSwitch Revision, transition TargetTransition) (Target, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	target, ok := store.targets[id]
	if !ok {
		return Target{}, ErrNotFound
	}
	// conflictTargets 模拟状态被其他所有者接管：CAS 失败与领域非法转换在存储层
	// 都表现为 ErrConflict。
	if target.Revision() != expected || target.SwitchVersion() != expectedSwitch || store.conflictTargets[id] {
		return Target{}, ErrConflict
	}
	now := scheduleNow()
	var next Target
	var err error
	switch transition.State {
	case ActualRunning:
		next, err = target.MarkRunning(now)
	case ActualWaiting:
		if transition.RecheckAt == nil {
			return Target{}, ErrInvalidInput
		}
		next, err = target.MarkWaiting(transition.Reason, *transition.RecheckAt, now)
	case ActualBlocked:
		next, err = target.MarkBlocked(transition.Reason, transition.RecheckAt, now)
	case ActualStopped:
		next, err = target.MarkStopped(now)
	case ActualError:
		next, err = target.MarkError(transition.Reason, now)
	default:
		return Target{}, ErrInvalidInput
	}
	if err != nil {
		return Target{}, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	store.targets[id] = next
	return next, nil
}

func (store *fakeScheduleStore) CreateSummaryRun(ctx context.Context, targetID TargetID, expectedSwitch Revision, initialCursor Cursor) (Run, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	target, ok := store.targets[targetID]
	if !ok {
		return Run{}, false, ErrNotFound
	}
	if target.Desired() != DesiredEnabled {
		return Run{}, false, ErrTargetDisabled
	}
	if target.SwitchVersion() != expectedSwitch {
		return Run{}, false, ErrConflict
	}
	appID, ok := target.AppID()
	if !ok {
		return Run{}, false, ErrIntegrity
	}
	if existingID, active := store.activeRuns[targetID]; active {
		existing := store.runs[existingID]
		if !existing.State().Terminal() {
			switchVersion, _ := existing.SwitchVersion()
			if switchVersion != expectedSwitch {
				return Run{}, false, ErrConflict
			}
			return existing, false, nil
		}
	}
	store.nextRunID++
	store.runSequences[targetID]++
	now := scheduleNow()
	side, _ := target.Side()
	run, err := NewSummaryRun(SummaryRunInput{
		ID: RunID(store.nextRunID), TargetID: targetID, Platform: target.Platform(), AppID: appID,
		Side: side, SwitchVersion: expectedSwitch, RunSequence: Sequence(store.runSequences[targetID]),
		State: RunPending, CurrentCursor: initialCursor, CreatedAt: now,
	})
	if err != nil {
		return Run{}, false, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	store.runs[run.ID()] = run
	store.activeRuns[targetID] = run.ID()
	return run, true, nil
}

func (store *fakeScheduleStore) BeginRun(ctx context.Context, id RunID) (Run, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	run, ok := store.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	next, err := run.Begin(scheduleNow())
	if err != nil {
		return Run{}, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	store.runs[id] = next
	return next, nil
}

func (store *fakeScheduleStore) FinishRun(ctx context.Context, id RunID, state RunState, completeness Completeness, reason RunReason) (Run, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failFinishErr != nil {
		return Run{}, store.failFinishErr
	}
	run, ok := store.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	now := scheduleNow()
	var next Run
	var err error
	switch state {
	case RunSucceeded:
		next, err = run.Succeed(completeness, now)
	case RunFailed:
		next, err = run.Fail(completeness, reason, now)
	case RunStopped:
		next, err = run.Stop(completeness, reason, now)
	default:
		return Run{}, ErrInvalidInput
	}
	if err != nil {
		return Run{}, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	store.runs[id] = next
	targetID, _ := run.TargetID()
	delete(store.activeRuns, targetID)
	return next, nil
}

func (store *fakeScheduleStore) ActiveRuns(ctx context.Context) ([]Run, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failActiveRunsErr != nil {
		return nil, store.failActiveRunsErr
	}
	runs := make([]Run, 0)
	for _, run := range store.runs {
		if !run.State().Terminal() {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(left, right int) bool { return runs[left].ID() < runs[right].ID() })
	return runs, nil
}

func (store *fakeScheduleStore) CommitSummaryPage(ctx context.Context, input SummaryPageCommit) (Page, bool, error) {
	return store.commitPage(input.RunID, input.PageSequence, input.CursorBefore, input.CursorAfter,
		input.CollectedAt, input.Attempts)
}

func (store *fakeScheduleStore) commitPage(
	runID RunID,
	sequence Sequence,
	before, after Cursor,
	collectedAt time.Time,
	attempts []AttemptWrite,
) (Page, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	run, ok := store.runs[runID]
	if !ok {
		return Page{}, false, ErrNotFound
	}
	if run.TaskType() != TaskTypeSummary {
		return Page{}, false, ErrIntegrity
	}
	targetID, _ := run.TargetID()
	target, ok := store.targets[targetID]
	if !ok {
		return Page{}, false, ErrIntegrity
	}
	switchVersion, _ := run.SwitchVersion()
	if target.Desired() != DesiredEnabled || target.SwitchVersion() != switchVersion || run.State() != RunRunning {
		return Page{}, false, ErrFence
	}
	if int64(sequence) != run.LastPageSequence()+1 || !run.CurrentCursor().Equal(before) {
		return Page{}, false, ErrPageOrder
	}
	// 与真实存储一致：attempt 观测时间不得早于运行开始时间。
	if startedAt, started := run.StartedAt(); started {
		for _, attempt := range attempts {
			if attempt.Observation.CollectedAt.Before(startedAt) {
				return Page{}, false, fmt.Errorf("%w: attempt precedes run start", ErrInvalidInput)
			}
		}
	}
	digest := sha256.New()
	digest.Write(before.Bytes())
	digest.Write(after.Bytes())
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(collectedAt.UnixMicro()))
	digest.Write(encoded[:])
	binary.BigEndian.PutUint64(encoded[:], uint64(len(attempts)))
	digest.Write(encoded[:])
	var digestValue [32]byte
	copy(digestValue[:], digest.Sum(nil))
	page, err := NewPage(PageInput{
		RunID: runID, PageSequence: sequence, CursorBefore: before, CursorAfter: after,
		PayloadDigest: digestValue, CollectedAt: collectedAt, CommittedAt: scheduleNow(),
	})
	if err != nil {
		return Page{}, false, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	next, err := run.CommitPage(page)
	if err != nil {
		return Page{}, false, ErrPageOrder
	}
	store.runs[runID] = next
	store.pages[runID] = append(store.pages[runID], page)
	store.attempts[runID] = append(store.attempts[runID], attempts)
	return page, true, nil
}

func (store *fakeScheduleStore) ListCombinationsFor(ctx context.Context, platform Platform, appID int64, side market.Side) ([]resource.AccountNodeCombination, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failListErr != nil {
		return nil, store.failListErr
	}
	filtered := make([]resource.AccountNodeCombination, 0)
	for _, combination := range store.combinations {
		meta, ok := store.combinationMeta[combination.ID]
		if !ok {
			continue
		}
		if meta.platform == resource.Platform(platform) && meta.appID == appID && meta.side == side {
			filtered = append(filtered, combination)
		}
	}
	return filtered, nil
}

// ---- stub coordinator repository ----

type stubCoordinatorRepository struct {
	mu        sync.Mutex
	resources map[resource.CombinationID]resource.CombinationResources
}

func newStubCoordinatorRepository() *stubCoordinatorRepository {
	return &stubCoordinatorRepository{resources: make(map[resource.CombinationID]resource.CombinationResources)}
}

func (repo *stubCoordinatorRepository) setResources(resources resource.CombinationResources) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	repo.resources[resources.Combination.ID] = resources
}

func (repo *stubCoordinatorRepository) CombinationResources(ctx context.Context, id resource.CombinationID) (resource.CombinationResources, bool, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	resources, ok := repo.resources[id]
	return resources, ok, nil
}

func (repo *stubCoordinatorRepository) Node(ctx context.Context, id resource.NodeID) (resource.AccessNode, bool, error) {
	return resource.AccessNode{}, false, nil
}

var errStubUnsupported = errors.New("stub repository operation is unsupported")

func (repo *stubCoordinatorRepository) CreateCombination(context.Context, resource.AccountID, resource.NodeID) (resource.AccountNodeCombination, error) {
	return resource.AccountNodeCombination{}, errStubUnsupported
}
func (repo *stubCoordinatorRepository) DeleteCombination(context.Context, resource.CombinationID) error {
	return errStubUnsupported
}
func (repo *stubCoordinatorRepository) DeleteAccount(context.Context, resource.AccountID) error {
	return errStubUnsupported
}
func (repo *stubCoordinatorRepository) DeleteNode(context.Context, resource.NodeID) error {
	return errStubUnsupported
}
func (repo *stubCoordinatorRepository) ReplaceAccountSession(context.Context, resource.AccountID, int64, []byte) (resource.PlatformAccount, error) {
	return resource.PlatformAccount{}, errStubUnsupported
}
func (repo *stubCoordinatorRepository) OpenAccountSessionAt(context.Context, resource.AccountID, int64) ([]byte, error) {
	return nil, errStubUnsupported
}
func (repo *stubCoordinatorRepository) RecordAccountSessionCheck(context.Context, resource.AccountID, int64, resource.AccountSessionState, time.Time) (resource.PlatformAccount, error) {
	return resource.PlatformAccount{}, errStubUnsupported
}
func (repo *stubCoordinatorRepository) ReplaceNodeConnection(context.Context, resource.NodeID, int64, resource.NodeConnectionInput) (resource.AccessNode, error) {
	return resource.AccessNode{}, errStubUnsupported
}
func (repo *stubCoordinatorRepository) BeginNodeRevalidation(context.Context, resource.NodeID, int64) (resource.AccessNode, error) {
	return resource.AccessNode{}, errStubUnsupported
}
func (repo *stubCoordinatorRepository) OpenNodeProxyCredentialAt(context.Context, resource.NodeID, int64, int64) ([]byte, error) {
	return nil, errStubUnsupported
}
func (repo *stubCoordinatorRepository) RecordNodeExit(context.Context, resource.NodeID, int64, netip.Addr, time.Time, time.Time) (resource.AccessNode, error) {
	return resource.AccessNode{}, errStubUnsupported
}
func (repo *stubCoordinatorRepository) MarkNodeUnavailable(context.Context, resource.NodeID, int64) (resource.AccessNode, error) {
	return resource.AccessNode{}, errStubUnsupported
}
func (repo *stubCoordinatorRepository) AssignNodeGame(context.Context, resource.NodeID, int64, int64) (resource.AccessNode, error) {
	return resource.AccessNode{}, errStubUnsupported
}
func (repo *stubCoordinatorRepository) AssignNodeSide(context.Context, resource.NodeID, int64, resource.Platform, market.Side) (resource.AccessNode, error) {
	return resource.AccessNode{}, errStubUnsupported
}

func combinationResources(id int64, platform resource.Platform, appID int64, side market.Side, exit netip.Addr) resource.CombinationResources {
	now := scheduleNow()
	checkedAt := now.Add(-time.Minute)
	return resource.CombinationResources{
		Combination: resource.AccountNodeCombination{
			ID: resource.CombinationID(id), Platform: platform,
			AccountID: resource.AccountID(id*10 + 1), NodeID: resource.NodeID(id*10 + 2),
		},
		Account: resource.PlatformAccount{
			ID: resource.AccountID(id*10 + 1), Platform: platform, Alias: fmt.Sprintf("account-%d", id),
			SessionState: resource.AccountSessionStateValid, SessionRevision: 1, LastCheckedAt: &checkedAt,
		},
		Node: resource.AccessNode{
			ID: resource.NodeID(id*10 + 2), Name: fmt.Sprintf("node-%d", id), Kind: resource.NodeKindDirect,
			Region: resource.NodeRegionDomestic, EgressMode: resource.EgressModeStatic,
			State: resource.NodeStateAvailable, EgressRevision: 1, AssignmentRevision: 1,
			AppID: appID,
			Sides: []resource.NodeSideAssignment{{Platform: platform, Side: side}},
			ExitVerification: &resource.ExitVerification{
				VerifiedRevision: 1, Address: exit,
				VerifiedAt: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour),
			},
		},
	}
}

// ---- fake admitter ----

type admitRecord struct {
	platform    resource.Platform
	exitAddress netip.Addr
	endpoint    string
}

type feedbackRecord struct {
	scopes   []ratelimit.Scope
	reason   ratelimit.ReasonCode
	cooldown time.Duration
}

type fakeAdmitter struct {
	mu        sync.Mutex
	signer    *ratelimit.Signer
	requests  []admitRecord
	feedbacks []feedbackRecord
	script    func(index int, request ratelimit.Request) (ratelimit.Decision, error)
}

func newFakeAdmitter(t *testing.T) *fakeAdmitter {
	t.Helper()
	signer, err := ratelimit.NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeAdmitter{signer: signer}
}

func (admitter *fakeAdmitter) allow(request ratelimit.Request) (ratelimit.Decision, error) {
	platformPolicy := ratelimit.Policy{
		ID: 1, Revision: 1, Enabled: true, ReadyAt: scheduleNow().Add(-time.Hour),
		Spec: ratelimit.PolicySpec{
			Platform: request.Platform(), RuleKey: "platform-total", Scope: ratelimit.ScopePlatform,
			Kind: ratelimit.KindMinInterval, MinInterval: time.Microsecond, DefaultCooldown: time.Minute,
		},
	}
	interfacePolicy := ratelimit.Policy{
		ID: 2, Revision: 1, Enabled: true, ReadyAt: scheduleNow().Add(-time.Hour),
		Spec: ratelimit.PolicySpec{
			Platform: request.Platform(), RuleKey: "interface-exact", Scope: ratelimit.ScopeInterface,
			EndpointClass: request.EndpointClass(), Kind: ratelimit.KindCooldownOnly, DefaultCooldown: time.Minute,
		},
	}
	rules := make([]ratelimit.AppliedRule, 0, 2)
	for _, policy := range []ratelimit.Policy{platformPolicy, interfacePolicy} {
		rule, err := ratelimit.AppliedRuleFromPolicy(policy)
		if err != nil {
			return ratelimit.Decision{}, err
		}
		rules = append(rules, rule)
	}
	admission, err := admitter.signer.Issue(request, rules, scheduleNow())
	if err != nil {
		return ratelimit.Decision{}, err
	}
	return admitter.signer.Allow(admission)
}

func (admitter *fakeAdmitter) AdmitRateLimit(ctx context.Context, request ratelimit.Request) (ratelimit.Decision, error) {
	admitter.mu.Lock()
	index := len(admitter.requests)
	admitter.requests = append(admitter.requests, admitRecord{
		platform:    request.Platform(),
		exitAddress: request.ExitAddress(),
		endpoint:    string(request.EndpointClass()),
	})
	script := admitter.script
	admitter.mu.Unlock()
	if script != nil {
		return script(index, request)
	}
	return admitter.allow(request)
}

func (admitter *fakeAdmitter) ApplyRateLimitFeedback(ctx context.Context, admission ratelimit.Admission, scopes []ratelimit.Scope, reason ratelimit.ReasonCode, cooldown time.Duration) error {
	admitter.mu.Lock()
	defer admitter.mu.Unlock()
	admitter.feedbacks = append(admitter.feedbacks, feedbackRecord{scopes: scopes, reason: reason, cooldown: cooldown})
	return nil
}

func (admitter *fakeAdmitter) records() []admitRecord {
	admitter.mu.Lock()
	defer admitter.mu.Unlock()
	return append([]admitRecord(nil), admitter.requests...)
}

func blockedDecision(t *testing.T, retryAt time.Time) ratelimit.Decision {
	t.Helper()
	blocker, err := ratelimit.NewBlocker(1, ratelimit.ScopePlatform, ratelimit.BlockReasonCooldown, retryAt)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := ratelimit.Block([]ratelimit.Blocker{blocker})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

// ---- fake fetcher ----

type fetchRecord struct {
	taskType     TaskType
	appID        int64
	pageSequence Sequence
	cursor       string
	leaseToken   string
}

type fakeFetcher struct {
	mu            sync.Mutex
	pagesPerRun   int
	calls         []fetchRecord
	inFlight      int
	maxInFlight   int
	hook          func(request PageFetch) error
	attempts      func(request PageFetch) []AttemptWrite
	fetchDuration time.Duration
}

func (fetcher *fakeFetcher) FetchPage(ctx context.Context, request PageFetch) (FetchedPage, error) {
	fetcher.mu.Lock()
	fetcher.inFlight++
	if fetcher.inFlight > fetcher.maxInFlight {
		fetcher.maxInFlight = fetcher.inFlight
	}
	fetcher.calls = append(fetcher.calls, fetchRecord{
		taskType:     request.TaskType,
		appID:        request.AppID,
		pageSequence: request.PageSequence,
		cursor:       string(request.Cursor.Bytes()),
		leaseToken:   request.Lease.Token.String(),
	})
	hook := fetcher.hook
	duration := fetcher.fetchDuration
	fetcher.mu.Unlock()
	defer func() {
		fetcher.mu.Lock()
		fetcher.inFlight--
		fetcher.mu.Unlock()
	}()
	if duration > 0 {
		select {
		case <-ctx.Done():
			return FetchedPage{}, ctx.Err()
		case <-time.After(duration):
		}
	}
	if hook != nil {
		if err := hook(request); err != nil {
			return FetchedPage{}, err
		}
	}
	cursorAfter, err := NewCursor([]byte(fmt.Sprintf("app%d-p%d", request.AppID, request.PageSequence)))
	if err != nil {
		return FetchedPage{}, err
	}
	pages := fetcher.pagesPerRun
	if pages < 1 {
		pages = 1
	}
	fetched := FetchedPage{CursorAfter: cursorAfter, Final: int(request.PageSequence) >= pages}
	if fetcher.attempts != nil {
		fetched.Attempts = fetcher.attempts(request)
	}
	return fetched, nil
}

func (fetcher *fakeFetcher) recordedCalls() []fetchRecord {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	return append([]fetchRecord(nil), fetcher.calls...)
}

// ---- harness ----

type scheduleHarness struct {
	store       *fakeScheduleStore
	repo        *stubCoordinatorRepository
	coordinator *resource.Coordinator
	admitter    *fakeAdmitter
	fetcher     *fakeFetcher
	scheduler   *Scheduler
}

func newScheduleHarness(t *testing.T, configure func(config *SchedulerConfig)) *scheduleHarness {
	t.Helper()
	store := newFakeScheduleStore()
	repo := newStubCoordinatorRepository()
	coordinator, err := resource.NewCoordinator(repo)
	if err != nil {
		t.Fatal(err)
	}
	admitter := newFakeAdmitter(t)
	fetcher := &fakeFetcher{pagesPerRun: 1}
	config := SchedulerConfig{
		Profiles: map[Platform]PlatformProfile{
			PlatformSteam: {
				TargetRegion:    resource.TargetRegionDomestic,
				SummaryEndpoint: "market_summary",
			},
		},
		PageTimeout:     time.Second,
		ResourceWait:    2 * time.Second,
		PollInterval:    2 * time.Millisecond,
		TransientRetry:  50 * time.Millisecond,
		SummaryPeriod:   time.Hour,
		MaxParallelRuns: 4,
	}
	if configure != nil {
		configure(&config)
	}
	scheduler, err := NewScheduler(store, coordinator, admitter, fetcher, config)
	if err != nil {
		t.Fatal(err)
	}
	return &scheduleHarness{
		store: store, repo: repo, coordinator: coordinator,
		admitter: admitter, fetcher: fetcher, scheduler: scheduler,
	}
}

func (harness *scheduleHarness) addCombination(id int64, platform resource.Platform, appID int64, side market.Side, exit netip.Addr) {
	resources := combinationResources(id, platform, appID, side, exit)
	harness.repo.setResources(resources)
	harness.store.mu.Lock()
	harness.store.combinations = append(harness.store.combinations, resources.Combination)
	harness.store.combinationMeta[resources.Combination.ID] = combinationFilter{platform: platform, appID: appID, side: side}
	harness.store.mu.Unlock()
}

func requireTargetState(t *testing.T, target Target, actual ActualState, reason TargetReason) {
	t.Helper()
	if target.Actual() != actual || target.Reason() != reason {
		t.Fatalf("target state = %s/%s, want %s/%s", target.Actual(), target.Reason(), actual, reason)
	}
}

// ---- tests ----

// 单摘要目标完整成功：运行、逐页提交、目标回到 waiting(next_cycle)。
func TestScheduleSummaryRunSucceeds(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.pagesPerRun = 3

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || !report.Targets[0].Dispatched {
		t.Fatalf("unexpected report %+v", report.Targets)
	}
	outcome := report.Targets[0].Runs[0]
	if outcome.State != RunSucceeded || outcome.Completeness != CompletenessComplete || outcome.PagesCommitted != 3 {
		t.Fatalf("unexpected run outcome %+v", outcome)
	}
	if harness.store.pageCount(outcome.RunID) != 3 {
		t.Fatalf("pages = %d, want 3", harness.store.pageCount(outcome.RunID))
	}
	final := harness.store.target(target.ID())
	requireTargetState(t, final, ActualWaiting, TargetReasonNextCycle)
	recheck, _ := final.RecheckAt()
	if recheck.Sub(final.ChangedAt()) < 59*time.Minute {
		t.Fatalf("summary recheck should honor SummaryPeriod, got %s", recheck.Sub(final.ChangedAt()))
	}
	// 游标流串行且衔接。
	calls := harness.fetcher.recordedCalls()
	if len(calls) != 3 || calls[0].cursor != "" || calls[1].cursor != "app730-p1" || calls[2].cursor != "app730-p2" {
		t.Fatalf("unexpected cursor chain %+v", calls)
	}
}

// 两个 appid 的摘要互不影响：禁用其中一个不影响另一个。
func TestScheduleSummaryAppIDsAreIndependent(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", 252490, market.SideAsk, netip.MustParseAddr("2.2.2.3"))
	enabled := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.addSummaryTarget(t, PlatformSteam, 252490, market.SideAsk, DesiredDisabled)

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].TargetID != enabled.ID() {
		t.Fatalf("only the enabled appid should be dispatched, got %+v", report.Targets)
	}
	if len(report.Targets[0].Runs) != 1 || report.Targets[0].Runs[0].AppID != 730 {
		t.Fatalf("enabled summary should dispatch its own appid, got %+v", report.Targets[0].Runs)
	}
	requireTargetState(t, harness.store.target(enabled.ID()), ActualWaiting, TargetReasonNextCycle)
}

// 等待资源与逐页释放：单个摘要目标多页串行占用同一组合。
func TestSchedulePageLevelOccupancyAndRelease(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	summary := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.pagesPerRun = 2
	harness.fetcher.fetchDuration = 5 * time.Millisecond

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || len(report.Targets[0].Runs) != 1 {
		t.Fatalf("unexpected report %+v", report.Targets)
	}
	run := report.Targets[0].Runs[0]
	if run.State != RunSucceeded || run.PagesCommitted != 2 {
		t.Fatalf("run %+v should complete two pages", run)
	}
	if harness.fetcher.maxInFlight != 1 {
		t.Fatalf("single combination must serialize page requests, max in flight = %d", harness.fetcher.maxInFlight)
	}
	calls := harness.fetcher.recordedCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 page fetches, got %d", len(calls))
	}
	tokens := map[string]bool{}
	for _, call := range calls {
		tokens[call.leaseToken] = true
	}
	if len(tokens) != 2 {
		t.Fatalf("each page must acquire a fresh lease, distinct tokens = %d", len(tokens))
	}
	requireTargetState(t, harness.store.target(summary.ID()), ActualWaiting, TargetReasonNextCycle)
}

// 并发容量上限：ask 与 bid 各一条组合，同时在途请求不超过 2。
func TestScheduleConcurrencyBoundedByCombinations(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", 730, market.SideBid, netip.MustParseAddr("2.2.2.3"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideBid, DesiredEnabled)
	harness.fetcher.fetchDuration = 10 * time.Millisecond

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 2 {
		t.Fatalf("both directions should dispatch, got %+v", report.Targets)
	}
	for _, outcome := range report.Targets {
		if outcome.Runs[0].State != RunSucceeded {
			t.Fatalf("run %+v should succeed after waiting for capacity", outcome.Runs[0])
		}
	}
	if harness.fetcher.maxInFlight > 2 {
		t.Fatalf("in-flight requests exceeded combination capacity: %d", harness.fetcher.maxInFlight)
	}
	if len(harness.fetcher.recordedCalls()) != 2 {
		t.Fatalf("both directions should fetch")
	}
}

// 没有任何组合：目标 blocked(no_combination)，不创建运行。
func TestScheduleBlockedWithoutCombination(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	final := harness.store.target(target.ID())
	requireTargetState(t, final, ActualBlocked, TargetReasonNoCombination)
	if final.Recovery() != RecoveryAutomatic {
		t.Fatalf("no_combination must recover automatically")
	}
	outcome := report.Targets[0].Runs[0]
	if !outcome.LeftActive {
		t.Fatalf("run should stay active for continuation, got %+v", outcome)
	}
}

// 出口验证过期：调度前拒绝该节点，目标 blocked(egress_unavailable)。
func TestScheduleRejectsExpiredEgress(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	resources := combinationResources(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	resources.Node.ExitVerification.ValidUntil = scheduleNow().Add(-time.Minute)
	harness.repo.setResources(resources)
	harness.store.mu.Lock()
	harness.store.combinations = append(harness.store.combinations, resources.Combination)
	harness.store.combinationMeta[resources.Combination.ID] = combinationFilter{
		platform: resources.Combination.Platform, appID: 730, side: market.SideAsk,
	}
	harness.store.mu.Unlock()
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonEgressUnavailable)
	if len(harness.fetcher.recordedCalls()) != 0 {
		t.Fatalf("expired egress must not send requests")
	}
}

// 正在重新验证的节点同样被拒绝。
func TestScheduleRejectsValidatingNode(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	resources := combinationResources(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	resources.Node.State = resource.NodeStateValidating
	resources.Node.ExitVerification = nil
	harness.repo.setResources(resources)
	harness.store.mu.Lock()
	harness.store.combinations = append(harness.store.combinations, resources.Combination)
	harness.store.combinationMeta[resources.Combination.ID] = combinationFilter{
		platform: resources.Combination.Platform, appID: 730, side: market.SideAsk,
	}
	harness.store.mu.Unlock()
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonEgressUnavailable)
	if len(harness.fetcher.recordedCalls()) != 0 {
		t.Fatalf("validating node must not send requests")
	}
}

func TestScheduleAllowsUnverifiedSession(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	resources, found, err := harness.repo.CombinationResources(context.Background(), 1)
	if err != nil || !found {
		t.Fatalf("combination resources found=%v err=%v", found, err)
	}
	resources.Account.SessionState = resource.AccountSessionStateUnverified
	resources.Account.LastCheckedAt = nil
	harness.repo.setResources(resources)
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(harness.fetcher.recordedCalls()) == 0 {
		t.Fatal("unverified session must still dispatch")
	}
}

func TestScheduleRejectsInvalidSession(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	resources, found, err := harness.repo.CombinationResources(context.Background(), 1)
	if err != nil || !found {
		t.Fatalf("combination resources found=%v err=%v", found, err)
	}
	resources.Account.SessionState = resource.AccountSessionStateInvalid
	harness.repo.setResources(resources)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonSessionInvalid)
	if harness.store.target(target.ID()).Recovery() != RecoveryManual {
		t.Fatal("session_invalid must recover manually")
	}
	if len(harness.fetcher.recordedCalls()) != 0 {
		t.Fatal("invalid session must not send requests")
	}
}

// 出口变化后只使用新 IP 限频键。
func TestScheduleUsesNewExitAddressAfterChange(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	oldExit := netip.MustParseAddr("2.2.2.2")
	newExit := netip.MustParseAddr("3.3.3.3")
	harness.addCombination(1, "steam", 730, market.SideAsk, oldExit)
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.pagesPerRun = 3

	var once sync.Once
	harness.fetcher.hook = func(request PageFetch) error {
		if request.PageSequence == 1 {
			once.Do(func() {
				// 页面一处理完成后节点重新验证出新出口。
				updated := combinationResources(1, "steam", 730, market.SideAsk, newExit)
				updated.Node.EgressRevision = 2
				updated.Node.ExitVerification.VerifiedRevision = 2
				harness.repo.setResources(updated)
			})
		}
		return nil
	}

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	records := harness.admitter.records()
	if len(records) != 3 {
		t.Fatalf("expected 3 admissions, got %d", len(records))
	}
	if records[0].exitAddress != oldExit {
		t.Fatalf("first admission should use the old exit, got %s", records[0].exitAddress)
	}
	for _, record := range records[1:] {
		if record.exitAddress != newExit {
			t.Fatalf("post-change admission still used old exit: %+v", records)
		}
	}
}

// 冷却：准入拒绝时运行保留续点，目标 blocked(cooldown) 且复查时间来自决定。
func TestScheduleCooldownKeepsRunForContinuation(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.pagesPerRun = 2

	retryAt := scheduleNow().Add(250 * time.Millisecond)
	harness.admitter.script = func(index int, request ratelimit.Request) (ratelimit.Decision, error) {
		if index == 1 {
			return blockedDecision(t, retryAt), nil
		}
		return harness.admitter.allow(request)
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome := report.Targets[0].Runs[0]
	if !outcome.LeftActive || outcome.PagesCommitted != 1 {
		t.Fatalf("cooldown must keep the run active with committed pages, got %+v", outcome)
	}
	final := harness.store.target(target.ID())
	requireTargetState(t, final, ActualBlocked, TargetReasonCooldown)
	recheck, _ := final.RecheckAt()
	if !recheck.Equal(retryAt) {
		t.Fatalf("recheck %s should equal decision retry %s", recheck, retryAt)
	}

	// 冷却到期后的下一周期从续点继续同一运行。
	time.Sleep(300 * time.Millisecond)
	harness.admitter.script = nil
	report, err = harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resumed := report.Targets[0].Runs[0]
	if resumed.RunID != outcome.RunID || resumed.State != RunSucceeded || resumed.PagesCommitted != 1 {
		t.Fatalf("cycle two should resume the same run, got %+v", resumed)
	}
	if harness.store.pageCount(resumed.RunID) != 2 {
		t.Fatalf("resumed run should contain both pages")
	}
	calls := harness.fetcher.recordedCalls()
	last := calls[len(calls)-1]
	if last.cursor != "app730-p1" || last.pageSequence != 2 {
		t.Fatalf("resume must continue from the stored cursor, got %+v", last)
	}
}

// 缺少限频策略：fail closed，目标 blocked(missing_rate_policy) 且需人工恢复。
func TestScheduleMissingRatePolicyBlocksManually(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.admitter.script = func(int, ratelimit.Request) (ratelimit.Decision, error) {
		return ratelimit.Decision{}, ratelimit.ErrPolicyUnavailable
	}

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	final := harness.store.target(target.ID())
	requireTargetState(t, final, ActualBlocked, TargetReasonMissingRatePolicy)
	if final.Recovery() != RecoveryManual {
		t.Fatalf("missing policy requires manual recovery")
	}
	if len(harness.fetcher.recordedCalls()) != 0 {
		t.Fatalf("blocked admission must not send requests")
	}
}

// 登录失效：运行 failed(login_invalid)，目标 blocked(session_invalid) 手动恢复，
// 下一周期不会自动再派发。
func TestScheduleSessionInvalidBlocksUntilRecovery(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.hook = func(PageFetch) error { return ErrFetchSessionInvalid }

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome := report.Targets[0].Runs[0]
	if outcome.State != RunFailed || outcome.Reason != RunReasonLoginInvalid || outcome.Completeness != CompletenessPartial {
		t.Fatalf("unexpected run outcome %+v", outcome)
	}
	final := harness.store.target(target.ID())
	requireTargetState(t, final, ActualBlocked, TargetReasonSessionInvalid)
	if final.Recovery() != RecoveryManual {
		t.Fatalf("session invalid requires manual recovery")
	}

	report, err = harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 0 {
		t.Fatalf("manually blocked target must not be redispatched, got %+v", report.Targets)
	}
}

// 超时：运行 failed(timeout)，目标 waiting(transient_failure)，下一周期新建运行。
func TestScheduleTimeoutFailsRunAndRetriesNextCycle(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		config.PageTimeout = 15 * time.Millisecond
		config.TransientRetry = 10 * time.Millisecond
	})
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.fetchDuration = 200 * time.Millisecond

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome := report.Targets[0].Runs[0]
	if outcome.State != RunFailed || outcome.Reason != RunReasonTimeout {
		t.Fatalf("unexpected run outcome %+v", outcome)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualWaiting, TargetReasonTransientFailure)

	// 下一周期在复查时间之后新建运行而不是复用失败运行。
	time.Sleep(20 * time.Millisecond)
	harness.fetcher.mu.Lock()
	harness.fetcher.fetchDuration = 0
	harness.fetcher.mu.Unlock()
	report, err = harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	retried := report.Targets[0].Runs[0]
	if retried.RunID == outcome.RunID || retried.State != RunSucceeded {
		t.Fatalf("transient failure should create a new run next cycle, got %+v", retried)
	}
}

// 网络错误映射为 failed(network_error)。
func TestScheduleNetworkErrorFailsRun(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.hook = func(PageFetch) error { return fmt.Errorf("dial: %w", ErrFetchNetwork) }

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome := report.Targets[0].Runs[0]
	if outcome.State != RunFailed || outcome.Reason != RunReasonNetworkError {
		t.Fatalf("unexpected run outcome %+v", outcome)
	}
}

// 平台限频信号：反馈进入准入器，目标 blocked(cooldown)，运行保留续点。
func TestScheduleRateLimitSignalFeedsBack(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.hook = func(PageFetch) error {
		return &RateLimitSignal{
			Scopes:   []ratelimit.Scope{ratelimit.ScopePlatform},
			Reason:   ratelimit.ReasonHTTP429,
			Cooldown: time.Minute,
		}
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome := report.Targets[0].Runs[0]
	if !outcome.LeftActive {
		t.Fatalf("rate limited run should stay active, got %+v", outcome)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonCooldown)
	harness.admitter.mu.Lock()
	feedbacks := append([]feedbackRecord(nil), harness.admitter.feedbacks...)
	harness.admitter.mu.Unlock()
	if len(feedbacks) != 1 || feedbacks[0].reason != ratelimit.ReasonHTTP429 || feedbacks[0].cooldown != time.Minute {
		t.Fatalf("unexpected feedback %+v", feedbacks)
	}
}

// 页间禁用：提交被 fence，运行 stopped(switch_disabled)，已提交页保留。
func TestScheduleDisableBetweenPagesStopsRun(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.pagesPerRun = 3

	var once sync.Once
	harness.fetcher.hook = func(request PageFetch) error {
		if request.PageSequence == 2 {
			once.Do(func() { harness.store.disable(t, target.ID()) })
		}
		return nil
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome := report.Targets[0].Runs[0]
	if outcome.State != RunStopped || outcome.Reason != RunReasonSwitchDisabled || outcome.Completeness != CompletenessPartial {
		t.Fatalf("unexpected run outcome %+v", outcome)
	}
	if harness.store.pageCount(outcome.RunID) != 1 {
		t.Fatalf("committed pages must be preserved, got %d", harness.store.pageCount(outcome.RunID))
	}
	final := harness.store.target(target.ID())
	if final.Desired() != DesiredDisabled || final.Actual() != ActualStopping {
		t.Fatalf("disable flow owns the target state, got %s/%s", final.Desired(), final.Actual())
	}
}

// 平台档案缺失：blocked(invalid_config) 手动恢复。
func TestScheduleMissingProfileBlocksManually(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(3, "buff", 730, market.SideAsk, netip.MustParseAddr("2.2.2.4"))
	target := harness.store.addSummaryTarget(t, "buff", 730, market.SideAsk, DesiredEnabled)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	final := harness.store.target(target.ID())
	requireTargetState(t, final, ActualBlocked, TargetReasonInvalidConfig)
	if final.Recovery() != RecoveryManual {
		t.Fatalf("invalid config requires manual recovery")
	}
}

// 没有目录任务时，已启用的摘要仍应派发。
func TestScheduleSummaryWithoutGamesWaits(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualWaiting, TargetReasonNextCycle)
	if len(harness.fetcher.recordedCalls()) != 1 {
		t.Fatalf("enabled summary must still dispatch, got %d fetches", len(harness.fetcher.recordedCalls()))
	}
}

// 摘要周期：waiting(next_cycle) 未到期不派发，到期后派发。
func TestScheduleSummaryPeriodGatesNextRun(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		config.SummaryPeriod = 250 * time.Millisecond
	})
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualWaiting, TargetReasonNextCycle)

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 0 {
		t.Fatalf("period not reached, target must not be dispatched")
	}

	time.Sleep(300 * time.Millisecond)
	report, err = harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].Runs[0].State != RunSucceeded {
		t.Fatalf("after the period the summary target must run again, got %+v", report.Targets)
	}
}

// 应用时钟落后于存储时钟时，页面提交仍以运行开始时间为下界成功。
func TestScheduleToleratesLaggingSchedulerClock(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		config.Clock = func() time.Time { return time.Now().Add(-2 * time.Second) }
	})
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome := report.Targets[0].Runs[0]
	if outcome.State != RunSucceeded || outcome.PagesCommitted != 1 {
		t.Fatalf("lagging clock must not fail page commits, got %+v", outcome)
	}
}

// 应用时钟落后时，attempt 观测时间同样被钳制到运行开始时间之后，摘要提交不失败。
func TestScheduleClampsAttemptTimesWithLaggingClock(t *testing.T) {
	laggingNow := func() time.Time { return time.Now().Add(-2 * time.Second) }
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		config.Clock = laggingNow
	})
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	summary := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.attempts = func(PageFetch) []AttemptWrite {
		return []AttemptWrite{{
			ProductID: 1,
			Observation: market.Observation{
				Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: laggingNow(),
			},
		}}
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var outcome RunOutcome
	for _, targetOutcome := range report.Targets {
		if targetOutcome.TargetID == summary.ID() && len(targetOutcome.Runs) > 0 {
			outcome = targetOutcome.Runs[0]
		}
	}
	if outcome.State != RunSucceeded || outcome.PagesCommitted != 1 {
		t.Fatalf("lagging attempt times must not fail summary commits, got %+v", outcome)
	}
	run, ok := harness.store.run(outcome.RunID)
	if !ok {
		t.Fatal("summary run missing")
	}
	startedAt, _ := run.StartedAt()
	harness.store.mu.Lock()
	stored := harness.store.attempts[outcome.RunID]
	harness.store.mu.Unlock()
	if len(stored) != 1 || len(stored[0]) != 1 {
		t.Fatalf("expected one stored attempt, got %+v", stored)
	}
	if stored[0][0].Observation.CollectedAt.Before(startedAt) {
		t.Fatalf("stored attempt time %v precedes run start %v", stored[0][0].Observation.CollectedAt, startedAt)
	}
}

// 目标停留在 running 且存在中途活动运行时，下一周期续点同一运行而不是新建。
func TestScheduleResumesMidflightRunWhenTargetRunning(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	existing, _, err := harness.store.CreateSummaryRun(context.Background(), target.ID(), target.SwitchVersion(), Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if existing, err = harness.store.BeginRun(context.Background(), existing.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.store.TransitionTarget(context.Background(), target.ID(), target.Revision(), target.SwitchVersion(),
		TargetTransition{State: ActualRunning}); err != nil {
		t.Fatal(err)
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome := report.Targets[0].Runs[0]
	if outcome.RunID != existing.ID() {
		t.Fatalf("must resume run %d, got %d", existing.ID(), outcome.RunID)
	}
	if outcome.State != RunSucceeded {
		t.Fatalf("resumed run should succeed, got %+v", outcome)
	}
}

// 目标停留在 running（上一周期处置写回失败或进程中断）时下一周期照常派发。
func TestScheduleResumesTargetStuckInRunning(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	if _, err := harness.store.TransitionTarget(context.Background(), target.ID(), target.Revision(), target.SwitchVersion(),
		TargetTransition{State: ActualRunning}); err != nil {
		t.Fatal(err)
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].Runs[0].State != RunSucceeded {
		t.Fatalf("running target must be re-dispatched, got %+v", report.Targets)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualWaiting, TargetReasonNextCycle)
}

// 周期取消：在途运行以 stopped(cancelled) 结束，页保留。
func TestScheduleCycleCancellationStopsRun(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", 730, market.SideAsk, netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.pagesPerRun = 5

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	harness.fetcher.hook = func(request PageFetch) error {
		if request.PageSequence == 2 {
			once.Do(cancel)
			return context.Canceled
		}
		return nil
	}

	report, err := harness.scheduler.RunCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outcome := report.Targets[0].Runs[0]
	if outcome.State != RunStopped || outcome.Reason != RunReasonCancelled {
		t.Fatalf("cancelled cycle should stop the run, got %+v", outcome)
	}
	if harness.store.pageCount(outcome.RunID) != 1 {
		t.Fatalf("committed pages must survive cancellation")
	}
}

// 无效调度器依赖与配置被拒绝。
func TestNewSchedulerValidation(t *testing.T) {
	store := newFakeScheduleStore()
	coordinator, err := resource.NewCoordinator(newStubCoordinatorRepository())
	if err != nil {
		t.Fatal(err)
	}
	admitter := &fakeAdmitter{}
	fetcher := &fakeFetcher{}
	valid := SchedulerConfig{
		PageTimeout: time.Second, ResourceWait: time.Second, PollInterval: time.Millisecond,
		TransientRetry: time.Second, SummaryPeriod: time.Hour, MaxParallelRuns: 1,
	}
	if _, err := NewScheduler(nil, coordinator, admitter, fetcher, valid); err == nil {
		t.Fatal("nil store must be rejected")
	}
	if _, err := NewScheduler(store, nil, admitter, fetcher, valid); err == nil {
		t.Fatal("nil coordinator must be rejected")
	}
	if _, err := NewScheduler(store, coordinator, nil, fetcher, valid); err == nil {
		t.Fatal("nil admitter must be rejected")
	}
	if _, err := NewScheduler(store, coordinator, admitter, nil, valid); err == nil {
		t.Fatal("nil fetcher must be rejected")
	}
	broken := valid
	broken.PageTimeout = 0
	if _, err := NewScheduler(store, coordinator, admitter, fetcher, broken); err == nil {
		t.Fatal("zero durations must be rejected")
	}
	broken = valid
	broken.MaxParallelRuns = 0
	if _, err := NewScheduler(store, coordinator, admitter, fetcher, broken); err == nil {
		t.Fatal("non-positive parallelism must be rejected")
	}
	broken = valid
	broken.Profiles = map[Platform]PlatformProfile{"steam": {TargetRegion: "mars"}}
	if _, err := NewScheduler(store, coordinator, admitter, fetcher, broken); err == nil {
		t.Fatal("invalid profile region must be rejected")
	}
}
