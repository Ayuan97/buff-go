package collection

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/market"
	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

func scheduleNow() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

type fakeScheduleStore struct {
	mu               sync.Mutex
	targets          map[TargetID]Target
	tasks            map[TaskID]Task
	pages            map[TargetID]Page
	combinations     []resource.AccountNodeCombination
	resources        map[resource.CombinationID]resource.CombinationResources
	products         []catalog.SteamProduct
	nextTargetID     int64
	nextTaskID       int64
	enqueueSeq       map[TargetID]int64
	failListErr      error
	failTargetsErr   error
	failTargetsCalls int
	targetsCalls     int
	failReleaseErr   error
	failClearErr     error
	conflictTargets  map[TargetID]bool
}

func newFakeScheduleStore() *fakeScheduleStore {
	return &fakeScheduleStore{
		targets:    make(map[TargetID]Target),
		tasks:      make(map[TaskID]Task),
		pages:      make(map[TargetID]Page),
		resources:  make(map[resource.CombinationID]resource.CombinationResources),
		enqueueSeq: make(map[TargetID]int64),
	}
}

func rebuildTarget(target Target, cursor Cursor, writeSeq, total int64) (Target, error) {
	recheck, has := target.RecheckAt()
	var recheckPtr *time.Time
	if has {
		recheckPtr = &recheck
	}
	appID, _ := target.AppID()
	side, _ := target.Side()
	return NewSummaryTarget(SummaryTargetInput{
		ID: target.ID(), Revision: target.Revision(), Platform: target.Platform(),
		AppID: appID, Side: side, Desired: target.Desired(), Actual: target.Actual(),
		SwitchVersion: target.SwitchVersion(), Reason: target.Reason(), Recovery: target.Recovery(),
		RecheckAt: recheckPtr, ChangedAt: target.ChangedAt(), Sort: target.Sort(),
		RefillCursor: cursor, WriteSeq: writeSeq, RefillTotal: total,
	})
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

func (store *fakeScheduleStore) setRefillTotal(t *testing.T, id TargetID, total int64) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	next, err := rebuildTarget(store.targets[id], store.targets[id].RefillCursor(), store.targets[id].WriteSeq(), total)
	if err != nil {
		t.Fatal(err)
	}
	store.targets[id] = next
}

func (store *fakeScheduleStore) disable(t *testing.T, id TargetID) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	target, err := store.targets[id].Disable(scheduleNow())
	if err != nil {
		t.Fatal(err)
	}
	store.targets[id] = target
	for taskID, task := range store.tasks {
		if task.TargetID() == id {
			delete(store.tasks, taskID)
		}
	}
}

func (store *fakeScheduleStore) askStarts() []int {
	store.mu.Lock()
	defer store.mu.Unlock()
	starts := make([]int, 0, len(store.tasks))
	for _, task := range store.tasks {
		if task.Kind() != TaskKindAskPage {
			continue
		}
		page, err := task.AskPage()
		if err != nil {
			continue
		}
		starts = append(starts, page.Start)
	}
	sort.Ints(starts)
	return starts
}

func (store *fakeScheduleStore) queuedCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, task := range store.tasks {
		if task.State() == TaskQueued {
			count++
		}
	}
	return count
}

func (store *fakeScheduleStore) claimedCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, task := range store.tasks {
		if task.State() == TaskClaimed {
			count++
		}
	}
	return count
}

func (store *fakeScheduleStore) Targets(ctx context.Context) ([]Target, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.targetsCalls++
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
	next, err = rebuildTarget(next, target.RefillCursor(), target.WriteSeq(), target.RefillTotal())
	if err != nil {
		return Target{}, err
	}
	store.targets[id] = next
	return next, nil
}

func (store *fakeScheduleStore) QueueDepth(ctx context.Context, id TargetID) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, task := range store.tasks {
		if task.TargetID() == id {
			count++
		}
	}
	return count, nil
}

