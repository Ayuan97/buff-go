package collection

import (
	"context"
	"encoding/json"
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
	mu                 sync.Mutex
	targets            map[TargetID]Target
	tasks              map[TaskID]Task
	pages              map[TargetID]Page
	combinations       []resource.AccountNodeCombination
	resources          map[resource.CombinationID]resource.CombinationResources
	resourceListCalls  map[Platform]int
	products           []catalog.SteamProduct
	productListCalls   int
	nextTargetID       int64
	nextTaskID         int64
	enqueueSeq         map[TargetID]int64
	failListErr        error
	failTargetsErr     error
	failTargetsCalls   int
	targetsCalls       int
	failReleaseErr     error
	failClearErr       error
	failTransitionErr  error
	failRequeueErr     error
	requeueHasDeadline bool
	requeueContextErr  error
	conflictTargets    map[TargetID]bool
}

func newFakeScheduleStore() *fakeScheduleStore {
	return &fakeScheduleStore{
		targets:           make(map[TargetID]Target),
		tasks:             make(map[TaskID]Task),
		pages:             make(map[TargetID]Page),
		resources:         make(map[resource.CombinationID]resource.CombinationResources),
		resourceListCalls: make(map[Platform]int),
		enqueueSeq:        make(map[TargetID]int64),
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
	if store.failTransitionErr != nil {
		return Target{}, store.failTransitionErr
	}
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

func (store *fakeScheduleStore) RecoverTarget(_ context.Context, id TargetID, expected, expectedSwitch Revision) (Target, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	target, ok := store.targets[id]
	if !ok {
		return Target{}, ErrNotFound
	}
	if target.Revision() != expected || target.SwitchVersion() != expectedSwitch || store.conflictTargets[id] {
		return Target{}, ErrConflict
	}
	next, err := target.Recover(scheduleNow())
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
	return store.enqueueTasks(id, expectedSwitch, nil, specs, cursor, total)
}

func (store *fakeScheduleStore) EnqueueTasksFenced(ctx context.Context, expected Target, specs []EnqueueSpec, cursor Cursor, total int64) error {
	return store.enqueueTasks(expected.ID(), expected.SwitchVersion(), &expected, specs, cursor, total)
}

func (store *fakeScheduleStore) enqueueTasks(id TargetID, expectedSwitch Revision, expected *Target, specs []EnqueueSpec, cursor Cursor, total int64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	target, ok := store.targets[id]
	if !ok {
		return ErrNotFound
	}
	if target.Desired() != DesiredEnabled || target.SwitchVersion() != expectedSwitch {
		return ErrFence
	}
	if expected != nil {
		side, _ := target.Side()
		if target.RefillTotal() != expected.RefillTotal() ||
			!target.RefillCursor().Equal(expected.RefillCursor()) ||
			(side == market.SideAsk && target.RefillTotal() == 0 && target.WriteSeq() != expected.WriteSeq()) {
			return ErrConflict
		}
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
	return store.claimTask(combinationID, platform, nil)
}

func (store *fakeScheduleStore) ClaimTaskForTargets(ctx context.Context, combinationID resource.CombinationID, platform Platform, eligibleTargets []TargetID) (Task, Target, bool, error) {
	return store.claimTask(combinationID, platform, eligibleTargets)
}

func (store *fakeScheduleStore) claimTask(combinationID resource.CombinationID, platform Platform, eligibleTargets []TargetID) (Task, Target, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	eligible := make(map[TargetID]struct{}, len(eligibleTargets))
	for _, id := range eligibleTargets {
		eligible[id] = struct{}{}
	}
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
		if eligibleTargets != nil {
			if _, found := eligible[task.TargetID()]; !found {
				continue
			}
		}
		target, ok := store.targets[task.TargetID()]
		if !ok || target.Desired() != DesiredEnabled || target.Platform() != platform ||
			(target.Actual() != ActualStarting && target.Actual() != ActualRunning) {
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
		ClaimedBy: &claimed, ClaimedAt: &now, ClaimGeneration: chosen.ClaimGeneration() + 1,
		EnqueuedAt: chosen.EnqueuedAt(),
	})
	if err != nil {
		return Task{}, Target{}, false, err
	}
	store.tasks[next.ID()] = next
	return next, store.targets[next.TargetID()], true, nil
}

func (store *fakeScheduleStore) ReleaseStaleClaims(ctx context.Context, olderThan time.Duration) (int, error) {
	return store.releaseStaleClaims(olderThan, nil)
}

func (store *fakeScheduleStore) ReleaseStaleClaimsExcept(ctx context.Context, olderThan time.Duration, activeTasks []TaskID) (int, error) {
	active := make(map[TaskID]struct{}, len(activeTasks))
	for _, id := range activeTasks {
		active[id] = struct{}{}
	}
	return store.releaseStaleClaims(olderThan, active)
}

func (store *fakeScheduleStore) releaseStaleClaims(olderThan time.Duration, active map[TaskID]struct{}) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failReleaseErr != nil {
		return 0, store.failReleaseErr
	}
	cutoff := scheduleNow().Add(-olderThan)
	released := 0
	for id, task := range store.tasks {
		if _, found := active[id]; found {
			continue
		}
		claimedAt, ok := task.ClaimedAt()
		if task.State() != TaskClaimed || !ok || !claimedAt.Before(cutoff) {
			continue
		}
		next, err := NewTask(TaskInput{
			ID: task.ID(), TargetID: task.TargetID(), EnqueueSeq: task.EnqueueSeq(),
			Kind: task.Kind(), Payload: task.Payload(), State: TaskQueued,
			ClaimGeneration: task.ClaimGeneration(), EnqueuedAt: task.EnqueuedAt(),
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
			Kind: task.Kind(), Payload: task.Payload(), State: TaskQueued,
			ClaimGeneration: task.ClaimGeneration(), EnqueuedAt: task.EnqueuedAt(),
		})
		if err != nil {
			return 0, err
		}
		store.tasks[id] = next
		released++
	}
	return released, nil
}

func (store *fakeScheduleStore) CompleteTask(ctx context.Context, id TaskID, combinationID resource.CombinationID, claimGeneration int64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	task, ok := store.tasks[id]
	if !ok {
		return ErrConflict
	}
	claimedBy, claimed := task.ClaimedBy()
	if task.State() != TaskClaimed || !claimed || claimedBy != combinationID || task.ClaimGeneration() != claimGeneration {
		return ErrConflict
	}
	delete(store.tasks, id)
	return nil
}

func (store *fakeScheduleStore) RequeueTask(ctx context.Context, id TaskID, combinationID resource.CombinationID, claimGeneration int64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, store.requeueHasDeadline = ctx.Deadline()
	store.requeueContextErr = ctx.Err()
	if store.failRequeueErr != nil {
		return store.failRequeueErr
	}
	task, ok := store.tasks[id]
	if !ok {
		return nil
	}
	claimedBy, claimed := task.ClaimedBy()
	if task.State() != TaskClaimed || !claimed || claimedBy != combinationID || task.ClaimGeneration() != claimGeneration {
		return nil
	}
	next, err := NewTask(TaskInput{
		ID: task.ID(), TargetID: task.TargetID(), EnqueueSeq: task.EnqueueSeq(),
		Kind: task.Kind(), Payload: task.Payload(), State: TaskQueued,
		ClaimGeneration: task.ClaimGeneration(), EnqueuedAt: task.EnqueuedAt(),
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
		ClaimedBy: &claimed, ClaimedAt: &claimedAt, ClaimGeneration: 1,
		EnqueuedAt: claimedAt.Add(-time.Second),
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
	owner, claimed := task.ClaimedBy()
	if !claimed || owner != input.CombinationID || task.ClaimGeneration() != input.ClaimGeneration {
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
	cursor := target.RefillCursor()
	side, _ := target.Side()
	if side == market.SideAsk {
		total = input.AskTotal
		if total == 0 {
			cursor, err = EncodeAskRefill(0)
			if err != nil {
				return Page{}, false, err
			}
			for taskID, queued := range store.tasks {
				if queued.TargetID() == input.TargetID && taskID != input.TaskID && queued.State() == TaskQueued {
					delete(store.tasks, taskID)
				}
			}
		}
	}
	next, err := rebuildTarget(target, cursor, writeSeq, total)
	if err != nil {
		return Page{}, false, err
	}
	store.targets[input.TargetID] = next
	store.pages[input.TargetID] = page
	return page, true, nil
}

func (store *fakeScheduleStore) ListCombinationResourcesFor(ctx context.Context, platform Platform) ([]resource.CombinationResources, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.resourceListCalls[platform]++
	if store.failListErr != nil {
		return nil, store.failListErr
	}
	filtered := make([]resource.CombinationResources, 0)
	for _, combination := range store.combinations {
		if combination.Platform != resource.Platform(platform) {
			continue
		}
		if resources, found := store.resources[combination.ID]; found {
			filtered = append(filtered, resources)
		}
	}
	return filtered, nil
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
	store.productListCalls++
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
func (repo *stubCoordinatorRepository) ConfirmNodeExit(context.Context, resource.NodeID, int64, netip.Addr, time.Time, time.Time) (resource.AccessNode, error) {
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

type pacingRecord struct {
	lane    requestLane
	workers int
}

type fakeRequestPacer struct {
	mu      sync.Mutex
	records []pacingRecord
}

func (pacer *fakeRequestPacer) Wait(
	_ context.Context,
	lane requestLane,
	_ workerRateKey,
	_ time.Duration,
	workers int,
) error {
	pacer.mu.Lock()
	defer pacer.mu.Unlock()
	pacer.records = append(pacer.records, pacingRecord{lane: lane, workers: workers})
	return nil
}

func (pacer *fakeRequestPacer) SetReadyAt(workerRateKey, time.Time) {}

type fakeAdmitter struct {
	mu                  sync.Mutex
	signer              *ratelimit.Signer
	requests            []admitRecord
	feedbacks           []feedbackRecord
	feedbackHasDeadline bool
	feedbackContextErr  error
	script              func(index int, request ratelimit.Request) (ratelimit.Decision, error)
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
	accountIPPolicy := ratelimit.Policy{
		ID: 1, Revision: 1, Enabled: true, ReadyAt: scheduleNow().Add(-time.Hour),
		Spec: ratelimit.PolicySpec{
			Platform: request.Platform(), RuleKey: "endpoint-account-exit", Scope: ratelimit.ScopeAccountIP,
			EndpointClass: request.EndpointClass(), Kind: ratelimit.KindMinInterval,
			MinInterval: time.Microsecond, DefaultCooldown: time.Minute,
		},
	}
	rule, err := ratelimit.AppliedRuleFromPolicy(accountIPPolicy)
	if err != nil {
		return ratelimit.Decision{}, err
	}
	admission, err := admitter.signer.Issue(request, []ratelimit.AppliedRule{rule}, scheduleNow())
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
	_, admitter.feedbackHasDeadline = ctx.Deadline()
	admitter.feedbackContextErr = ctx.Err()
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
	result      *FetchedPage
}

func (fetcher *fakeFetcher) FetchPage(ctx context.Context, request PageFetch) (FetchedPage, error) {
	admission, err := request.AdmitRequest(ctx)
	if err != nil {
		return FetchedPage{}, err
	}
	request.RequestStarted()
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
			var signal *RateLimitSignal
			if errors.As(err, &signal) {
				signal.Admission = admission
			}
			return FetchedPage{}, err
		}
	}
	if fetcher.result != nil {
		return *fetcher.result, nil
	}
	total := int64(200)
	if request.Side == market.SideBid {
		total = 0
	}
	return FetchedPage{Payload: []byte(`{}`), TotalCount: total}, nil
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

func TestScheduleCommitsFetcherObservationTime(t *testing.T) {
	now := scheduleNow()
	observedAt := now.Add(-time.Minute)
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		config.Clock = func() time.Time { return now }
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.result = &FetchedPage{
		Payload: []byte(`{}`), TotalCount: 1, CollectedAt: observedAt,
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Workers) != 1 || !report.Workers[0].Committed {
		t.Fatalf("workers = %+v", report.Workers)
	}
	harness.store.mu.Lock()
	page := harness.store.pages[target.ID()]
	harness.store.mu.Unlock()
	if !page.CollectedAt().Equal(observedAt) {
		t.Fatalf("committed collected_at=%v, want fetch observation time %v", page.CollectedAt(), observedAt)
	}
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

func TestScheduleBidSmallCatalogFillsRequestedDepth(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	for id := int64(1); id <= 20; id++ {
		harness.addCombination(id, "steam", netip.AddrFrom4([4]byte{2, 2, 2, byte(id)}))
	}
	harness.store.mu.Lock()
	harness.store.products = []catalog.SteamProduct{{ProductID: 1, AppID: 730, Name: "AK-47 | Redline"}}
	harness.store.mu.Unlock()
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideBid, DesiredEnabled)
	payload, err := EncodeBidBatch(0, BidBatchSize)
	if err != nil {
		t.Fatal(err)
	}
	queued := make([]EnqueueSpec, QueueWatermark-20)
	for index := range queued {
		queued[index] = EnqueueSpec{Kind: TaskKindBidBatch, Payload: payload}
	}
	if err := harness.store.EnqueueTasks(t.Context(), target.ID(), target.SwitchVersion(), queued, Cursor{}, 0); err != nil {
		t.Fatal(err)
	}

	report, _, _, err := harness.scheduler.planCycle(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].Enqueued != 20 {
		t.Fatalf("planned targets=%+v", report.Targets)
	}
	if depth, err := harness.store.QueueDepth(t.Context(), target.ID()); err != nil || depth != QueueWatermark {
		t.Fatalf("queue depth=%d err=%v", depth, err)
	}
	harness.store.mu.Lock()
	calls := harness.store.productListCalls
	harness.store.mu.Unlock()
	if calls != 1 {
		t.Fatalf("catalog batch reads=%d, want 1", calls)
	}
}

func TestScheduleAskZeroTotalKeepsProbingFirstPage(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.result = &FetchedPage{Payload: []byte(`{"success":true,"total_count":0}`), TotalCount: 0}

	for range 2 {
		if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
			t.Fatal(err)
		}
		if depth, err := harness.store.QueueDepth(context.Background(), target.ID()); err != nil || depth != 0 {
			t.Fatalf("zero-result queue depth = %d err=%v", depth, err)
		}
	}
	if len(harness.fetcher.calls) != 2 {
		t.Fatalf("fetch calls = %d", len(harness.fetcher.calls))
	}
	for _, call := range harness.fetcher.calls {
		var page AskPagePayload
		err := json.Unmarshal([]byte(call.payload), &page)
		if err != nil || page.Start != 0 {
			t.Fatalf("zero-result probe = %+v err=%v", page, err)
		}
	}
	stored := harness.store.target(target.ID())
	if stored.RefillTotal() != 0 || stored.WriteSeq() != 2 {
		t.Fatalf("zero-result target total=%d write_seq=%d", stored.RefillTotal(), stored.WriteSeq())
	}
}

func TestScheduleAskZeroTotalDoesNotDuplicatePendingProbe(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	cursor, err := EncodeAskRefill(0)
	if err != nil {
		t.Fatal(err)
	}
	harness.store.mu.Lock()
	harness.store.targets[target.ID()], err = rebuildTarget(target, cursor, 1, 0)
	harness.store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	harness.store.seedClaimedTask(t, target.ID(), 1, scheduleNow())

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].Enqueued != 0 {
		t.Fatalf("targets = %+v", report.Targets)
	}
	if depth, err := harness.store.QueueDepth(context.Background(), target.ID()); err != nil || depth != 1 {
		t.Fatalf("zero-result probe depth=%d err=%v", depth, err)
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
	if report.Workers[0].retryKind == workerRetryNode || !report.Workers[0].RetryAt.IsZero() {
		t.Fatalf("session failure entered node backoff: %+v", report.Workers[0])
	}
	if harness.store.queuedCount() != QueueWatermark || harness.store.claimedCount() != 0 {
		t.Fatalf("queued=%d claimed=%d", harness.store.queuedCount(), harness.store.claimedCount())
	}
	if harness.store.target(target.ID()).WriteSeq() != 0 {
		t.Fatal("failed fetch must not advance write_seq")
	}
}

func TestScheduleNetworkFailureDoesNotPauseTarget(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.3"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.hook = func(request PageFetch) error {
		if request.Lease.Snapshot.CombinationID == 1 {
			return ErrFetchNetwork
		}
		return nil
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	committed := 0
	for _, worker := range report.Workers {
		if worker.Committed {
			committed++
		} else if worker.retryReason != WorkerWaitReasonNetwork {
			t.Fatalf("network retry reason = %q", worker.retryReason)
		}
	}
	if committed != 1 {
		t.Fatalf("workers=%+v", report.Workers)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualRunning, TargetReasonNone)
	requests := len(harness.admitter.requests)
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(harness.admitter.requests) <= requests {
		t.Fatalf("running target did not continue: admissions=%d fetches=%d", len(harness.admitter.requests), len(harness.fetcher.calls))
	}
}

func TestScheduleTimeoutHasDistinctNodeRetryReason(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.hook = func(PageFetch) error { return context.DeadlineExceeded }

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Workers) != 1 || report.Workers[0].retryKind != workerRetryNode ||
		report.Workers[0].retryReason != WorkerWaitReasonTimeout ||
		report.Workers[0].BlockOrErr != BlockTimeout {
		t.Fatalf("timeout worker = %+v", report.Workers)
	}
}

func TestScheduleCollectErrorCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code BlockOrErr
	}{
		{name: "http 5xx", err: ErrFetchHTTP5xx, code: BlockHTTP5xx},
		{name: "parse", err: ErrFetchParse, code: BlockParseFail},
		{name: "auth", err: ErrFetchAuth, code: BlockAuthFail},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newScheduleHarness(t, nil)
			harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
			harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
			harness.fetcher.hook = func(PageFetch) error { return test.err }
			report, err := harness.scheduler.RunCycle(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Workers) != 1 || report.Workers[0].BlockOrErr != test.code {
				t.Fatalf("workers=%+v want %q", report.Workers, test.code)
			}
		})
	}
}

