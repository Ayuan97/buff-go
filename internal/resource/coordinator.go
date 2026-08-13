package resource

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"sync"
	"time"

	"buff-go/internal/market"
)

var (
	ErrComponentUnavailable = errors.New("resource component is unavailable")
	ErrCombinationNotFound  = errors.New("account-node combination not found")
	ErrNodeNotFound         = errors.New("access node not found")
	ErrResourceOccupied     = errors.New("resource is occupied")
	ErrResourceUnusable     = errors.New("resource is not usable")
	ErrLeaseNotHeld         = errors.New("resource lease is not held")
	ErrCoordinatorIntegrity = errors.New("resource coordinator state is inconsistent")
	ErrSequenceExhausted    = errors.New("resource coordinator sequence is exhausted")
	ErrComponentCanceled    = errors.New("resource component was canceled")
	ErrLeaseReleased        = errors.New("resource lease was released")
)

// CoordinatorRepository provides authoritative resource reads and the guarded
// configuration mutations serialized by Coordinator.
type CoordinatorRepository interface {
	CombinationResources(context.Context, CombinationID) (CombinationResources, bool, error)
	Node(context.Context, NodeID) (AccessNode, bool, error)
	CreateCombination(context.Context, AccountID, NodeID) (AccountNodeCombination, error)
	DeleteCombination(context.Context, CombinationID) error
	DeleteAccount(context.Context, AccountID) error
	DeleteNode(context.Context, NodeID) error
	ReplaceAccountSession(context.Context, AccountID, int64, []byte) (PlatformAccount, error)
	OpenAccountSessionAt(context.Context, AccountID, int64) ([]byte, error)
	RecordAccountSessionCheck(context.Context, AccountID, int64, AccountSessionState, time.Time) (PlatformAccount, error)
	ReplaceNodeConnection(context.Context, NodeID, int64, NodeConnectionInput) (AccessNode, error)
	BeginNodeRevalidation(context.Context, NodeID, int64) (AccessNode, error)
	OpenNodeProxyCredentialAt(context.Context, NodeID, int64, int64) ([]byte, error)
	RecordNodeExit(context.Context, NodeID, int64, netip.Addr, time.Time, time.Time) (AccessNode, error)
	MarkNodeUnavailable(context.Context, NodeID, int64) (AccessNode, error)
	AssignNodeGame(context.Context, NodeID, int64, int64) (AccessNode, error)
	AssignNodeSide(context.Context, NodeID, int64, Platform, market.Side) (AccessNode, error)
}

// ComponentID identifies one runtime cancellation generation. It is valid only
// for the Coordinator that created it.
type ComponentID struct {
	epoch      [16]byte
	generation uint64
	state      *componentState
}

// String returns a credential-free correlation value.
func (id ComponentID) String() string {
	if id.generation == 0 {
		return ""
	}
	return hex.EncodeToString(id.epoch[:]) + fmt.Sprintf("-%016x", id.generation)
}

// LeaseToken identifies one exact occupation generation. Its manager epoch and
// monotonic sequence prevent a stale release from affecting a later lease.
type LeaseToken struct {
	epoch    [16]byte
	sequence uint64
}

// String returns a credential-free correlation value.
func (token LeaseToken) String() string {
	if token.sequence == 0 {
		return ""
	}
	return hex.EncodeToString(token.epoch[:]) + fmt.Sprintf("-%016x", token.sequence)
}

// LeaseKind distinguishes request use from node-only maintenance.
type LeaseKind string

const (
	LeaseKindCombination     LeaseKind = "combination"
	LeaseKindNodeMaintenance LeaseKind = "node_maintenance"
)

// LeaseSnapshot freezes the resource generations observed before occupation.
type LeaseSnapshot struct {
	CombinationID      CombinationID
	Platform           Platform
	AccountID          AccountID
	NodeID             NodeID
	NodeKind           NodeKind
	NodeRegion         NodeRegion
	EgressMode         EgressMode
	ExitAddress        netip.Addr
	ExitVerifiedAt     time.Time
	ExitValidUntil     time.Time
	SessionRevision    int64
	EgressRevision     int64
	AssignmentRevision int64
}

// Lease is one exact in-memory occupation. Snapshot is a safe read copy; code
// that needs an authoritative identity must use CombinationSnapshot. Context
// is canceled by component cancellation and by explicit release.
type Lease struct {
	Token     LeaseToken
	Component ComponentID
	Kind      LeaseKind
	Snapshot  LeaseSnapshot
	ctx       context.Context
	authority *leaseAuthority
}

type leaseAuthority struct {
	mu       sync.RWMutex
	active   bool
	kind     LeaseKind
	snapshot LeaseSnapshot
}

// Context is the execution context owned by this lease.
func (lease Lease) Context() context.Context {
	if lease.ctx == nil {
		return context.Background()
	}
	return lease.ctx
}