func (store *fakeScheduleStore) EnqueueTasks(ctx context.Context, id TargetID, expectedSwitch Revision, specs []EnqueueSpec, cursor Cursor, total int64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	target, ok := store.targets[id]
	if !ok {
		return ErrNotFound
	}
	if target.Desired() != DesiredEnabled || target.SwitchVersion() != expectedSwitch {
		return ErrFence
	}
	now := scheduleNow()
	for _, spec := range specs {
		store.nextTaskID++
		store.enqueueSeq[id]++
		task, err := NewTask(TaskInput{
			ID: TaskID(store.nextTaskID), TargetID: id, EnqueueSeq: store.enqueueSeq[id],
			Kind: spec.Kind, Payload: spec.Payload, State: TaskQueued, EnqueuedAt: now,
		})
		if err != nil {
			return err
		}
		store.tasks[task.ID()] = task
	}
	next, err := rebuildTarget(target, cursor, target.WriteSeq(), total)
	if err != nil {
		return err
	}
	store.targets[id] = next
	return nil
}

func (store *fakeScheduleStore) ClearTargetQueue(ctx context.Context, id TargetID) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failClearErr != nil {
		return store.failClearErr
	}
	for taskID, task := range store.tasks {
		if task.TargetID() == id {
			delete(store.tasks, taskID)
		}
	}
	return nil
}

func (store *fakeScheduleStore) ClaimTask(ctx context.Context, combinationID resource.CombinationID, platform Platform) (Task, Target, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, task := range store.tasks {
		if owner, ok := task.ClaimedBy(); ok && task.State() == TaskClaimed && owner == combinationID {
			return Task{}, Target{}, false, nil
		}
	}
	var chosen Task
	found := false
	for _, task := range store.tasks {
		if task.State() != TaskQueued {
			continue
		}
		target, ok := store.targets[task.TargetID()]
		if !ok || target.Desired() != DesiredEnabled || target.Platform() != platform {
			continue
		}
		if !found || task.EnqueuedAt().Before(chosen.EnqueuedAt()) ||
			(task.EnqueuedAt().Equal(chosen.EnqueuedAt()) && task.ID() < chosen.ID()) {
			chosen, found = task, true
		}
	}
	if !found {
		return Task{}, Target{}, false, nil
	}
	now := scheduleNow()
	claimed := combinationID
	next, err := NewTask(TaskInput{
		ID: chosen.ID(), TargetID: chosen.TargetID(), EnqueueSeq: chosen.EnqueueSeq(),
		Kind: chosen.Kind(), Payload: chosen.Payload(), State: TaskClaimed,
		ClaimedBy: &claimed, ClaimedAt: &now, EnqueuedAt: chosen.EnqueuedAt(),
	})
	if err != nil {
		return Task{}, Target{}, false, err
	}
	store.tasks[next.ID()] = next
	return next, store.targets[next.TargetID()], true, nil
}

func (store *fakeScheduleStore) ReleaseStaleClaims(ctx context.Context, olderThan time.Duration) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failReleaseErr != nil {
		return 0, store.failReleaseErr
	}
	cutoff := scheduleNow().Add(-olderThan)
	released := 0
	for id, task := range store.tasks {
		claimedAt, ok := task.ClaimedAt()
		if task.State() != TaskClaimed || !ok || !claimedAt.Before(cutoff) {
			continue
		}
		next, err := NewTask(TaskInput{
			ID: task.ID(), TargetID: task.TargetID(), EnqueueSeq: task.EnqueueSeq(),
			Kind: task.Kind(), Payload: task.Payload(), State: TaskQueued, EnqueuedAt: task.EnqueuedAt(),
		})
		if err != nil {
			return 0, err
		}
		store.tasks[id] = next
		released++
	}
	return released, nil
}