func TestScheduleReportsLeaseHeld(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.3"))
	harness.store.mu.Lock()
	first := harness.store.resources[1]
	harness.store.mu.Unlock()
	harness.mutateCombination(2, func(resources *resource.CombinationResources) {
		resources.Combination.NodeID = first.Node.ID
		resources.Node = first.Node
	})
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, worker := range report.Workers {
		if worker.BlockOrErr == BlockLeaseHeld {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("lease_held workers=%+v", report.Workers)
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

func TestSmoothRequestPacerSpreadsAndIsolatesLanes(t *testing.T) {
	pacer := newSmoothRequestPacer()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	ask := requestLane{platform: PlatformSteam, endpoint: "market_summary"}
	want := []time.Time{now, now.Add(100 * time.Millisecond), now.Add(200 * time.Millisecond)}
	for index := range want {
		got := pacer.reserve(ask, now, 300*time.Millisecond, 3)
		if !got.Equal(want[index]) {
			t.Fatalf("ask slot[%d]=%s want=%s", index, got, want[index])
		}
	}
	for _, lane := range []requestLane{
		{platform: PlatformSteam, endpoint: "market_orderbook"},
		{platform: "buff", endpoint: "market_summary"},
	} {
		got := pacer.reserve(lane, now, 300*time.Millisecond, 3)
		if !got.Equal(now) {
			t.Fatalf("independent lane slot=%s want=%s", got, now)
		}
	}
}

func TestSmoothRequestPacerDoesNotReserveLaneBeforeIdentityIsReady(t *testing.T) {
	pacer := newSmoothRequestPacer()
	lane := requestLane{platform: PlatformSteam, endpoint: "market_summary"}
	blocked := workerRateKey{
		accountID: 1, exitAddress: netip.MustParseAddr("2.2.2.1"), lane: lane,
	}
	ready := workerRateKey{
		accountID: 2, exitAddress: netip.MustParseAddr("2.2.2.2"), lane: lane,
	}
	pacer.SetReadyAt(blocked, time.Now().Add(time.Hour))
	blockedCtx, cancelBlocked := context.WithCancel(context.Background())
	blockedDone := make(chan error, 1)
	go func() {
		blockedDone <- pacer.Wait(blockedCtx, lane, blocked, 100*time.Millisecond, 2)
	}()
	select {
	case err := <-blockedDone:
		t.Fatalf("blocked identity returned early: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelReady()
	if err := pacer.Wait(readyCtx, lane, ready, 100*time.Millisecond, 2); err != nil {
		t.Fatalf("ready identity could not fill the open lane slot: %v", err)
	}
	cancelBlocked()
	if err := <-blockedDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked identity error=%v", err)
	}
}

func TestMaximumIndependentCombinationsUsesFullMatching(t *testing.T) {
	accountX := combinationResources(1, "steam", netip.MustParseAddr("2.2.2.1"))
	accountY := combinationResources(2, "steam", netip.MustParseAddr("2.2.2.2"))
	accountY.Account = accountX.Account
	accountY.Combination.AccountID = accountX.Account.ID
	otherX := combinationResources(3, "steam", netip.MustParseAddr("2.2.2.3"))
	otherX.Node = accountX.Node
	otherX.Combination.NodeID = accountX.Node.ID

	selected := maximumIndependentCombinations([]resource.CombinationResources{accountX, accountY, otherX})
	if len(selected) != 2 {
		t.Fatalf("selected=%+v", selected)
	}
	accounts := make(map[resource.AccountID]struct{}, 2)
	nodes := make(map[resource.NodeID]struct{}, 2)
	for _, combination := range selected {
		accounts[combination.AccountID] = struct{}{}
		nodes[combination.NodeID] = struct{}{}
	}
	if len(accounts) != 2 || len(nodes) != 2 {
		t.Fatalf("matching did not maximize independent resources: %+v", selected)
	}
}

func TestMaximumIndependentCombinationsSeparatesNodeByPlatform(t *testing.T) {
	steam := combinationResources(1, "steam", netip.MustParseAddr("2.2.2.1"))
	buff := combinationResources(2, "buff", netip.MustParseAddr("2.2.2.1"))
	buff.Node = steam.Node
	buff.Combination.NodeID = steam.Node.ID

	selected := maximumIndependentCombinations([]resource.CombinationResources{steam, buff})
	if len(selected) != 2 {
		t.Fatalf("same node on independent platforms was serialized: %+v", selected)
	}
}

func TestMaximumIndependentCombinationsIgnoresFullyCooledCombination(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	accountX := combinationResources(1, "steam", netip.MustParseAddr("2.2.2.1"))
	accountY := combinationResources(2, "steam", netip.MustParseAddr("2.2.2.2"))
	accountZ := combinationResources(3, "steam", netip.MustParseAddr("2.2.2.3"))
	xOnY := combinationResources(4, "steam", netip.MustParseAddr("2.2.2.2"))
	xOnY.Account = accountX.Account
	xOnY.Combination.AccountID = accountX.Account.ID
	xOnY.Node = accountY.Node
	xOnY.Combination.NodeID = accountY.Node.ID
	yOnX := combinationResources(5, "steam", netip.MustParseAddr("2.2.2.1"))
	yOnX.Account = accountY.Account
	yOnX.Combination.AccountID = accountY.Account.ID
	yOnX.Node = accountX.Node
	yOnX.Combination.NodeID = accountX.Node.ID

	blocked := map[workerRateKey]struct{}{{
		accountID:   accountX.Account.ID,
		exitAddress: accountX.Node.ExitVerification.Address,
		lane:        harness.scheduler.requestLane(target),
	}: {}}
	candidates := []resource.CombinationResources{accountX, accountY, accountZ, xOnY, yOnX}
	eligible := candidates[:0]
	for _, candidate := range candidates {
		if harness.scheduler.hasRunnableLane(candidate, []Target{target}, blocked, scheduleNow()) {
			eligible = append(eligible, candidate)
		}
	}
	selected := maximumIndependentCombinations(eligible)
	if len(selected) != 3 {
		t.Fatalf("cooled combination reduced matching capacity: %+v", selected)
	}
	for _, combination := range selected {
		if combination.ID == accountX.Combination.ID {
			t.Fatalf("fully cooled combination was selected: %+v", selected)
		}
	}
}

func TestRunnableLaneDoesNotPreFilterStickyProxyByCachedExit(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	proxy := combinationResources(1, "steam", netip.MustParseAddr("2.2.2.1"))
	validUntil := scheduleNow().Add(2 * time.Hour)
	proxy.Node.Kind = resource.NodeKindProxy
	proxy.Node.EgressMode = resource.EgressModeSticky
	proxy.Node.HasProxyCredential = true
	proxy.Node.StickySessionValidUntil = &validUntil
	blocked := map[workerRateKey]struct{}{{
		accountID:   proxy.Account.ID,
		exitAddress: proxy.Node.ExitVerification.Address,
		lane:        harness.scheduler.requestLane(target),
	}: {}}
	if !harness.scheduler.hasRunnableLane(proxy, []Target{target}, blocked, scheduleNow()) {
		t.Fatal("sticky proxy was excluded before its authoritative exit could be refreshed")
	}
}

func TestSchedulePacingCountsOnlyWorkersWithTasks(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		profile := config.Profiles[PlatformSteam]
		profile.RequestInterval = 300 * time.Millisecond
		config.Profiles[PlatformSteam] = profile
	})
	for id := int64(1); id <= 3; id++ {
		harness.addCombination(id, "steam", netip.MustParseAddr(fmt.Sprintf("2.2.2.%d", id)))
	}
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	payload, err := EncodeAskPage(0, AskPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.store.EnqueueTasks(t.Context(), target.ID(), target.SwitchVersion(), []EnqueueSpec{
		{Kind: TaskKindAskPage, Payload: payload},
		{Kind: TaskKindAskPage, Payload: payload},
	}, Cursor{}, 0); err != nil {
		t.Fatal(err)
	}
	pacer := &fakeRequestPacer{}
	harness.scheduler.pacer = pacer
	component, err := harness.coordinator.RegisterComponent()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = harness.coordinator.CancelComponent(context.Background(), component) }()
	workers, err := harness.scheduler.dispatchWorkers(
		t.Context(), component, []Target{target},
		newCycleCombinationResources(harness.store, harness.store),
	)
	if err != nil {
		t.Fatal(err)
	}
	committed := 0
	for _, worker := range workers {
		if worker.Committed {
			committed++
		}
	}
	if committed != 2 {
		t.Fatalf("committed=%d workers=%+v", committed, workers)
	}
	pacer.mu.Lock()
	defer pacer.mu.Unlock()
	if len(pacer.records) != 2 {
		t.Fatalf("pacing records=%+v", pacer.records)
	}
	for _, record := range pacer.records {
		if record.workers != 2 || record.lane != (requestLane{platform: PlatformSteam, endpoint: "market_summary"}) {
			t.Fatalf("pacing record=%+v", record)
		}
	}
}

func TestSchedulePacingCountsPlatformWorkersAcrossEndpoints(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		profile := config.Profiles[PlatformSteam]
		profile.BidEndpoint = "market_orderbook"
		profile.RequestInterval = 300 * time.Millisecond
		config.Profiles[PlatformSteam] = profile
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.1"))
	harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.2"))
	ask := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	bid := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideBid, DesiredEnabled)
	askPayload, err := EncodeAskPage(0, AskPageSize)
	if err != nil {
		t.Fatal(err)
	}
	bidPayload, err := EncodeBidBatch(0, BidBatchSize)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.store.EnqueueTasks(t.Context(), ask.ID(), ask.SwitchVersion(), []EnqueueSpec{
		{Kind: TaskKindAskPage, Payload: askPayload},
	}, Cursor{}, 0); err != nil {
		t.Fatal(err)
	}
	if err := harness.store.EnqueueTasks(t.Context(), bid.ID(), bid.SwitchVersion(), []EnqueueSpec{
		{Kind: TaskKindBidBatch, Payload: bidPayload},
	}, Cursor{}, 0); err != nil {
		t.Fatal(err)
	}
	pacer := &fakeRequestPacer{}
	harness.scheduler.pacer = pacer
	component, err := harness.coordinator.RegisterComponent()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = harness.coordinator.CancelComponent(context.Background(), component) }()
	if _, err := harness.scheduler.dispatchWorkers(
		t.Context(), component, []Target{ask, bid},
		newCycleCombinationResources(harness.store, harness.store),
	); err != nil {
		t.Fatal(err)
	}
	pacer.mu.Lock()
	defer pacer.mu.Unlock()
	if len(pacer.records) != 2 {
		t.Fatalf("pacing records=%+v", pacer.records)
	}
	lanes := make(map[requestLane]struct{}, 2)
	for _, record := range pacer.records {
		if record.workers != 2 {
			t.Fatalf("pacing record=%+v", record)
		}
		lanes[record.lane] = struct{}{}
	}
	if len(lanes) != 2 {
		t.Fatalf("endpoint lanes=%+v", pacer.records)
	}
}

