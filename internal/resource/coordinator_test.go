package resource

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type coordinatorRepositoryStub struct {
	mu           sync.Mutex
	combinations map[CombinationID]CombinationResources
	nodes        map[NodeID]AccessNode
	calls        map[string]int
	credential   []byte

	blockedCombination CombinationID
	readStarted        chan struct{}
	readContinue       chan struct{}
	blockedOpen        string
	openStarted        chan struct{}
	openContinue       chan struct{}
	lastOpened         []byte
	blockBegin         bool
	beginStarted       chan struct{}
	beginContinue      chan struct{}
	lastAccountOpen    accountCredentialOpen
	lastNodeOpen       nodeCredentialOpen
}

type accountCredentialOpen struct {
	id       AccountID
	revision int64
}

type nodeCredentialOpen struct {
	id             NodeID
	egressRevision int64
}

func newCoordinatorRepositoryStub(resources ...CombinationResources) *coordinatorRepositoryStub {
	repository := &coordinatorRepositoryStub{
		combinations: make(map[CombinationID]CombinationResources),
		nodes:        make(map[NodeID]AccessNode),
		calls:        make(map[string]int),
		credential:   []byte("opaque-credential"),
	}
	for _, value := range resources {
		repository.combinations[value.Combination.ID] = cloneCombinationResources(value)
		repository.nodes[value.Node.ID] = cloneCombinationResources(value).Node
	}
	return repository
}

func (repository *coordinatorRepositoryStub) CombinationResources(ctx context.Context, id CombinationID) (CombinationResources, bool, error) {
	repository.mu.Lock()
	blocked := repository.blockedCombination == id && repository.readContinue != nil
	started := repository.readStarted
	continued := repository.readContinue
	repository.mu.Unlock()
	if blocked {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-continued:
		case <-ctx.Done():
			return CombinationResources{}, false, ctx.Err()
		}
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, found := repository.combinations[id]
	return cloneCombinationResources(value), found, nil
}

func (repository *coordinatorRepositoryStub) Node(_ context.Context, id NodeID) (AccessNode, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	node, found := repository.nodes[id]
	return cloneCombinationResources(CombinationResources{Node: node}).Node, found, nil
}

func (repository *coordinatorRepositoryStub) CreateCombination(_ context.Context, accountID AccountID, nodeID NodeID) (AccountNodeCombination, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["create_combination"]++
	var account PlatformAccount
	for _, value := range repository.combinations {
		if value.Account.ID == accountID {
			account = value.Account
			break
		}
	}
	node, found := repository.nodes[nodeID]
	if account.ID == 0 || !found {
		return AccountNodeCombination{}, errors.New("incompatible resources")
	}
	var next CombinationID = 1
	for id := range repository.combinations {
		if id >= next {
			next = id + 1
		}
	}
	combination := AccountNodeCombination{ID: next, Platform: account.Platform, AccountID: accountID, NodeID: nodeID}
	repository.combinations[next] = CombinationResources{Combination: combination, Account: account, Node: node}
	return combination, nil
}

func (repository *coordinatorRepositoryStub) DeleteCombination(_ context.Context, id CombinationID) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["delete_combination"]++
	delete(repository.combinations, id)
	return nil
}

func (repository *coordinatorRepositoryStub) DeleteAccount(_ context.Context, _ AccountID) error {
	repository.recordCall("delete_account")
	return nil
}

func (repository *coordinatorRepositoryStub) DeleteNode(_ context.Context, id NodeID) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["delete_node"]++
	delete(repository.nodes, id)
	return nil
}

func (repository *coordinatorRepositoryStub) ReplaceAccountSession(_ context.Context, id AccountID, expectedRevision int64, _ []byte) (PlatformAccount, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["replace_account"]++
	for combinationID, value := range repository.combinations {
		if value.Account.ID == id && value.Account.SessionRevision == expectedRevision {
			value.Account.SessionRevision++
			value.Account.SessionState = AccountSessionStateUnverified
			value.Account.LastCheckedAt = nil
			repository.combinations[combinationID] = value
			return value.Account, nil
		}
	}
	return PlatformAccount{}, errors.New("account revision conflict")
}

func (repository *coordinatorRepositoryStub) OpenAccountSessionAt(ctx context.Context, id AccountID, revision int64) ([]byte, error) {
	repository.mu.Lock()
	valid := false
	for _, value := range repository.combinations {
		if value.Account.ID == id && value.Account.SessionRevision == revision {
			valid = true
			break
		}
	}
	repository.lastAccountOpen = accountCredentialOpen{id: id, revision: revision}
	repository.mu.Unlock()
	if !valid {
		return nil, errors.New("account revision conflict")
	}
	return repository.openCredential(ctx, "open_account")
}