func (store *fakeScheduleStore) ReleaseAllClaims(ctx context.Context) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failReleaseErr != nil {
		return 0, store.failReleaseErr
	}
	released := 0
	for id, task := range store.tasks {
		if task.State() != TaskClaimed {
			continue
		}
		next, err := NewTask(TaskInput{
			ID: task.ID(), TargetID: task.TargetID(), EnqueueSeq: task.EnqueueSeq(),
			Kind: task.Kind(), Payload: task.Payload(), State: TaskQueued, EnqueuedAt: task.EnqueuedAt(),
		})
		if err != nil {
			return 0, err
		}
		store.tasks[id] = next
		released++
	}
	return released, nil
}

func (store *fakeScheduleStore) CompleteTask(ctx context.Context, id TaskID, combinationID resource.CombinationID) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	task, ok := store.tasks[id]
	if !ok {
		return ErrConflict
	}
	claimedBy, claimed := task.ClaimedBy()
	if task.State() != TaskClaimed || !claimed || claimedBy != combinationID {
		return ErrConflict
	}
	delete(store.tasks, id)
	return nil
}

func (store *fakeScheduleStore) RequeueTask(ctx context.Context, id TaskID, combinationID resource.CombinationID) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	task, ok := store.tasks[id]
	if !ok {
		return nil
	}
	claimedBy, claimed := task.ClaimedBy()
	if task.State() != TaskClaimed || !claimed || claimedBy != combinationID {
		return nil
	}
	next, err := NewTask(TaskInput{
		ID: task.ID(), TargetID: task.TargetID(), EnqueueSeq: task.EnqueueSeq(),
		Kind: task.Kind(), Payload: task.Payload(), State: TaskQueued, EnqueuedAt: task.EnqueuedAt(),
	})
	if err != nil {
		return err
	}
	store.tasks[id] = next
	return nil
}