// CombinationSnapshot returns the immutable identity captured by Coordinator,
// ignoring any caller mutation of the exported safe read copy.
func (lease Lease) CombinationSnapshot() (LeaseSnapshot, error) {
	if lease.Kind != LeaseKindCombination || lease.Token.String() == "" || lease.Component.String() == "" || lease.ctx == nil || lease.authority == nil {
		return LeaseSnapshot{}, ErrLeaseNotHeld
	}
	snapshot, active := lease.authority.current(LeaseKindCombination)
	if !active || lease.ctx.Err() != nil {
		return LeaseSnapshot{}, ErrLeaseNotHeld
	}
	if snapshot.CombinationID == 0 || snapshot.AccountID == 0 || snapshot.NodeID == 0 {
		return LeaseSnapshot{}, ErrCoordinatorIntegrity
	}
	return snapshot, nil
}

func (authority *leaseAuthority) current(kind LeaseKind) (LeaseSnapshot, bool) {
	if authority == nil {
		return LeaseSnapshot{}, false
	}
	authority.mu.RLock()
	defer authority.mu.RUnlock()
	return authority.snapshot, authority.active && authority.kind == kind
}

func (authority *leaseAuthority) deactivate() {
	if authority == nil {
		return
	}
	authority.mu.Lock()
	authority.active = false
	authority.mu.Unlock()
}

func (authority *leaseAuthority) update(snapshot LeaseSnapshot) {
	if authority == nil {
		return
	}
	authority.mu.Lock()
	authority.snapshot = snapshot
	authority.mu.Unlock()
}

type componentState struct {
	closing bool
	drain   chan struct{}
	leases  map[LeaseToken]struct{}
}

type leaseRecord struct {
	lease  Lease
	cancel context.CancelCauseFunc
}

type contextMutex chan struct{}

func newContextMutex() contextMutex {
	mutex := make(contextMutex, 1)
	mutex <- struct{}{}
	return mutex
}

func (mutex contextMutex) Lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-mutex:
		return nil
	}
}

func (mutex contextMutex) Unlock() {
	mutex <- struct{}{}
}

// nodePlatformKey is the combination occupancy identity: one node may run
// different platforms in parallel, but the same platform remains exclusive.
type nodePlatformKey struct {
	nodeID   NodeID
	platform Platform
}

// Coordinator serializes runtime occupations and guarded configuration
// mutations for the supported single-process architecture.
type Coordinator struct {
	repository CoordinatorRepository
	epoch      [16]byte

	coordMu           contextMutex
	ownerMu           sync.Mutex
	componentSequence uint64
	leaseSequence     uint64
	components        map[ComponentID]*componentState
	leases            map[LeaseToken]leaseRecord
	accounts          map[AccountID]LeaseToken
	nodePlatforms     map[nodePlatformKey]LeaseToken
	nodes             map[NodeID]LeaseToken
	combinations      map[CombinationID]LeaseToken
}

// NewCoordinator creates an empty runtime occupation set with a fresh epoch.
func NewCoordinator(repository CoordinatorRepository) (*Coordinator, error) {
	if repository == nil {
		return nil, fmt.Errorf("nil resource coordinator repository")
	}
	coordinator := &Coordinator{
		repository:    repository,
		coordMu:       newContextMutex(),
		components:    make(map[ComponentID]*componentState),
		leases:        make(map[LeaseToken]leaseRecord),
		accounts:      make(map[AccountID]LeaseToken),
		nodePlatforms: make(map[nodePlatformKey]LeaseToken),
		nodes:         make(map[NodeID]LeaseToken),
		combinations:  make(map[CombinationID]LeaseToken),
	}
	if _, err := rand.Read(coordinator.epoch[:]); err != nil {
		return nil, fmt.Errorf("create resource coordinator epoch: %w", err)
	}
	return coordinator, nil
}

// RegisterComponent starts one independently cancelable runtime generation.
func (coordinator *Coordinator) RegisterComponent() (ComponentID, error) {
	if coordinator == nil {
		return ComponentID{}, fmt.Errorf("nil resource coordinator")
	}
	coordinator.ownerMu.Lock()
	defer coordinator.ownerMu.Unlock()
	if coordinator.componentSequence == math.MaxUint64 {
		return ComponentID{}, ErrSequenceExhausted
	}
	coordinator.componentSequence++
	state := &componentState{leases: make(map[LeaseToken]struct{})}
	id := ComponentID{epoch: coordinator.epoch, generation: coordinator.componentSequence, state: state}
	coordinator.components[id] = state
	return id, nil
}