func TestSchedulePacingExcludesConflictingCombinations(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*resource.CombinationResources, resource.CombinationResources)
	}{
		{name: "shared account", mutate: func(current *resource.CombinationResources, first resource.CombinationResources) {
			current.Combination.AccountID = first.Account.ID
			current.Account = first.Account
		}},
		{name: "shared node", mutate: func(current *resource.CombinationResources, first resource.CombinationResources) {
			current.Combination.NodeID = first.Node.ID
			current.Node = first.Node
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newScheduleHarness(t, func(config *SchedulerConfig) {
				profile := config.Profiles[PlatformSteam]
				profile.RequestInterval = 300 * time.Millisecond
				config.Profiles[PlatformSteam] = profile
			})
			harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.1"))
			harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.2"))
			harness.store.mu.Lock()
			first := harness.store.resources[1]
			harness.store.mu.Unlock()
			harness.mutateCombination(2, func(current *resource.CombinationResources) {
				test.mutate(current, first)
			})
			target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
			payload, err := EncodeAskPage(0, AskPageSize)
			if err != nil {
				t.Fatal(err)
			}
			if err := harness.store.EnqueueTasks(t.Context(), target.ID(), target.SwitchVersion(), []EnqueueSpec{
				{Kind: TaskKindAskPage, Payload: payload},
				{Kind: TaskKindAskPage, Payload: payload},
			}, Cursor{}, 0); err != nil {
				t.Fatal(err)
			}
			pacer := &fakeRequestPacer{}
			harness.scheduler.pacer = pacer
			component, err := harness.coordinator.RegisterComponent()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = harness.coordinator.CancelComponent(context.Background(), component) }()
			workers, err := harness.scheduler.dispatchWorkers(
				t.Context(), component, []Target{target},
				newCycleCombinationResources(harness.store, harness.store),
			)
			if err != nil {
				t.Fatal(err)
			}
			committed := 0
			for _, worker := range workers {
				if worker.Committed {
					committed++
				}
			}
			if committed != 1 {
				t.Fatalf("committed=%d workers=%+v", committed, workers)
			}
			pacer.mu.Lock()
			defer pacer.mu.Unlock()
			if len(pacer.records) != 1 || pacer.records[0].workers != 1 {
				t.Fatalf("pacing records=%+v", pacer.records)
			}
		})
	}
}