func (repository *coordinatorRepositoryStub) RecordAccountSessionCheck(_ context.Context, id AccountID, revision int64, state AccountSessionState, _ time.Time) (PlatformAccount, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["record_session_check"]++
	for _, resources := range repository.combinations {
		if resources.Account.ID == id && resources.Account.SessionRevision == revision {
			account := resources.Account
			account.SessionState = state
			now := time.Now().UTC()
			account.LastCheckedAt = &now
			resources.Account = account
			repository.combinations[resources.Combination.ID] = resources
			return account, nil
		}
	}
	return PlatformAccount{}, errors.New("account revision conflict")
}

func (repository *coordinatorRepositoryStub) ReplaceNodeConnection(_ context.Context, id NodeID, expectedRevision int64, input NodeConnectionInput) (AccessNode, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["replace_node"]++
	node, found := repository.nodes[id]
	if !found || node.EgressRevision != expectedRevision {
		return AccessNode{}, errors.New("node revision conflict")
	}
	node.Kind = input.Kind
	node.Region = input.Region
	node.EgressMode = input.EgressMode
	node.HasProxyCredential = input.Kind == NodeKindProxy && len(input.ProxyCredential) > 0
	node.StickySessionValidUntil = input.StickySessionValidUntil
	node.State = NodeStateValidating
	node.EgressRevision++
	node.ExitVerification = nil
	repository.storeNodeLocked(node)
	return node, nil
}