func (store *fakeScheduleStore) seedClaimedTask(t *testing.T, targetID TargetID, combinationID resource.CombinationID, claimedAt time.Time) Task {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	payload, err := EncodeAskPage(0, AskPageSize)
	if err != nil {
		t.Fatal(err)
	}
	store.nextTaskID++
	store.enqueueSeq[targetID]++
	claimed := combinationID
	task, err := NewTask(TaskInput{
		ID: TaskID(store.nextTaskID), TargetID: targetID, EnqueueSeq: store.enqueueSeq[targetID],
		Kind: TaskKindAskPage, Payload: payload, State: TaskClaimed,
		ClaimedBy: &claimed, ClaimedAt: &claimedAt, EnqueuedAt: claimedAt.Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.tasks[task.ID()] = task
	return task
}

func (store *fakeScheduleStore) CommitSummaryPage(ctx context.Context, input SummaryPageCommit) (Page, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	task, ok := store.tasks[input.TaskID]
	if !ok || task.State() != TaskClaimed || task.TargetID() != input.TargetID {
		return Page{}, false, ErrFence
	}
	target, ok := store.targets[input.TargetID]
	if !ok || target.Desired() != DesiredEnabled || target.SwitchVersion() != input.ExpectedSwitch {
		return Page{}, false, ErrFence
	}
	writeSeq := target.WriteSeq() + 1
	page, err := NewPage(PageInput{
		TargetID: input.TargetID, WriteSeq: writeSeq,
		CursorBefore: input.CursorBefore, CursorAfter: input.CursorAfter,
		PayloadDigest: [32]byte{1}, CollectedAt: input.CollectedAt, CommittedAt: input.CollectedAt,
		AccountID: input.AccountID, ExitAddress: input.ExitAddress,
	})
	if err != nil {
		return Page{}, false, err
	}
	total := target.RefillTotal()
	if input.AskTotal > 0 {
		total = input.AskTotal
	}
	next, err := rebuildTarget(target, target.RefillCursor(), writeSeq, total)
	if err != nil {
		return Page{}, false, err
	}
	store.targets[input.TargetID] = next
	store.pages[input.TargetID] = page
	return page, true, nil
}

func (store *fakeScheduleStore) ListCombinationsFor(ctx context.Context, platform Platform) ([]resource.AccountNodeCombination, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failListErr != nil {
		return nil, store.failListErr
	}
	filtered := make([]resource.AccountNodeCombination, 0)
	for _, combination := range store.combinations {
		if combination.Platform == resource.Platform(platform) {
			filtered = append(filtered, combination)
		}
	}
	return filtered, nil
}

func (store *fakeScheduleStore) CombinationResources(ctx context.Context, id resource.CombinationID) (resource.CombinationResources, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	resources, ok := store.resources[id]
	return resources, ok, nil
}

func (store *fakeScheduleStore) ListWorkers(ctx context.Context) ([]WorkerSnapshot, error) {
	return nil, nil
}

func (store *fakeScheduleStore) ListSteamProductsAfter(ctx context.Context, appID int64, after catalog.ProductID, limit int) ([]catalog.SteamProduct, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	out := make([]catalog.SteamProduct, 0)
	for _, product := range store.products {
		if product.AppID != appID || product.ProductID <= after {
			continue
		}
		out = append(out, product)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

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
func (repo *stubCoordinatorRepository) OpenNodeProxyCredentialAt(context.Context, resource.NodeID, int64) ([]byte, error) {
	return nil, errStubUnsupported
}
func (repo *stubCoordinatorRepository) RecordNodeExit(context.Context, resource.NodeID, int64, netip.Addr, time.Time, time.Time) (resource.AccessNode, error) {
	return resource.AccessNode{}, errStubUnsupported
}
func (repo *stubCoordinatorRepository) MarkNodeUnavailable(context.Context, resource.NodeID, int64) (resource.AccessNode, error) {
	return resource.AccessNode{}, errStubUnsupported
}

func combinationResources(id int64, platform resource.Platform, exit netip.Addr) resource.CombinationResources {
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
			State: resource.NodeStateAvailable, EgressRevision: 1,
			ExitVerification: &resource.ExitVerification{
				VerifiedRevision: 1, Address: exit,
				VerifiedAt: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour),
			},
		},
	}
}

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

type fetchRecord struct {
	kind       TaskKind
	payload    string
	leaseToken string
}

type fakeFetcher struct {
	mu          sync.Mutex
	calls       []fetchRecord
	inFlight    int
	maxInFlight int
	hook        func(request PageFetch) error
}

func (fetcher *fakeFetcher) FetchPage(ctx context.Context, request PageFetch) (FetchedPage, error) {
	fetcher.mu.Lock()
	fetcher.inFlight++
	if fetcher.inFlight > fetcher.maxInFlight {
		fetcher.maxInFlight = fetcher.inFlight
	}
	fetcher.calls = append(fetcher.calls, fetchRecord{
		kind: request.Kind, payload: string(request.Payload), leaseToken: request.Lease.Token.String(),
	})
	hook := fetcher.hook
	fetcher.mu.Unlock()
	defer func() {
		fetcher.mu.Lock()
		fetcher.inFlight--
		fetcher.mu.Unlock()
	}()
	if hook != nil {
		if err := hook(request); err != nil {
			return FetchedPage{}, err
		}
	}
	return FetchedPage{Payload: []byte(`{}`), TotalCount: 200}, nil
}

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
	fetcher := &fakeFetcher{}
	config := SchedulerConfig{
		Profiles: map[Platform]PlatformProfile{
			PlatformSteam: {
				TargetRegion:    resource.TargetRegionDomestic,
				SummaryEndpoint: "market_summary",
			},
		},
		PageTimeout:    time.Second,
		TransientRetry: 50 * time.Millisecond,
		ClaimTimeout:   45 * time.Second,
	}
	if configure != nil {
		configure(&config)
	}
	scheduler, err := NewScheduler(store, store, coordinator, admitter, fetcher, config)
	if err != nil {
		t.Fatal(err)
	}
	return &scheduleHarness{
		store: store, repo: repo, coordinator: coordinator,
		admitter: admitter, fetcher: fetcher, scheduler: scheduler,
	}
}