func TestScheduleLoadsCombinationResourcesOncePerPlatform(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.store.addSummaryTarget(t, PlatformSteam, 252490, market.SideAsk, DesiredEnabled)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	harness.store.mu.Lock()
	calls := harness.store.resourceListCalls[PlatformSteam]
	harness.store.mu.Unlock()
	if calls != 1 {
		t.Fatalf("combination resource list calls = %d, want 1", calls)
	}
}

func TestScheduleQueueDepthScalesPastBaseWatermark(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	const workers = QueueWatermark + 20
	for id := int64(1); id <= workers; id++ {
		harness.addCombination(id, "steam", netip.AddrFrom4([4]byte{2, 2, byte(id / 250), byte(id%250 + 1)}))
	}
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	report, _, _, err := harness.scheduler.planCycle(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Targets) != 1 || report.Targets[0].Enqueued != workers {
		t.Fatalf("planned targets=%+v", report.Targets)
	}
	if depth, err := harness.store.QueueDepth(t.Context(), target.ID()); err != nil || depth != workers {
		t.Fatalf("queue depth=%d err=%v", depth, err)
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
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonEgressUnavailable)
}

func TestScheduleClassifiesUnavailableEgressStates(t *testing.T) {
	tests := []struct {
		name   string
		reason TargetReason
		mutate func(*resource.CombinationResources)
	}{
		{
			name:   "region mismatch",
			reason: TargetReasonEgressUnavailable,
			mutate: func(resources *resource.CombinationResources) {
				resources.Node.Region = resource.NodeRegionForeign
			},
		},
		{
			name:   "exit not verified",
			reason: TargetReasonResourceIncomplete,
			mutate: func(resources *resource.CombinationResources) {
				resources.Node.State = resource.NodeStateValidating
				resources.Node.ExitVerification = nil
			},
		},
		{
			name:   "node unavailable",
			reason: TargetReasonResourceIncomplete,
			mutate: func(resources *resource.CombinationResources) {
				resources.Node.State = resource.NodeStateUnavailable
				resources.Node.ExitVerification = nil
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			harness := newScheduleHarness(t, nil)
			harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
			harness.mutateCombination(1, test.mutate)
			target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

			if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
				t.Fatal(err)
			}
			requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, test.reason)
		})
	}
}