// AcquireCombination re-reads one authoritative combination and occupies its
// combination, account, and (node, platform) identities. Maintenance that
// holds the whole node still blocks this path.
func (coordinator *Coordinator) AcquireCombination(
	ctx context.Context,
	component ComponentID,
	id CombinationID,
	target TargetRegion,
	now time.Time,
	appID int64,
	side market.Side,
) (Lease, error) {
	if coordinator == nil {
		return Lease{}, fmt.Errorf("nil resource coordinator")
	}
	if ctx == nil {
		return Lease{}, fmt.Errorf("nil acquisition context")
	}
	if err := id.Validate(); err != nil {
		return Lease{}, err
	}
	if err := target.Validate(); err != nil {
		return Lease{}, err
	}
	if now.IsZero() {
		return Lease{}, fmt.Errorf("acquisition time is required")
	}
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}

	if err := coordinator.lockCoord(ctx); err != nil {
		return Lease{}, err
	}
	defer coordinator.coordMu.Unlock()
	coordinator.ownerMu.Lock()
	_, err := coordinator.componentLocked(component)
	coordinator.ownerMu.Unlock()
	if err != nil {
		return Lease{}, err
	}
	resources, found, err := coordinator.repository.CombinationResources(ctx, id)
	if err != nil {
		return Lease{}, fmt.Errorf("read combination resources: %w", err)
	}
	if !found {
		return Lease{}, ErrCombinationNotFound
	}
	if resources.Combination.ID != id {
		return Lease{}, ErrCoordinatorIntegrity
	}
	if err := resources.ValidateForUse(now, target, appID, side); err != nil {
		return Lease{}, fmt.Errorf("%w: %w", ErrResourceUnusable, err)
	}
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	coordinator.ownerMu.Lock()
	defer coordinator.ownerMu.Unlock()
	state, err := coordinator.componentLocked(component)
	if err != nil {
		return Lease{}, err
	}
	if _, occupied := coordinator.combinations[id]; occupied {
		return Lease{}, fmt.Errorf("%w: combination", ErrResourceOccupied)
	}
	if _, occupied := coordinator.accounts[resources.Account.ID]; occupied {
		return Lease{}, fmt.Errorf("%w: account", ErrResourceOccupied)
	}
	if _, occupied := coordinator.nodes[resources.Node.ID]; occupied {
		return Lease{}, fmt.Errorf("%w: node", ErrResourceOccupied)
	}
	platformKey := nodePlatformKey{nodeID: resources.Node.ID, platform: resources.Combination.Platform}
	if _, occupied := coordinator.nodePlatforms[platformKey]; occupied {
		return Lease{}, fmt.Errorf("%w: node", ErrResourceOccupied)
	}

	snapshot := nodeLeaseSnapshot(resources.Node)
	snapshot.CombinationID = id
	snapshot.Platform = resources.Combination.Platform
	snapshot.AccountID = resources.Account.ID
	snapshot.SessionRevision = resources.Account.SessionRevision
	// Credentials are opened separately by exact revision after occupation.
	return coordinator.newLeaseLocked(ctx, state, component, LeaseKindCombination, snapshot)
}