func (repository *coordinatorRepositoryStub) BeginNodeRevalidation(ctx context.Context, id NodeID, expectedRevision int64) (AccessNode, error) {
	repository.mu.Lock()
	blocked := repository.blockBegin
	started := repository.beginStarted
	continued := repository.beginContinue
	repository.mu.Unlock()
	if blocked {
		select {
		case started <- struct{}{}:
		default:
		}
		<-continued
	} else if err := ctx.Err(); err != nil {
		return AccessNode{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["begin_revalidation"]++
	node, found := repository.nodes[id]
	if !found || node.EgressRevision != expectedRevision {
		return AccessNode{}, errors.New("node revision conflict")
	}
	node.State = NodeStateValidating
	node.EgressRevision++
	node.ExitVerification = nil
	repository.storeNodeLocked(node)
	return node, nil
}

func (repository *coordinatorRepositoryStub) OpenNodeProxyCredentialAt(
	ctx context.Context,
	id NodeID,
	egressRevision int64,
) ([]byte, error) {
	repository.mu.Lock()
	node, found := repository.nodes[id]
	valid := found && node.EgressRevision == egressRevision
	repository.lastNodeOpen = nodeCredentialOpen{
		id: id, egressRevision: egressRevision,
	}
	repository.mu.Unlock()
	if !valid {
		return nil, errors.New("node revision conflict")
	}
	return repository.openCredential(ctx, "open_node")
}

func (repository *coordinatorRepositoryStub) RecordNodeExit(ctx context.Context, id NodeID, expectedRevision int64, address netip.Addr, verifiedAt, validUntil time.Time) (AccessNode, error) {
	if err := ctx.Err(); err != nil {
		return AccessNode{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["record_exit"]++
	node, found := repository.nodes[id]
	if !found || node.EgressRevision != expectedRevision {
		return AccessNode{}, errors.New("node revision conflict")
	}
	node.State = NodeStateAvailable
	node.ExitVerification = &ExitVerification{
		VerifiedRevision: expectedRevision, Address: address, VerifiedAt: verifiedAt, ValidUntil: validUntil,
	}
	repository.storeNodeLocked(node)
	return node, nil
}

func (repository *coordinatorRepositoryStub) ConfirmNodeExit(ctx context.Context, id NodeID, expectedRevision int64, address netip.Addr, verifiedAt, validUntil time.Time) (AccessNode, error) {
	if err := ctx.Err(); err != nil {
		return AccessNode{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["confirm_exit"]++
	node, found := repository.nodes[id]
	if !found || node.EgressRevision != expectedRevision {
		return AccessNode{}, errors.New("node revision conflict")
	}
	node.State = NodeStateAvailable
	node.EgressRevision++
	node.ExitVerification = &ExitVerification{
		VerifiedRevision: node.EgressRevision, Address: address, VerifiedAt: verifiedAt, ValidUntil: validUntil,
	}
	repository.storeNodeLocked(node)
	return node, nil
}

func (repository *coordinatorRepositoryStub) MarkNodeUnavailable(ctx context.Context, id NodeID, expectedRevision int64) (AccessNode, error) {
	if err := ctx.Err(); err != nil {
		return AccessNode{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.calls["mark_unavailable"]++
	node, found := repository.nodes[id]
	if !found || node.EgressRevision != expectedRevision {
		return AccessNode{}, errors.New("node revision conflict")
	}
	node.State = NodeStateUnavailable
	node.ExitVerification = nil
	repository.storeNodeLocked(node)
	return node, nil
}

func (repository *coordinatorRepositoryStub) storeNodeLocked(node AccessNode) {
	repository.nodes[node.ID] = node
	for id, value := range repository.combinations {
		if value.Node.ID == node.ID {
			value.Node = cloneCombinationResources(CombinationResources{Node: node}).Node
			repository.combinations[id] = value
		}
	}
}

func (repository *coordinatorRepositoryStub) recordCall(operation string) {
	repository.mu.Lock()
	repository.calls[operation]++
	repository.mu.Unlock()
}

func (repository *coordinatorRepositoryStub) openCredential(ctx context.Context, operation string) ([]byte, error) {
	repository.mu.Lock()
	repository.calls[operation]++
	blocked := repository.blockedOpen == operation && repository.openContinue != nil
	started := repository.openStarted
	continued := repository.openContinue
	plaintext := append([]byte(nil), repository.credential...)
	repository.lastOpened = plaintext
	repository.mu.Unlock()
	if blocked {
		select {
		case started <- struct{}{}:
		default:
		}
		<-continued
		return plaintext, nil
	}
	if err := ctx.Err(); err != nil {
		return plaintext, err
	}
	return plaintext, nil
}

func (repository *coordinatorRepositoryStub) callCount(operation string) int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.calls[operation]
}

func (repository *coordinatorRepositoryStub) openedAccount() accountCredentialOpen {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.lastAccountOpen
}

func (repository *coordinatorRepositoryStub) openedNode() nodeCredentialOpen {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.lastNodeOpen
}

func (repository *coordinatorRepositoryStub) removeCombination(id CombinationID) {
	repository.mu.Lock()
	delete(repository.combinations, id)
	repository.mu.Unlock()
}

func TestCoordinatorCombinationExclusivityAndSnapshot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	first := usableCombinationResources(now, 1, 11, 21)
	sharedAccount := usableCombinationResources(now, 2, 11, 22)
	samePlatform := usableCombinationResources(now, 3, 12, 21)
	crossPlatform := usableCombinationResourcesFor(now, 5, 13, 21, "buff")
	disjoint := usableCombinationResources(now, 4, 12, 22)
	repository := newCoordinatorRepositoryStub(first, sharedAccount, samePlatform, crossPlatform, disjoint)
	coordinator := newTestCoordinator(t, repository)
	components := registerTestComponents(t, coordinator, 5)

	lease, err := coordinator.AcquireCombination(context.Background(), components[0], 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatalf("AcquireCombination(first) error = %v", err)
	}
	if lease.Kind != LeaseKindCombination || lease.Snapshot.CombinationID != 1 ||
		lease.Snapshot.AccountID != 11 || lease.Snapshot.NodeID != 21 ||
		lease.Snapshot.Platform != "steam" ||
		lease.Snapshot.SessionRevision != 3 || lease.Snapshot.EgressRevision != 4 ||
		lease.Snapshot.NodeKind != NodeKindDirect ||
		lease.Snapshot.NodeRegion != NodeRegionDomestic || lease.Snapshot.EgressMode != EgressModeStatic ||
		lease.Snapshot.ExitAddress != netip.MustParseAddr("8.8.8.8") ||
		lease.Snapshot.ExitVerifiedAt.IsZero() || lease.Snapshot.ExitValidUntil.IsZero() {
		t.Fatalf("unexpected lease snapshot: %+v", lease.Snapshot)
	}
	if _, err := coordinator.AcquireCombination(context.Background(), components[1], 2, TargetRegionDomestic, now); !errors.Is(err, ErrResourceOccupied) {
		t.Fatalf("shared account error = %v", err)
	}
	if _, err := coordinator.AcquireCombination(context.Background(), components[2], 3, TargetRegionDomestic, now); !errors.Is(err, ErrResourceOccupied) {
		t.Fatalf("same-platform shared node error = %v", err)
	}
	cross, err := coordinator.AcquireCombination(context.Background(), components[3], 5, TargetRegionDomestic, now)
	if err != nil {
		t.Fatalf("AcquireCombination(cross-platform) error = %v", err)
	}
	if cross.Snapshot.Platform != "buff" || cross.Snapshot.NodeID != 21 {
		t.Fatalf("cross-platform snapshot = %+v", cross.Snapshot)
	}
	other, err := coordinator.AcquireCombination(context.Background(), components[4], 4, TargetRegionDomestic, now)
	if err != nil {
		t.Fatalf("AcquireCombination(disjoint) error = %v", err)
	}
	if err := coordinator.Release(other.Token); err != nil {
		t.Fatalf("Release(disjoint) error = %v", err)
	}
	if err := coordinator.Release(cross.Token); err != nil {
		t.Fatalf("Release(cross-platform) error = %v", err)
	}
	if err := coordinator.Release(lease.Token); err != nil {
		t.Fatalf("Release(first) error = %v", err)
	}
}

func TestCoordinatorAcquireSessionStates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	unverified := usableCombinationResources(now, 1, 11, 21)
	unverified.Account.SessionState = AccountSessionStateUnverified
	unverified.Account.LastCheckedAt = nil
	unverifiedCoord := newTestCoordinator(t, newCoordinatorRepositoryStub(unverified))
	unverifiedLease, err := unverifiedCoord.AcquireCombination(
		context.Background(), registerTestComponents(t, unverifiedCoord, 1)[0],
		1, TargetRegionDomestic, now,
	)
	if err != nil {
		t.Fatalf("unverified AcquireCombination() error = %v", err)
	}
	if err := unverifiedCoord.Release(unverifiedLease.Token); err != nil {
		t.Fatalf("Release(unverified) error = %v", err)
	}

	invalid := usableCombinationResources(now, 1, 11, 21)
	invalid.Account.SessionState = AccountSessionStateInvalid
	invalidCoord := newTestCoordinator(t, newCoordinatorRepositoryStub(invalid))
	if _, err := invalidCoord.AcquireCombination(
		context.Background(), registerTestComponents(t, invalidCoord, 1)[0],
		1, TargetRegionDomestic, now,
	); !errors.Is(err, ErrAccountSessionUnusable) {
		t.Fatalf("invalid session error = %v", err)
	}
}

func TestLeaseAuthorityDeactivationRejectsBeforeContextCancellation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repository := newCoordinatorRepositoryStub(usableCombinationResources(now, 1, 11, 21))
	coordinator := newTestCoordinator(t, repository)
	component := registerTestComponents(t, coordinator, 1)[0]
	lease, err := coordinator.AcquireCombination(context.Background(), component, 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Context().Err() != nil {
		t.Fatal("new lease context is already canceled")
	}
	lease.authority.deactivate()
	if lease.Context().Err() != nil {
		t.Fatal("authority deactivation unexpectedly canceled the context")
	}
	if _, err := lease.CombinationSnapshot(); !errors.Is(err, ErrLeaseNotHeld) {
		t.Fatalf("CombinationSnapshot() error = %v", err)
	}
	if err := coordinator.Release(lease.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorConcurrentAcquireHasOneWinner(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repository := newCoordinatorRepositoryStub(usableCombinationResources(now, 1, 11, 21))
	coordinator := newTestCoordinator(t, repository)
	components := registerTestComponents(t, coordinator, 24)

	start := make(chan struct{})
	results := make(chan struct {
		lease Lease
		err   error
	}, len(components))
	var wait sync.WaitGroup
	for _, component := range components {
		component := component
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			lease, err := coordinator.AcquireCombination(context.Background(), component, 1, TargetRegionDomestic, now)
			results <- struct {
				lease Lease
				err   error
			}{lease: lease, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	var winner Lease
	for result := range results {
		if result.err == nil {
			winners++
			winner = result.lease
			continue
		}
		if !errors.Is(result.err, ErrResourceOccupied) {
			t.Fatalf("AcquireCombination() error = %v", result.err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want 1", winners)
	}
	if err := coordinator.Release(winner.Token); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestCoordinatorStaleReleaseAndRestartEpoch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repository := newCoordinatorRepositoryStub(usableCombinationResources(now, 1, 11, 21))
	coordinator := newTestCoordinator(t, repository)
	components := registerTestComponents(t, coordinator, 3)
	first, err := coordinator.AcquireCombination(context.Background(), components[0], 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Release(first.Token); err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.AcquireCombination(context.Background(), components[1], 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Release(first.Token); !errors.Is(err, ErrLeaseNotHeld) {
		t.Fatalf("stale Release() error = %v", err)
	}
	if _, err := coordinator.AcquireCombination(context.Background(), components[2], 1, TargetRegionDomestic, now); !errors.Is(err, ErrResourceOccupied) {
		t.Fatalf("lease was cleared by stale token: %v", err)
	}

	restarted := newTestCoordinator(t, repository)
	restartedComponent := registerTestComponents(t, restarted, 1)[0]
	restartedLease, err := restarted.AcquireCombination(context.Background(), restartedComponent, 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatalf("restarted AcquireCombination() error = %v", err)
	}
	if err := restarted.Release(second.Token); !errors.Is(err, ErrLeaseNotHeld) {
		t.Fatalf("old epoch Release() error = %v", err)
	}
	if err := coordinator.Release(second.Token); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Release(restartedLease.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorParentCancelKeepsOccupationUntilRelease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repository := newCoordinatorRepositoryStub(usableCombinationResources(now, 1, 11, 21))
	coordinator := newTestCoordinator(t, repository)
	components := registerTestComponents(t, coordinator, 2)
	parent, cancel := context.WithCancel(context.Background())
	lease, err := coordinator.AcquireCombination(parent, components[0], 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-lease.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("lease context was not canceled by parent")
	}
	if _, err := coordinator.AcquireCombination(context.Background(), components[1], 1, TargetRegionDomestic, now); !errors.Is(err, ErrResourceOccupied) {
		t.Fatalf("parent cancellation released occupation: %v", err)
	}
	if err := coordinator.Release(lease.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorCancelWaitsForExplicitReleaseAndCanRetry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	first := usableCombinationResources(now, 1, 11, 21)
	second := usableCombinationResources(now, 2, 12, 22)
	repository := newCoordinatorRepositoryStub(first, second)
	coordinator := newTestCoordinator(t, repository)
	component := registerTestComponents(t, coordinator, 1)[0]
	leaseOne, err := coordinator.AcquireCombination(context.Background(), component, 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}
	leaseTwo, err := coordinator.AcquireCombination(context.Background(), component, 2, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}
	causes := make(chan error, 2)
	for _, lease := range []Lease{leaseOne, leaseTwo} {
		lease := lease
		go func() {
			<-lease.Context().Done()
			causes <- context.Cause(lease.Context())
			_ = coordinator.Release(lease.Token)
		}()
	}
	if err := coordinator.CancelComponent(context.Background(), component); err != nil {
		t.Fatalf("CancelComponent() error = %v", err)
	}
	for range 2 {
		if cause := <-causes; !errors.Is(cause, ErrComponentCanceled) {
			t.Fatalf("lease cancellation cause = %v", cause)
		}
	}
	if _, err := coordinator.AcquireCombination(context.Background(), component, 1, TargetRegionDomestic, now); !errors.Is(err, ErrComponentUnavailable) {
		t.Fatalf("canceled component AcquireCombination() error = %v", err)
	}
	if err := coordinator.CancelComponent(context.Background(), component); err != nil {
		t.Fatalf("repeated CancelComponent() error = %v", err)
	}

	stuckComponent := registerTestComponents(t, coordinator, 1)[0]
	stuck, err := coordinator.AcquireCombination(context.Background(), stuckComponent, 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	if err := coordinator.CancelComponent(waitContext, stuckComponent); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed CancelComponent() error = %v", err)
	}
	competitor := registerTestComponents(t, coordinator, 1)[0]
	if _, err := coordinator.AcquireCombination(context.Background(), competitor, 1, TargetRegionDomestic, now); !errors.Is(err, ErrResourceOccupied) {
		t.Fatalf("timed cancellation force-released lease: %v", err)
	}
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- coordinator.CancelComponent(context.Background(), stuckComponent)
	}()
	if err := coordinator.Release(stuck.Token); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-retryDone:
		if err != nil {
			t.Fatalf("retry CancelComponent() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry CancelComponent() did not drain")
	}
}

func TestCoordinatorReReadsDeletedCombination(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repository := newCoordinatorRepositoryStub(usableCombinationResources(now, 1, 11, 21))
	coordinator := newTestCoordinator(t, repository)
	component := registerTestComponents(t, coordinator, 1)[0]
	repository.removeCombination(1)
	if _, err := coordinator.AcquireCombination(context.Background(), component, 1, TargetRegionDomestic, now); !errors.Is(err, ErrCombinationNotFound) {
		t.Fatalf("AcquireCombination(deleted) error = %v", err)
	}
}

func TestCoordinatorReleaseDoesNotWaitForRepositoryRead(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	first := usableCombinationResources(now, 1, 11, 21)
	second := usableCombinationResources(now, 2, 12, 22)
	repository := newCoordinatorRepositoryStub(first, second)
	coordinator := newTestCoordinator(t, repository)
	components := registerTestComponents(t, coordinator, 2)
	lease, err := coordinator.AcquireCombination(context.Background(), components[0], 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}
	repository.mu.Lock()
	repository.blockedCombination = 2
	repository.readStarted = make(chan struct{}, 1)
	repository.readContinue = make(chan struct{})
	started := repository.readStarted
	continued := repository.readContinue
	repository.mu.Unlock()
	acquireDone := make(chan error, 1)
	var secondLease Lease
	go func() {
		var err error
		secondLease, err = coordinator.AcquireCombination(context.Background(), components[1], 2, TargetRegionDomestic, now)
		acquireDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("repository read did not block")
	}
	releaseDone := make(chan error, 1)
	go func() { releaseDone <- coordinator.Release(lease.Token) }()
	select {
	case err := <-releaseDone:
		if err != nil {
			t.Fatalf("Release() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Release was blocked by repository read")
	}
	close(continued)
	if err := <-acquireDone; err != nil {
		t.Fatalf("second AcquireCombination() error = %v", err)
	}
	if err := coordinator.Release(secondLease.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorCoordWaitHonorsContext(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	first := usableCombinationResources(now, 1, 11, 21)
	second := usableCombinationResources(now, 2, 12, 22)
	repository := newCoordinatorRepositoryStub(first, second)
	repository.blockedCombination = 1
	repository.readStarted = make(chan struct{}, 1)
	repository.readContinue = make(chan struct{})
	coordinator := newTestCoordinator(t, repository)
	components := registerTestComponents(t, coordinator, 2)
	type acquisitionResult struct {
		lease Lease
		err   error
	}
	firstResult := make(chan acquisitionResult, 1)
	go func() {
		lease, err := coordinator.AcquireCombination(context.Background(), components[0], 1, TargetRegionDomestic, now)
		firstResult <- acquisitionResult{lease: lease, err: err}
	}()
	select {
	case <-repository.readStarted:
	case <-time.After(time.Second):
		t.Fatal("first acquisition did not enter repository")
	}

	waitContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	waitResult := make(chan error, 1)
	go func() {
		_, err := coordinator.AcquireCombination(waitContext, components[1], 2, TargetRegionDomestic, now)
		waitResult <- err
	}()
	select {
	case err := <-waitResult:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting AcquireCombination() error = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("waiting AcquireCombination ignored its context")
	}

	close(repository.readContinue)
	acquired := <-firstResult
	if acquired.err != nil {
		t.Fatalf("first AcquireCombination() error = %v", acquired.err)
	}
	if err := coordinator.Release(acquired.lease.Token); err != nil {
		t.Fatal(err)
	}
	lease, err := coordinator.AcquireCombination(context.Background(), components[1], 2, TargetRegionDomestic, now)
	if err != nil {
		t.Fatalf("AcquireCombination() after canceled waiter error = %v", err)
	}
	if err := coordinator.Release(lease.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorMaintenanceOwnsRevalidationAndCredentials(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	resources := usableCombinationResources(now, 1, 11, 21)
	resources.Node.Kind = NodeKindProxy
	resources.Node.HasProxyCredential = true
	repository := newCoordinatorRepositoryStub(resources)
	coordinator := newTestCoordinator(t, repository)
	components := registerTestComponents(t, coordinator, 2)

	requestLease, err := coordinator.AcquireCombination(context.Background(), components[0], 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.AcquireNodeMaintenance(context.Background(), components[1], 21); !errors.Is(err, ErrResourceOccupied) {
		t.Fatalf("maintenance while request active error = %v", err)
	}
	accountSecret, err := coordinator.OpenAccountSession(context.Background(), requestLease.Token)
	if err != nil || string(accountSecret) != "opaque-credential" {
		t.Fatalf("OpenAccountSession() = %q, %v", accountSecret, err)
	}
	if opened := repository.openedAccount(); opened.id != 11 || opened.revision != 3 {
		t.Fatalf("OpenAccountSessionAt() arguments = %+v", opened)
	}
	nodeSecret, err := coordinator.OpenNodeProxyCredential(context.Background(), requestLease.Token)
	if err != nil || string(nodeSecret) != "opaque-credential" {
		t.Fatalf("OpenNodeProxyCredential(request) = %q, %v", nodeSecret, err)
	}
	if opened := repository.openedNode(); opened.id != 21 || opened.egressRevision != 4 {
		t.Fatalf("OpenNodeProxyCredentialAt(request) arguments = %+v", opened)
	}
	if err := coordinator.Release(requestLease.Token); err != nil {
		t.Fatal(err)
	}

	maintenance, err := coordinator.AcquireNodeMaintenance(context.Background(), components[1], 21)
	if err != nil {
		t.Fatal(err)
	}
	if maintenance.Kind != LeaseKindNodeMaintenance || maintenance.Snapshot.EgressRevision != 4 {
		t.Fatalf("maintenance snapshot = %+v", maintenance)
	}
	if _, err := coordinator.AcquireCombination(context.Background(), components[0], 1, TargetRegionDomestic, now); !errors.Is(err, ErrResourceOccupied) {
		t.Fatalf("request while maintenance active error = %v", err)
	}
	revalidating, err := coordinator.BeginNodeRevalidation(context.Background(), maintenance.Token, 4)
	if err != nil {
		t.Fatalf("BeginNodeRevalidation() error = %v", err)
	}
	if revalidating.EgressRevision != 5 || revalidating.State != NodeStateValidating {
		t.Fatalf("revalidating node = %+v", revalidating)
	}
	nodeSecret, err = coordinator.OpenNodeProxyCredential(context.Background(), maintenance.Token)
	if err != nil || string(nodeSecret) != "opaque-credential" {
		t.Fatalf("OpenNodeProxyCredential(maintenance) = %q, %v", nodeSecret, err)
	}
	if opened := repository.openedNode(); opened.id != 21 || opened.egressRevision != 5 {
		t.Fatalf("OpenNodeProxyCredentialAt(maintenance) arguments = %+v", opened)
	}
	verifiedAt := now.Add(time.Minute)
	available, err := coordinator.RecordNodeExit(
		context.Background(), maintenance.Token, netip.MustParseAddr("1.1.1.1"), verifiedAt, verifiedAt.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("RecordNodeExit() error = %v", err)
	}
	if available.EgressRevision != 5 || available.State != NodeStateAvailable {
		t.Fatalf("available node = %+v", available)
	}
	if err := coordinator.Release(maintenance.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.MarkNodeUnavailable(context.Background(), maintenance.Token); !errors.Is(err, ErrLeaseNotHeld) {
		t.Fatalf("stale maintenance token error = %v", err)
	}
}

func TestCoordinatorBeginRevalidationSynchronizesCommittedRevisionAfterCallerCancel(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repository := newCoordinatorRepositoryStub(usableCombinationResources(now, 1, 11, 21))
	repository.blockBegin = true
	repository.beginStarted = make(chan struct{}, 1)
	repository.beginContinue = make(chan struct{})
	coordinator := newTestCoordinator(t, repository)
	component := registerTestComponents(t, coordinator, 1)[0]
	maintenance, err := coordinator.AcquireNodeMaintenance(context.Background(), component, 21)
	if err != nil {
		t.Fatal(err)
	}

	operationContext, cancel := context.WithCancel(context.Background())
	type beginResult struct {
		node AccessNode
		err  error
	}
	result := make(chan beginResult, 1)
	go func() {
		node, err := coordinator.BeginNodeRevalidation(operationContext, maintenance.Token, 4)
		result <- beginResult{node: node, err: err}
	}()
	select {
	case <-repository.beginStarted:
	case <-time.After(time.Second):
		t.Fatal("BeginNodeRevalidation did not enter repository")
	}
	cancel()
	close(repository.beginContinue)
	committed := <-result
	if !errors.Is(committed.err, context.Canceled) {
		t.Fatalf("BeginNodeRevalidation() error = %v", committed.err)
	}
	if committed.node.ID != 21 || committed.node.EgressRevision != 5 || committed.node.State != NodeStateValidating {
		t.Fatalf("BeginNodeRevalidation() committed node = %+v", committed.node)
	}

	verifiedAt := now.Add(time.Minute)
	available, err := coordinator.RecordNodeExit(
		context.Background(), maintenance.Token, netip.MustParseAddr("1.1.1.1"), verifiedAt, verifiedAt.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("RecordNodeExit() after canceled caller error = %v", err)
	}
	if available.EgressRevision != 5 || available.State != NodeStateAvailable {
		t.Fatalf("RecordNodeExit() node = %+v", available)
	}
	if err := coordinator.Release(maintenance.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorConfirmNodeExitAdvancesMaintenanceLease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repository := newCoordinatorRepositoryStub(usableCombinationResources(now, 1, 11, 21))
	coordinator := newTestCoordinator(t, repository)
	component := registerTestComponents(t, coordinator, 1)[0]
	maintenance, err := coordinator.AcquireNodeMaintenance(context.Background(), component, 21)
	if err != nil {
		t.Fatal(err)
	}

	confirmed, err := coordinator.ConfirmNodeExit(
		context.Background(), maintenance.Token, netip.MustParseAddr("8.8.4.4"), now, now.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("ConfirmNodeExit() error = %v", err)
	}
	if confirmed.EgressRevision != 5 || confirmed.State != NodeStateAvailable ||
		confirmed.ExitVerification == nil || confirmed.ExitVerification.VerifiedRevision != 5 {
		t.Fatalf("ConfirmNodeExit() node = %+v", confirmed)
	}
	if _, err := coordinator.OpenNodeProxyCredential(context.Background(), maintenance.Token); err != nil {
		t.Fatalf("OpenNodeProxyCredential() after confirmation error = %v", err)
	}
	if opened := repository.openedNode(); opened.egressRevision != 5 {
		t.Fatalf("OpenNodeProxyCredentialAt() revision = %d", opened.egressRevision)
	}
	if err := coordinator.Release(maintenance.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorNamedMutationsAreGuarded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	resources := usableCombinationResources(now, 1, 11, 21)
	repository := newCoordinatorRepositoryStub(resources)
	coordinator := newTestCoordinator(t, repository)
	component := registerTestComponents(t, coordinator, 1)[0]
	lease, err := coordinator.AcquireCombination(context.Background(), component, 1, TargetRegionDomestic, now)
	if err != nil {
		t.Fatal(err)
	}

	assertOccupied := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrResourceOccupied) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	assertOccupied("DeleteCombination", coordinator.DeleteCombination(context.Background(), 1))
	assertOccupied("DeleteAccount", coordinator.DeleteAccount(context.Background(), 11))
	assertOccupied("DeleteNode", coordinator.DeleteNode(context.Background(), 21))
	_, err = coordinator.ReplaceAccountSession(context.Background(), 11, 3, []byte("replacement"))
	assertOccupied("ReplaceAccountSession", err)
	_, err = coordinator.ReplaceNodeConnection(context.Background(), 21, 4, NodeConnectionInput{
		Kind: NodeKindDirect, Region: NodeRegionDomestic, EgressMode: EgressModeStatic,
	})
	assertOccupied("ReplaceNodeConnection", err)

	if repository.callCount("delete_combination") != 0 || repository.callCount("delete_account") != 0 ||
		repository.callCount("delete_node") != 0 || repository.callCount("replace_account") != 0 ||
		repository.callCount("replace_node") != 0 {
		t.Fatal("guarded mutation reached repository while occupied")
	}
	if err := coordinator.Release(lease.Token); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.DeleteCombination(context.Background(), 1); err != nil {
		t.Fatalf("DeleteCombination(idle) error = %v", err)
	}
	if repository.callCount("delete_combination") != 1 {
		t.Fatal("idle mutation did not reach repository")
	}
}

func TestCoordinatorAcquireDeleteLinearization(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repository := newCoordinatorRepositoryStub(usableCombinationResources(now, 1, 11, 21))
	repository.blockedCombination = 1
	repository.readStarted = make(chan struct{}, 1)
	repository.readContinue = make(chan struct{})
	coordinator := newTestCoordinator(t, repository)
	component := registerTestComponents(t, coordinator, 1)[0]
	leaseResult := make(chan Lease, 1)
	errorResult := make(chan error, 2)
	go func() {
		lease, err := coordinator.AcquireCombination(context.Background(), component, 1, TargetRegionDomestic, now)
		leaseResult <- lease
		errorResult <- err
	}()
	select {
	case <-repository.readStarted:
	case <-time.After(time.Second):
		t.Fatal("AcquireCombination did not enter repository")
	}
	go func() { errorResult <- coordinator.DeleteCombination(context.Background(), 1) }()
	close(repository.readContinue)
	results := []error{<-errorResult, <-errorResult}
	var succeeded, occupied int
	for _, err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrResourceOccupied) {
			occupied++
		} else {
			t.Fatalf("concurrent operation error = %v", err)
		}
	}
	if succeeded != 1 || occupied != 1 {
		t.Fatalf("concurrent results = %#v", results)
	}
	lease := <-leaseResult
	if repository.callCount("delete_combination") != 0 {
		t.Fatal("occupied DeleteCombination reached repository")
	}
	if err := coordinator.Release(lease.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorCredentialOpenRechecksCanceledCallerAndWipes(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"open_account", "open_node"} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
			resources := usableCombinationResources(now, 1, 11, 21)
			resources.Node.Kind = NodeKindProxy
			resources.Node.HasProxyCredential = true
			repository := newCoordinatorRepositoryStub(resources)
			repository.blockedOpen = operation
			repository.openStarted = make(chan struct{}, 1)
			repository.openContinue = make(chan struct{})
			coordinator := newTestCoordinator(t, repository)
			components := registerTestComponents(t, coordinator, 2)
			lease, err := coordinator.AcquireCombination(context.Background(), components[0], 1, TargetRegionDomestic, now)
			if err != nil {
				t.Fatal(err)
			}
			operationContext, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				if operation == "open_account" {
					_, err = coordinator.OpenAccountSession(operationContext, lease.Token)
				} else {
					_, err = coordinator.OpenNodeProxyCredential(operationContext, lease.Token)
				}
				result <- err
			}()
			select {
			case <-repository.openStarted:
			case <-time.After(time.Second):
				t.Fatal("credential open did not block")
			}
			cancel()
			close(repository.openContinue)
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("credential open error = %v", err)
			}
			repository.mu.Lock()
			opened := append([]byte(nil), repository.lastOpened...)
			repository.mu.Unlock()
			for index, value := range opened {
				if value != 0 {
					t.Fatalf("plaintext byte %d was not wiped", index)
				}
			}
			if _, err := coordinator.AcquireCombination(context.Background(), components[1], 1, TargetRegionDomestic, now); !errors.Is(err, ErrResourceOccupied) {
				t.Fatalf("canceled open released occupation: %v", err)
			}
			if err := coordinator.Release(lease.Token); err != nil {
				t.Fatal(err)
			}
			if _, err := coordinator.OpenAccountSession(context.Background(), lease.Token); !errors.Is(err, ErrLeaseNotHeld) {
				t.Fatalf("stale token OpenAccountSession() error = %v", err)
			}
		})
	}
}

func newTestCoordinator(t *testing.T, repository CoordinatorRepository) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(repository)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	return coordinator
}

func registerTestComponents(t *testing.T, coordinator *Coordinator, count int) []ComponentID {
	t.Helper()
	components := make([]ComponentID, count)
	for index := range components {
		component, err := coordinator.RegisterComponent()
		if err != nil {
			t.Fatalf("RegisterComponent() error = %v", err)
		}
		components[index] = component
	}
	return components
}

func usableCombinationResourcesFor(
	now time.Time,
	combinationID CombinationID,
	accountID AccountID,
	nodeID NodeID,
	platform Platform,
) CombinationResources {
	value := usableCombinationResources(now, combinationID, accountID, nodeID)
	value.Combination.Platform = platform
	value.Account.Platform = platform
	return value
}

var _ CoordinatorRepository = (*coordinatorRepositoryStub)(nil)