func TestScheduleBlocksDomesticEgressForForeignSteam(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		profile := config.Profiles[PlatformSteam]
		profile.TargetRegion = resource.TargetRegionForeign
		config.Profiles[PlatformSteam] = profile
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonEgressCNBlocked)
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
	harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.3"))
	for id := resource.CombinationID(1); id <= 2; id++ {
		harness.mutateCombination(id, func(resources *resource.CombinationResources) {
			resources.Account.SessionState = resource.AccountSessionStateInvalid
		})
	}
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonSessionInvalid)

	harness.mutateCombination(1, func(resources *resource.CombinationResources) {
		resources.Account.SessionState = resource.AccountSessionStateUnverified
		resources.Account.LastCheckedAt = nil
	})
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualRunning, TargetReasonNone)
	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Workers) != 1 || !report.Workers[0].Committed {
		t.Fatalf("workers after session replacement = %+v", report.Workers)
	}
}

func TestScheduleMixedFailuresPreferEgressUnavailable(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.mutateCombination(1, func(resources *resource.CombinationResources) {
		resources.Account.SessionState = resource.AccountSessionStateInvalid
	})
	harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.3"))
	harness.mutateCombination(2, func(resources *resource.CombinationResources) {
		resources.Node.State = resource.NodeStateValidating
		resources.Node.ExitVerification = nil
	})
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualBlocked, TargetReasonResourceIncomplete)
}