func (harness *scheduleHarness) addCombination(id int64, platform resource.Platform, exit netip.Addr) {
	resources := combinationResources(id, platform, exit)
	harness.repo.setResources(resources)
	harness.store.mu.Lock()
	harness.store.combinations = append(harness.store.combinations, resources.Combination)
	harness.store.resources[resources.Combination.ID] = resources
	harness.store.mu.Unlock()
}

func (harness *scheduleHarness) mutateCombination(id resource.CombinationID, mutate func(*resource.CombinationResources)) {
	harness.store.mu.Lock()
	resources := harness.store.resources[id]
	mutate(&resources)
	harness.store.resources[id] = resources
	harness.store.mu.Unlock()
	harness.repo.setResources(resources)
}

func requireTargetState(t *testing.T, target Target, actual ActualState, reason TargetReason) {
	t.Helper()
	if target.Actual() != actual || target.Reason() != reason {
		t.Fatalf("target state = %s/%s, want %s/%s", target.Actual(), target.Reason(), actual, reason)
	}
}

func TestScheduleRefillsAskQueueToWatermark(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].Enqueued != QueueWatermark {
		t.Fatalf("enqueued = %+v", report.Targets)
	}
	if len(report.Workers) != 1 || !report.Workers[0].Committed {
		t.Fatalf("workers = %+v", report.Workers)
	}
	depth, err := harness.store.QueueDepth(context.Background(), target.ID())
	if err != nil || depth != QueueWatermark-1 {
		t.Fatalf("depth = %d err=%v", depth, err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualRunning, TargetReasonNone)
}

func TestScheduleAskWrapsWithoutDedup(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.setRefillTotal(t, target.ID(), 20)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	zeros := 0
	for _, start := range harness.store.askStarts() {
		if start == 0 {
			zeros++
		}
	}
	if zeros < 2 {
		t.Fatalf("wrapped pages must reappear, starts=%v", harness.store.askStarts())
	}
}

func TestScheduleNoRefillWithoutHealthyWorker(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].Enqueued != 0 {
		t.Fatalf("report = %+v", report.Targets)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonNoCombination)
	depth, err := harness.store.QueueDepth(context.Background(), target.ID())
	if err != nil || depth != 0 {
		t.Fatalf("depth = %d err=%v", depth, err)
	}
}

func TestScheduleReleasesStaleClaims(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		config.ClaimTimeout = 10 * time.Millisecond
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	stale := harness.store.seedClaimedTask(t, target.ID(), 99, scheduleNow().Add(-time.Second))

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Workers) != 1 || report.Workers[0].TaskID != stale.ID() || !report.Workers[0].Committed {
		t.Fatalf("stale task should be reclaimed, workers=%+v", report.Workers)
	}
}

func TestScheduleSessionInvalidRequeuesTask(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.hook = func(PageFetch) error { return ErrFetchSessionInvalid }

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Workers) != 1 || report.Workers[0].Committed {
		t.Fatalf("workers = %+v", report.Workers)
	}
	if harness.store.queuedCount() != QueueWatermark || harness.store.claimedCount() != 0 {
		t.Fatalf("queued=%d claimed=%d", harness.store.queuedCount(), harness.store.claimedCount())
	}
	if harness.store.target(target.ID()).WriteSeq() != 0 {
		t.Fatal("failed fetch must not advance write_seq")
	}
}

func TestScheduleConcurrencyBoundedByWorkers(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.3"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	harness.fetcher.hook = func(PageFetch) error {
		started <- struct{}{}
		<-release
		return nil
	}

	done := make(chan CycleReport, 1)
	go func() {
		report, err := harness.scheduler.RunCycle(context.Background())
		if err != nil {
			t.Error(err)
		}
		done <- report
	}()
	<-started
	<-started
	if harness.fetcher.maxInFlight != 2 {
		t.Fatalf("max in flight = %d", harness.fetcher.maxInFlight)
	}
	close(release)
	report := <-done
	if len(report.Workers) != 2 {
		t.Fatalf("workers = %+v", report.Workers)
	}
}