// AcquireNodeMaintenance occupies one node without requiring an account-node
// combination. It is used for operations such as exit revalidation.
func (coordinator *Coordinator) AcquireNodeMaintenance(ctx context.Context, component ComponentID, id NodeID) (Lease, error) {
	if coordinator == nil {
		return Lease{}, fmt.Errorf("nil resource coordinator")
	}
	if ctx == nil {
		return Lease{}, fmt.Errorf("nil acquisition context")
	}
	if err := id.Validate(); err != nil {
		return Lease{}, err
	}
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}

	if err := coordinator.lockCoord(ctx); err != nil {
		return Lease{}, err
	}
	defer coordinator.coordMu.Unlock()
	coordinator.ownerMu.Lock()
	_, err := coordinator.componentLocked(component)
	coordinator.ownerMu.Unlock()
	if err != nil {
		return Lease{}, err
	}
	node, found, err := coordinator.repository.Node(ctx, id)
	if err != nil {
		return Lease{}, fmt.Errorf("read maintenance node: %w", err)
	}
	if !found {
		return Lease{}, ErrNodeNotFound
	}
	if node.ID != id {
		return Lease{}, ErrCoordinatorIntegrity
	}
	if err := node.Validate(); err != nil {
		return Lease{}, fmt.Errorf("maintenance node: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	coordinator.ownerMu.Lock()
	defer coordinator.ownerMu.Unlock()
	state, err := coordinator.componentLocked(component)
	if err != nil {
		return Lease{}, err
	}
	if coordinator.nodeBusy(id) {
		return Lease{}, fmt.Errorf("%w: node", ErrResourceOccupied)
	}

	snapshot := nodeLeaseSnapshot(node)
	return coordinator.newLeaseLocked(ctx, state, component, LeaseKindNodeMaintenance, snapshot)
}

// Release frees only the exact lease token supplied by its owner generation.
// It intentionally does not accept a context, so canceled cleanup still works.
func (coordinator *Coordinator) Release(token LeaseToken) error {
	if coordinator == nil {
		return fmt.Errorf("nil resource coordinator")
	}
	coordinator.ownerMu.Lock()
	record, found := coordinator.leases[token]
	if !found || token.epoch != coordinator.epoch || token.sequence == 0 {
		coordinator.ownerMu.Unlock()
		return ErrLeaseNotHeld
	}
	component, found := coordinator.components[record.lease.Component]
	if !found || !ownsTokenSet(component.leases, token) || !coordinator.leaseOwnsKeysLocked(record.lease, token) {
		coordinator.ownerMu.Unlock()
		return ErrCoordinatorIntegrity
	}
	record.lease.authority.deactivate()

	delete(coordinator.leases, token)
	delete(component.leases, token)
	coordinator.clearLeaseKeysLocked(record.lease)
	if component.closing && len(component.leases) == 0 {
		close(component.drain)
		delete(coordinator.components, record.lease.Component)
	}
	coordinator.ownerMu.Unlock()
	record.cancel(ErrLeaseReleased)
	return nil
}

// CancelComponent prevents new acquisitions, cancels all lease contexts, and
// waits for execution owners to release. A wait timeout never force-releases.
func (coordinator *Coordinator) CancelComponent(ctx context.Context, id ComponentID) error {
	if coordinator == nil {
		return fmt.Errorf("nil resource coordinator")
	}
	if ctx == nil {
		return fmt.Errorf("nil cancellation context")
	}

	coordinator.ownerMu.Lock()
	component, err := coordinator.componentForCancelLocked(id)
	if err != nil {
		coordinator.ownerMu.Unlock()
		return err
	}
	var cancels []context.CancelCauseFunc
	if !component.closing {
		component.closing = true
		component.drain = make(chan struct{})
		cancels = make([]context.CancelCauseFunc, 0, len(component.leases))
		for token := range component.leases {
			record := coordinator.leases[token]
			record.lease.authority.deactivate()
			cancels = append(cancels, record.cancel)
		}
		if len(component.leases) == 0 {
			close(component.drain)
			delete(coordinator.components, id)
		}
	}
	drain := component.drain
	coordinator.ownerMu.Unlock()

	for _, cancel := range cancels {
		cancel(ErrComponentCanceled)
	}
	select {
	case <-drain:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CreateCombination serializes a persistent binding with acquisitions. It does
// not require idle resources because creating a binding cannot alter a lease.
func (coordinator *Coordinator) CreateCombination(ctx context.Context, accountID AccountID, nodeID NodeID) (AccountNodeCombination, error) {
	if err := accountID.Validate(); err != nil {
		return AccountNodeCombination{}, err
	}
	if err := nodeID.Validate(); err != nil {
		return AccountNodeCombination{}, err
	}
	if err := coordinator.lockCoord(ctx); err != nil {
		return AccountNodeCombination{}, err
	}
	defer coordinator.coordMu.Unlock()
	return coordinator.repository.CreateCombination(ctx, accountID, nodeID)
}

// DeleteCombination rejects deletion while the exact binding is occupied.
func (coordinator *Coordinator) DeleteCombination(ctx context.Context, id CombinationID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	return coordinator.withIdle(ctx, "combination", func() bool {
		_, occupied := coordinator.combinations[id]
		return occupied
	}, func() error {
		return coordinator.repository.DeleteCombination(ctx, id)
	})
}

// DeleteAccount rejects deletion while any lease owns the account.
func (coordinator *Coordinator) DeleteAccount(ctx context.Context, id AccountID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	return coordinator.withIdle(ctx, "account", func() bool {
		_, occupied := coordinator.accounts[id]
		return occupied
	}, func() error {
		return coordinator.repository.DeleteAccount(ctx, id)
	})
}

// DeleteNode rejects deletion while a request or maintenance lease owns it.
func (coordinator *Coordinator) DeleteNode(ctx context.Context, id NodeID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	return coordinator.withIdle(ctx, "node", func() bool {
		return coordinator.nodeBusy(id)
	}, func() error {
		return coordinator.repository.DeleteNode(ctx, id)
	})
}

// ReplaceAccountSession rejects credential replacement while the account is in use.
func (coordinator *Coordinator) ReplaceAccountSession(ctx context.Context, id AccountID, expectedRevision int64, session []byte) (PlatformAccount, error) {
	if err := validateExpectedRevision(id.Validate(), expectedRevision); err != nil {
		return PlatformAccount{}, err
	}
	var account PlatformAccount
	err := coordinator.withIdle(ctx, "account", func() bool {
		_, occupied := coordinator.accounts[id]
		return occupied
	}, func() error {
		var err error
		account, err = coordinator.repository.ReplaceAccountSession(ctx, id, expectedRevision, session)
		return err
	})
	return account, err
}

// ReplaceNodeConnection rejects connection replacement while the node is in use.
func (coordinator *Coordinator) ReplaceNodeConnection(ctx context.Context, id NodeID, expectedRevision int64, input NodeConnectionInput) (AccessNode, error) {
	if err := validateExpectedRevision(id.Validate(), expectedRevision); err != nil {
		return AccessNode{}, err
	}
	var node AccessNode
	err := coordinator.withIdle(ctx, "node", func() bool {
		return coordinator.nodeBusy(id)
	}, func() error {
		var err error
		node, err = coordinator.repository.ReplaceNodeConnection(ctx, id, expectedRevision, input)
		return err
	})
	return node, err
}

// BeginNodeRevalidation advances the node owned by an exact maintenance lease
// and moves that same lease to the returned egress revision.
func (coordinator *Coordinator) BeginNodeRevalidation(ctx context.Context, token LeaseToken, expectedRevision int64) (AccessNode, error) {
	if expectedRevision < 1 {
		return AccessNode{}, fmt.Errorf("expected revision must be at least 1")
	}
	if expectedRevision == math.MaxInt64 {
		return AccessNode{}, fmt.Errorf("egress revision is exhausted")
	}
	if err := coordinator.lockCoord(ctx); err != nil {
		return AccessNode{}, err
	}
	defer coordinator.coordMu.Unlock()
	record, operationContext, stop, err := coordinator.leaseOperation(ctx, token, LeaseKindNodeMaintenance)
	if err != nil {
		return AccessNode{}, err
	}
	defer stop()
	snapshot := record.lease.Snapshot
	if snapshot.EgressRevision != expectedRevision {
		return AccessNode{}, fmt.Errorf("maintenance lease revision does not match expected revision")
	}
	node, err := coordinator.repository.BeginNodeRevalidation(operationContext, snapshot.NodeID, expectedRevision)
	if err != nil {
		if cause := context.Cause(operationContext); cause != nil {
			return AccessNode{}, cause
		}
		return AccessNode{}, err
	}
	if node.ID != snapshot.NodeID || node.EgressRevision != expectedRevision+1 || node.State != NodeStateValidating {
		return AccessNode{}, ErrCoordinatorIntegrity
	}
	if err := coordinator.updateLeaseNodeSnapshot(token, LeaseKindNodeMaintenance, snapshot, node, true); err != nil {
		return AccessNode{}, err
	}
	if cause := context.Cause(operationContext); cause != nil {
		return node, cause
	}
	return node, nil
}

// OpenAccountSession opens a session only for an exact combination lease.
func (coordinator *Coordinator) OpenAccountSession(ctx context.Context, token LeaseToken) ([]byte, error) {
	record, operationContext, stop, err := coordinator.leaseOperation(ctx, token, LeaseKindCombination)
	if err != nil {
		return nil, err
	}
	defer stop()
	snapshot := record.lease.Snapshot
	plaintext, err := coordinator.repository.OpenAccountSessionAt(operationContext, snapshot.AccountID, snapshot.SessionRevision)
	if err != nil {
		wipeBytes(plaintext)
		if cause := context.Cause(operationContext); cause != nil {
			return nil, cause
		}
		return nil, err
	}
	if cause := context.Cause(operationContext); cause != nil {
		wipeBytes(plaintext)
		return nil, cause
	}
	if err := coordinator.confirmLease(token, LeaseKindCombination); err != nil {
		wipeBytes(plaintext)
		return nil, err
	}
	return plaintext, nil
}

// RecordAccountSessionCheck records a valid or invalid check for the account
// occupied by an exact combination lease.
func (coordinator *Coordinator) RecordAccountSessionCheck(ctx context.Context, token LeaseToken, state AccountSessionState, checkedAt time.Time) (PlatformAccount, error) {
	record, operationContext, stop, err := coordinator.leaseOperation(ctx, token, LeaseKindCombination)
	if err != nil {
		return PlatformAccount{}, err
	}
	defer stop()
	snapshot := record.lease.Snapshot
	account, err := coordinator.repository.RecordAccountSessionCheck(operationContext, snapshot.AccountID, snapshot.SessionRevision, state, checkedAt)
	if err != nil {
		if cause := context.Cause(operationContext); cause != nil {
			return PlatformAccount{}, cause
		}
		return PlatformAccount{}, err
	}
	if cause := context.Cause(operationContext); cause != nil {
		return account, cause
	}
	return account, nil
}

// OpenNodeProxyCredential opens connection material only for an exact current
// combination or node-maintenance lease generation.
func (coordinator *Coordinator) OpenNodeProxyCredential(ctx context.Context, token LeaseToken) ([]byte, error) {
	record, operationContext, stop, err := coordinator.leaseOperation(
		ctx,
		token,
		LeaseKindCombination,
		LeaseKindNodeMaintenance,
	)
	if err != nil {
		return nil, err
	}
	defer stop()
	snapshot := record.lease.Snapshot
	plaintext, err := coordinator.repository.OpenNodeProxyCredentialAt(
		operationContext,
		snapshot.NodeID,
		snapshot.EgressRevision,
		snapshot.AssignmentRevision,
	)
	if err != nil {
		wipeBytes(plaintext)
		if cause := context.Cause(operationContext); cause != nil {
			return nil, cause
		}
		return nil, err
	}
	if cause := context.Cause(operationContext); cause != nil {
		wipeBytes(plaintext)
		return nil, cause
	}
	if err := coordinator.confirmLease(token, record.lease.Kind); err != nil {
		wipeBytes(plaintext)
		return nil, err
	}
	return plaintext, nil
}

// RecordNodeExit accepts exit evidence only from the exact maintenance lease.
func (coordinator *Coordinator) RecordNodeExit(ctx context.Context, token LeaseToken, address netip.Addr, verifiedAt, validUntil time.Time) (AccessNode, error) {
	if err := coordinator.lockCoord(ctx); err != nil {
		return AccessNode{}, err
	}
	defer coordinator.coordMu.Unlock()
	record, operationContext, stop, err := coordinator.leaseOperation(ctx, token, LeaseKindNodeMaintenance)
	if err != nil {
		return AccessNode{}, err
	}
	defer stop()
	snapshot := record.lease.Snapshot
	node, err := coordinator.repository.RecordNodeExit(operationContext, snapshot.NodeID, snapshot.EgressRevision, address, verifiedAt, validUntil)
	if err != nil {
		if cause := context.Cause(operationContext); cause != nil {
			return AccessNode{}, cause
		}
		return AccessNode{}, err
	}
	if err := coordinator.updateLeaseNodeSnapshot(token, LeaseKindNodeMaintenance, snapshot, node, false); err != nil {
		return AccessNode{}, err
	}
	if cause := context.Cause(operationContext); cause != nil {
		return node, cause
	}
	return node, nil
}

// MarkNodeUnavailable records probe failure only for the exact maintenance lease.
func (coordinator *Coordinator) MarkNodeUnavailable(ctx context.Context, token LeaseToken) (AccessNode, error) {
	if err := coordinator.lockCoord(ctx); err != nil {
		return AccessNode{}, err
	}
	defer coordinator.coordMu.Unlock()
	record, operationContext, stop, err := coordinator.leaseOperation(ctx, token, LeaseKindNodeMaintenance)
	if err != nil {
		return AccessNode{}, err
	}
	defer stop()
	snapshot := record.lease.Snapshot
	node, err := coordinator.repository.MarkNodeUnavailable(operationContext, snapshot.NodeID, snapshot.EgressRevision)
	if err != nil {
		if cause := context.Cause(operationContext); cause != nil {
			return AccessNode{}, cause
		}
		return AccessNode{}, err
	}
	if err := coordinator.updateLeaseNodeSnapshot(token, LeaseKindNodeMaintenance, snapshot, node, false); err != nil {
		return AccessNode{}, err
	}
	if cause := context.Cause(operationContext); cause != nil {
		return node, cause
	}
	return node, nil
}

// AssignNodeGame applies a game CAS while the node is idle. appID 0 clears it.
func (coordinator *Coordinator) AssignNodeGame(ctx context.Context, id NodeID, expectedRevision int64, appID int64) (AccessNode, error) {
	if err := validateExpectedRevision(id.Validate(), expectedRevision); err != nil {
		return AccessNode{}, err
	}
	if appID < 0 {
		return AccessNode{}, fmt.Errorf("appid must be non-negative")
	}
	var node AccessNode
	err := coordinator.withIdle(ctx, "node", func() bool {
		return coordinator.nodeBusy(id)
	}, func() error {
		var err error
		node, err = coordinator.repository.AssignNodeGame(ctx, id, expectedRevision, appID)
		return err
	})
	return node, err
}

// AssignNodeSide applies a direction CAS while the node is idle. Empty side
// deletes that platform row.
func (coordinator *Coordinator) AssignNodeSide(ctx context.Context, id NodeID, expectedRevision int64, platform Platform, side market.Side) (AccessNode, error) {
	if err := validateNodeSideInput(id, expectedRevision, platform, side); err != nil {
		return AccessNode{}, err
	}
	var node AccessNode
	err := coordinator.withIdle(ctx, "node", func() bool {
		return coordinator.nodeBusy(id)
	}, func() error {
		var err error
		node, err = coordinator.repository.AssignNodeSide(ctx, id, expectedRevision, platform, side)
		return err
	})
	return node, err
}

func (coordinator *Coordinator) withIdle(ctx context.Context, kind string, occupied func() bool, action func() error) error {
	if err := coordinator.lockCoord(ctx); err != nil {
		return err
	}
	defer coordinator.coordMu.Unlock()
	coordinator.ownerMu.Lock()
	busy := occupied()
	coordinator.ownerMu.Unlock()
	if busy {
		return fmt.Errorf("%w: %s", ErrResourceOccupied, kind)
	}
	return action()
}

func (coordinator *Coordinator) lockCoord(ctx context.Context) error {
	if coordinator == nil {
		return fmt.Errorf("nil resource coordinator")
	}
	if ctx == nil {
		return fmt.Errorf("nil coordinator context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := coordinator.coordMu.Lock(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		coordinator.coordMu.Unlock()
		return err
	}
	return nil
}

func validateExpectedRevision(identityErr error, revision int64) error {
	if identityErr != nil {
		return identityErr
	}
	if revision < 1 {
		return fmt.Errorf("expected revision must be at least 1")
	}
	return nil
}

func validateNodeSideInput(id NodeID, revision int64, platform Platform, side market.Side) error {
	if err := validateExpectedRevision(id.Validate(), revision); err != nil {
		return err
	}
	if err := platform.Validate(); err != nil {
		return err
	}
	if side == "" {
		return nil
	}
	if side != market.SideBid && side != market.SideAsk {
		return fmt.Errorf("invalid assignment side %q", side)
	}
	return nil
}

func (coordinator *Coordinator) leaseOperation(ctx context.Context, token LeaseToken, allowedKinds ...LeaseKind) (leaseRecord, context.Context, func(), error) {
	if coordinator == nil {
		return leaseRecord{}, nil, nil, fmt.Errorf("nil resource coordinator")
	}
	if ctx == nil {
		return leaseRecord{}, nil, nil, fmt.Errorf("nil lease operation context")
	}
	if err := ctx.Err(); err != nil {
		return leaseRecord{}, nil, nil, err
	}
	coordinator.ownerMu.Lock()
	record, err := coordinator.leaseRecordLocked(token)
	coordinator.ownerMu.Unlock()
	if err != nil {
		return leaseRecord{}, nil, nil, err
	}
	allowed := false
	for _, kind := range allowedKinds {
		if record.lease.Kind == kind {
			allowed = true
			break
		}
	}
	if !allowed {
		return leaseRecord{}, nil, nil, ErrLeaseNotHeld
	}
	if _, active := record.lease.authority.current(record.lease.Kind); !active {
		return leaseRecord{}, nil, nil, ErrLeaseNotHeld
	}
	if cause := context.Cause(record.lease.Context()); cause != nil {
		return leaseRecord{}, nil, nil, cause
	}
	operationContext, cancel := context.WithCancelCause(ctx)
	stopAfter := context.AfterFunc(record.lease.Context(), func() {
		cancel(context.Cause(record.lease.Context()))
	})
	stop := func() {
		stopAfter()
		cancel(nil)
	}
	return record, operationContext, stop, nil
}

func (coordinator *Coordinator) confirmLease(token LeaseToken, expectedKind LeaseKind) error {
	coordinator.ownerMu.Lock()
	defer coordinator.ownerMu.Unlock()
	record, err := coordinator.leaseRecordLocked(token)
	if err != nil {
		return err
	}
	if record.lease.Kind != expectedKind {
		return ErrLeaseNotHeld
	}
	if _, active := record.lease.authority.current(expectedKind); !active {
		return ErrLeaseNotHeld
	}
	if cause := context.Cause(record.lease.Context()); cause != nil {
		return cause
	}
	return nil
}

func (coordinator *Coordinator) updateLeaseNodeSnapshot(token LeaseToken, expectedKind LeaseKind, previous LeaseSnapshot, node AccessNode, revisionAdvanced bool) error {
	if err := node.Validate(); err != nil {
		return fmt.Errorf("maintenance result: %w", err)
	}
	wantEgressRevision := previous.EgressRevision
	if revisionAdvanced {
		if wantEgressRevision == math.MaxInt64 {
			return ErrCoordinatorIntegrity
		}
		wantEgressRevision++
	}
	if node.ID != previous.NodeID ||
		node.EgressRevision != wantEgressRevision ||
		node.AssignmentRevision != previous.AssignmentRevision {
		return ErrCoordinatorIntegrity
	}
	coordinator.ownerMu.Lock()
	defer coordinator.ownerMu.Unlock()
	record, err := coordinator.leaseRecordLocked(token)
	if err != nil {
		return err
	}
	if record.lease.Kind != expectedKind ||
		record.lease.Snapshot.NodeID != previous.NodeID ||
		record.lease.Snapshot.EgressRevision != previous.EgressRevision ||
		record.lease.Snapshot.AssignmentRevision != previous.AssignmentRevision {
		return ErrCoordinatorIntegrity
	}
	if _, active := record.lease.authority.current(expectedKind); !active {
		return ErrLeaseNotHeld
	}
	if cause := context.Cause(record.lease.Context()); cause != nil {
		return cause
	}
	updated := nodeLeaseSnapshot(node)
	updated.CombinationID = previous.CombinationID
	updated.Platform = previous.Platform
	updated.AccountID = previous.AccountID
	updated.SessionRevision = previous.SessionRevision
	record.lease.Snapshot = updated
	record.lease.authority.update(updated)
	coordinator.leases[token] = record
	return nil
}

func (coordinator *Coordinator) leaseRecordLocked(token LeaseToken) (leaseRecord, error) {
	record, found := coordinator.leases[token]
	if !found || token.epoch != coordinator.epoch || token.sequence == 0 {
		return leaseRecord{}, ErrLeaseNotHeld
	}
	component, found := coordinator.components[record.lease.Component]
	if !found || component != record.lease.Component.state || !ownsTokenSet(component.leases, token) ||
		record.lease.Snapshot.NodeID == 0 || !coordinator.leaseOwnsKeysLocked(record.lease, token) {
		return leaseRecord{}, ErrCoordinatorIntegrity
	}
	switch record.lease.Kind {
	case LeaseKindCombination:
		if record.lease.Snapshot.AccountID == 0 || record.lease.Snapshot.CombinationID == 0 || record.lease.Snapshot.Platform == "" {
			return leaseRecord{}, ErrCoordinatorIntegrity
		}
	case LeaseKindNodeMaintenance:
		if record.lease.Snapshot.AccountID != 0 || record.lease.Snapshot.CombinationID != 0 {
			return leaseRecord{}, ErrCoordinatorIntegrity
		}
	default:
		return leaseRecord{}, ErrCoordinatorIntegrity
	}
	return record, nil
}

func (coordinator *Coordinator) componentLocked(id ComponentID) (*componentState, error) {
	if id.epoch != coordinator.epoch || id.generation == 0 || id.state == nil {
		return nil, ErrComponentUnavailable
	}
	component, found := coordinator.components[id]
	if !found || component != id.state || component.closing {
		return nil, ErrComponentUnavailable
	}
	return component, nil
}

func (coordinator *Coordinator) componentForCancelLocked(id ComponentID) (*componentState, error) {
	if id.epoch != coordinator.epoch || id.generation == 0 || id.state == nil {
		return nil, ErrComponentUnavailable
	}
	component, found := coordinator.components[id]
	if found && component != id.state {
		return nil, ErrCoordinatorIntegrity
	}
	if !found && !id.state.closing {
		return nil, ErrComponentUnavailable
	}
	return id.state, nil
}

func (coordinator *Coordinator) newLeaseLocked(parent context.Context, state *componentState, component ComponentID, kind LeaseKind, snapshot LeaseSnapshot) (Lease, error) {
	if coordinator.leaseSequence == math.MaxUint64 {
		return Lease{}, ErrSequenceExhausted
	}
	coordinator.leaseSequence++
	token := LeaseToken{epoch: coordinator.epoch, sequence: coordinator.leaseSequence}
	leaseContext, cancel := context.WithCancelCause(parent)
	lease := Lease{
		Token: token, Component: component, Kind: kind, Snapshot: snapshot, ctx: leaseContext,
		authority: &leaseAuthority{active: true, kind: kind, snapshot: snapshot},
	}
	coordinator.leases[token] = leaseRecord{lease: lease, cancel: cancel}
	state.leases[token] = struct{}{}
	switch kind {
	case LeaseKindCombination:
		coordinator.nodePlatforms[nodePlatformKey{nodeID: snapshot.NodeID, platform: snapshot.Platform}] = token
		coordinator.accounts[snapshot.AccountID] = token
		coordinator.combinations[snapshot.CombinationID] = token
	case LeaseKindNodeMaintenance:
		coordinator.nodes[snapshot.NodeID] = token
	}
	return lease, nil
}

func (coordinator *Coordinator) nodeBusy(id NodeID) bool {
	if _, occupied := coordinator.nodes[id]; occupied {
		return true
	}
	for key := range coordinator.nodePlatforms {
		if key.nodeID == id {
			return true
		}
	}
	return false
}

func (coordinator *Coordinator) leaseOwnsKeysLocked(lease Lease, token LeaseToken) bool {
	switch lease.Kind {
	case LeaseKindCombination:
		return ownsToken(coordinator.nodePlatforms, nodePlatformKey{nodeID: lease.Snapshot.NodeID, platform: lease.Snapshot.Platform}, token) &&
			ownsToken(coordinator.accounts, lease.Snapshot.AccountID, token) &&
			ownsToken(coordinator.combinations, lease.Snapshot.CombinationID, token)
	case LeaseKindNodeMaintenance:
		return ownsToken(coordinator.nodes, lease.Snapshot.NodeID, token)
	default:
		return false
	}
}

func (coordinator *Coordinator) clearLeaseKeysLocked(lease Lease) {
	switch lease.Kind {
	case LeaseKindCombination:
		delete(coordinator.nodePlatforms, nodePlatformKey{nodeID: lease.Snapshot.NodeID, platform: lease.Snapshot.Platform})
		delete(coordinator.accounts, lease.Snapshot.AccountID)
		delete(coordinator.combinations, lease.Snapshot.CombinationID)
	case LeaseKindNodeMaintenance:
		delete(coordinator.nodes, lease.Snapshot.NodeID)
	}
}

func ownsToken[K comparable](owners map[K]LeaseToken, key K, token LeaseToken) bool {
	owned, found := owners[key]
	return found && owned == token
}

func ownsTokenSet(owners map[LeaseToken]struct{}, token LeaseToken) bool {
	_, found := owners[token]
	return found
}

func nodeLeaseSnapshot(node AccessNode) LeaseSnapshot {
	snapshot := LeaseSnapshot{
		NodeID:             node.ID,
		NodeKind:           node.Kind,
		NodeRegion:         node.Region,
		EgressMode:         node.EgressMode,
		EgressRevision:     node.EgressRevision,
		AssignmentRevision: node.AssignmentRevision,
	}
	if node.ExitVerification != nil {
		snapshot.ExitAddress = node.ExitVerification.Address
		snapshot.ExitVerifiedAt = node.ExitVerification.VerifiedAt
		snapshot.ExitValidUntil = node.ExitVerification.ValidUntil
	}
	return snapshot
}

func wipeBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