func TestScheduleRateLimitSignalFeedsBack(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.3"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	harness.fetcher.hook = func(request PageFetch) error {
		if request.Lease.Snapshot.CombinationID == 1 {
			return &RateLimitSignal{Scopes: []ratelimit.Scope{ratelimit.ScopeAccountIP}, Reason: ratelimit.ReasonHTTP429}
		}
		return nil
	}
	startedAt := scheduleNow()
	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(harness.admitter.feedbacks) != 1 {
		t.Fatalf("feedbacks = %+v", harness.admitter.feedbacks)
	}
	committed := 0
	for _, worker := range report.Workers {
		if worker.Committed {
			committed++
		} else if worker.retryReason != WorkerWaitReasonRateLimit || worker.BlockOrErr != BlockHTTP429 {
			t.Fatalf("rate signal reason = %q block=%q", worker.retryReason, worker.BlockOrErr)
		} else if worker.RetryAt.Before(startedAt.Add(59 * time.Second)) {
			t.Fatalf("rate signal retry_at = %s", worker.RetryAt)
		}
	}
	if committed != 1 {
		t.Fatalf("workers=%+v", report.Workers)
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualRunning, TargetReasonNone)
}

func TestScheduleRateLimitDefersWorkerWithoutBlockingTarget(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	retryAt := scheduleNow().Add(time.Hour)
	blocker, err := ratelimit.NewBlocker(1, ratelimit.ScopePlatform, ratelimit.BlockReasonBudget, retryAt)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := ratelimit.Block([]ratelimit.Blocker{blocker})
	if err != nil {
		t.Fatal(err)
	}
	harness.admitter.script = func(int, ratelimit.Request) (ratelimit.Decision, error) {
		return blocked, nil
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Workers) != 1 || report.Workers[0].retryReason != WorkerWaitReasonDeferred ||
		report.Workers[0].BlockOrErr != BlockHTTP429 {
		t.Fatalf("deferred workers = %+v", report.Workers)
	}
	if len(harness.fetcher.calls) != 0 || harness.store.queuedCount() != QueueWatermark {
		t.Fatalf("blocked fetches=%d queued=%d", len(harness.fetcher.calls), harness.store.queuedCount())
	}
	stored := harness.store.target(target.ID())
	requireTargetState(t, stored, ActualRunning, TargetReasonNone)
	requests := len(harness.admitter.requests)
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(harness.admitter.requests) <= requests {
		t.Fatal("running target did not let the worker recheck its exact cooldown")
	}
}