func TestScheduleRejectsExpiredEgress(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.mutateCombination(1, func(resources *resource.CombinationResources) {
		resources.Node.ExitVerification.ValidUntil = scheduleNow().Add(-time.Minute)
	})
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonNoCombination)
}

func TestScheduleAllowsUnverifiedSession(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.mutateCombination(1, func(resources *resource.CombinationResources) {
		resources.Account.SessionState = resource.AccountSessionStateUnverified
		resources.Account.LastCheckedAt = nil
	})
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Workers) != 1 || !report.Workers[0].Committed {
		t.Fatalf("workers = %+v", report.Workers)
	}
}

func TestScheduleRejectsInvalidSession(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.mutateCombination(1, func(resources *resource.CombinationResources) {
		resources.Account.SessionState = resource.AccountSessionStateInvalid
	})
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonNoCombination)
}

func TestScheduleRateLimitSignalFeedsBack(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.hook = func(PageFetch) error {
		return &RateLimitSignal{Scopes: []ratelimit.Scope{ratelimit.ScopePlatform}, Reason: ratelimit.ReasonHTTP429, Cooldown: time.Minute}
	}
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(harness.admitter.feedbacks) != 1 {
		t.Fatalf("feedbacks = %+v", harness.admitter.feedbacks)
	}
	if harness.store.queuedCount() != QueueWatermark {
		t.Fatalf("queued = %d", harness.store.queuedCount())
	}
}

func TestScheduleMissingProfileBlocksManually(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		config.Profiles = map[Platform]PlatformProfile{}
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonInvalidConfig)
}

func TestScheduleIndependentTargets(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	enabled := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.addSummaryTarget(t, PlatformSteam, 252490, market.SideAsk, DesiredDisabled)
	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].TargetID != enabled.ID() || report.Targets[0].Enqueued != QueueWatermark {
		t.Fatalf("report = %+v", report.Targets)
	}
}

func TestNewSchedulerValidation(t *testing.T) {
	store := newFakeScheduleStore()
	coordinator, err := resource.NewCoordinator(newStubCoordinatorRepository())
	if err != nil {
		t.Fatal(err)
	}
	admitter := &fakeAdmitter{}
	fetcher := &fakeFetcher{}
	valid := SchedulerConfig{
		PageTimeout: time.Second, TransientRetry: time.Second, ClaimTimeout: time.Second,
	}
	if _, err := NewScheduler(nil, store, coordinator, admitter, fetcher, valid); err == nil {
		t.Fatal("nil store must be rejected")
	}
	if _, err := NewScheduler(store, nil, coordinator, admitter, fetcher, valid); err == nil {
		t.Fatal("nil catalog must be rejected")
	}
	if _, err := NewScheduler(store, store, nil, admitter, fetcher, valid); err == nil {
		t.Fatal("nil coordinator must be rejected")
	}
	if _, err := NewScheduler(store, store, coordinator, nil, fetcher, valid); err == nil {
		t.Fatal("nil admitter must be rejected")
	}
	if _, err := NewScheduler(store, store, coordinator, admitter, nil, valid); err == nil {
		t.Fatal("nil fetcher must be rejected")
	}
	broken := valid
	broken.ClaimTimeout = 0
	if _, err := NewScheduler(store, store, coordinator, admitter, fetcher, broken); err == nil {
		t.Fatal("zero durations must be rejected")
	}
	broken = valid
	broken.Profiles = map[Platform]PlatformProfile{"steam": {TargetRegion: "mars"}}
	if _, err := NewScheduler(store, store, coordinator, admitter, fetcher, broken); err == nil {
		t.Fatal("invalid profile region must be rejected")
	}
}