func TestScheduleExactCooldownDoesNotWaitInsideCycle(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	retryAt := scheduleNow().Add(500 * time.Millisecond)
	blocker, err := ratelimit.NewBlocker(1, ratelimit.ScopeAccountIP, ratelimit.BlockReasonCooldown, retryAt)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := ratelimit.Block([]ratelimit.Blocker{blocker})
	if err != nil {
		t.Fatal(err)
	}
	harness.admitter.script = func(int, ratelimit.Request) (ratelimit.Decision, error) {
		return blocked, nil
	}
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(harness.admitter.requests) != 1 {
		t.Fatalf("cooldown admission attempts=%d", len(harness.admitter.requests))
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualRunning, TargetReasonNone)
}

func TestScheduleSharedWarmupDoesNotSynchronizeWorkers(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.addCombination(2, "steam", netip.MustParseAddr("2.2.2.3"))
	target := harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	blocker, err := ratelimit.NewBlocker(
		1, ratelimit.ScopeAccountIP, ratelimit.BlockReasonWarmup, scheduleNow().Add(500*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := ratelimit.Block([]ratelimit.Blocker{blocker})
	if err != nil {
		t.Fatal(err)
	}
	harness.admitter.script = func(int, ratelimit.Request) (ratelimit.Decision, error) {
		return blocked, nil
	}
	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(harness.admitter.requests) != 2 || len(harness.fetcher.calls) != 0 {
		t.Fatalf("warmup admissions=%d fetches=%d", len(harness.admitter.requests), len(harness.fetcher.calls))
	}
	requireTargetState(t, harness.store.target(target.ID()), ActualRunning, TargetReasonNone)
}

func TestScheduleAdmissionRetryReentersPacing(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		profile := config.Profiles[PlatformSteam]
		profile.RequestInterval = 300 * time.Millisecond
		config.Profiles[PlatformSteam] = profile
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	pacer := &fakeRequestPacer{}
	harness.scheduler.pacer = pacer
	harness.admitter.script = func(index int, request ratelimit.Request) (ratelimit.Decision, error) {
		if index > 0 {
			return harness.admitter.allow(request)
		}
		blocker, err := ratelimit.NewBlocker(
			1, ratelimit.ScopeAccountIP, ratelimit.BlockReasonBudget, scheduleNow().Add(time.Millisecond),
		)
		if err != nil {
			return ratelimit.Decision{}, err
		}
		return ratelimit.Block([]ratelimit.Blocker{blocker})
	}
	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Workers) != 1 || !report.Workers[0].Committed || len(harness.admitter.requests) != 2 {
		t.Fatalf("workers=%+v admissions=%d", report.Workers, len(harness.admitter.requests))
	}
	pacer.mu.Lock()
	defer pacer.mu.Unlock()
	if len(pacer.records) != 2 {
		t.Fatalf("pacing records=%+v", pacer.records)
	}
}

func TestScheduleRateLimitPastRetryUsesBoundedPolling(t *testing.T) {
	harness := newScheduleHarness(t, func(config *SchedulerConfig) {
		config.PageTimeout = 250 * time.Millisecond
	})
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	blocker, err := ratelimit.NewBlocker(1, ratelimit.ScopePlatform, ratelimit.BlockReasonBudget, scheduleNow().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := ratelimit.Block([]ratelimit.Blocker{blocker})
	if err != nil {
		t.Fatal(err)
	}
	harness.admitter.script = func(int, ratelimit.Request) (ratelimit.Decision, error) {
		return blocked, nil
	}

	if _, err := harness.scheduler.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(harness.admitter.requests); got < 2 || got > 4 {
		t.Fatalf("admission polls = %d", got)
	}
}

func TestScheduleReportsNetworkAndRequeueFailures(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	harness.addCombination(1, "steam", netip.MustParseAddr("2.2.2.2"))
	harness.store.addSummaryTarget(t, PlatformSteam, 730, market.SideAsk, DesiredEnabled)
	requeueErr := errors.New("task requeue failed")
	harness.fetcher.hook = func(PageFetch) error {
		harness.store.mu.Lock()
		harness.store.failRequeueErr = requeueErr
		harness.store.mu.Unlock()
		return ErrFetchNetwork
	}

	report, err := harness.scheduler.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Workers) != 1 || !errors.Is(report.Workers[0].Err, ErrFetchNetwork) || !errors.Is(report.Workers[0].Err, requeueErr) {
		t.Fatalf("workers = %+v", report.Workers)
	}
}

func TestScheduleCleanupContextIsDetachedButBounded(t *testing.T) {
	harness := newScheduleHarness(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := harness.scheduler.requeueTask(ctx, Task{id: 1, claimGeneration: 1}, 1); err != nil {
		t.Fatal(err)
	}
	if err := harness.scheduler.applyRateLimitFeedback(ctx, ratelimit.Admission{}, nil, ratelimit.ReasonHTTP429, time.Minute); err != nil {
		t.Fatal(err)
	}

	harness.store.mu.Lock()
	requeueHasDeadline := harness.store.requeueHasDeadline
	requeueContextErr := harness.store.requeueContextErr
	harness.store.mu.Unlock()
	harness.admitter.mu.Lock()
	feedbackHasDeadline := harness.admitter.feedbackHasDeadline
	feedbackContextErr := harness.admitter.feedbackContextErr
	harness.admitter.mu.Unlock()
	if !requeueHasDeadline || requeueContextErr != nil {
		t.Fatalf("requeue context deadline=%v err=%v", requeueHasDeadline, requeueContextErr)
	}
	if !feedbackHasDeadline || feedbackContextErr != nil {
		t.Fatalf("feedback context deadline=%v err=%v", feedbackHasDeadline, feedbackContextErr)
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
	broken = valid
	broken.Profiles = map[Platform]PlatformProfile{"steam": {
		TargetRegion: resource.TargetRegionDomestic, RequestInterval: time.Nanosecond,
	}}
	if _, err := NewScheduler(store, store, coordinator, admitter, fetcher, broken); err == nil {
		t.Fatal("sub-microsecond request interval must be rejected")
	}
	broken = valid
	broken.Profiles = map[Platform]PlatformProfile{"steam": {
		TargetRegion: resource.TargetRegionDomestic, RequestInterval: time.Second,
	}}
	if _, err := NewScheduler(store, store, coordinator, admitter, fetcher, broken); err == nil {
		t.Fatal("request interval equal to page and claim timeout must be rejected")
	}
}
