package app

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

// accountControl is the occupancy-guarded account API facade.
type accountControl struct {
	store       *postgres.Store
	coordinator *resource.Coordinator
}

func (c accountControl) ListAccounts(ctx context.Context) ([]resource.PlatformAccount, error) {
	return c.store.ListAccounts(ctx)
}

func (c accountControl) Account(ctx context.Context, id resource.AccountID) (resource.PlatformAccount, bool, error) {
	return c.store.Account(ctx, id)
}

func (c accountControl) CreateAccount(ctx context.Context, platform resource.Platform, alias string, session []byte) (resource.PlatformAccount, error) {
	return c.store.CreateAccount(ctx, platform, alias, session)
}

func (c accountControl) RenameAccount(ctx context.Context, id resource.AccountID, alias string) (resource.PlatformAccount, error) {
	return c.store.RenameAccount(ctx, id, alias)
}

func (c accountControl) ReplaceAccountSession(ctx context.Context, id resource.AccountID, expectedRevision int64, session []byte) (resource.PlatformAccount, error) {
	return c.coordinator.ReplaceAccountSession(ctx, id, expectedRevision, session)
}

func (c accountControl) DeleteAccount(ctx context.Context, id resource.AccountID) error {
	return c.coordinator.DeleteAccount(ctx, id)
}

// nodeControl is the occupancy-guarded node API facade.
type nodeControl struct {
	store       *postgres.Store
	coordinator *resource.Coordinator
}

func (c nodeControl) ListNodes(ctx context.Context) ([]resource.AccessNode, error) {
	return c.store.ListNodes(ctx)
}

func (c nodeControl) Node(ctx context.Context, id resource.NodeID) (resource.AccessNode, bool, error) {
	return c.store.Node(ctx, id)
}

func (c nodeControl) CreateNode(ctx context.Context, name string, input resource.NodeConnectionInput) (resource.AccessNode, error) {
	return c.store.CreateNode(ctx, name, input)
}

func (c nodeControl) RenameNode(ctx context.Context, id resource.NodeID, name string) (resource.AccessNode, error) {
	return c.store.RenameNode(ctx, id, name)
}

func (c nodeControl) ReplaceNodeConnection(ctx context.Context, id resource.NodeID, expectedRevision int64, input resource.NodeConnectionInput) (resource.AccessNode, error) {
	return c.coordinator.ReplaceNodeConnection(ctx, id, expectedRevision, input)
}

func (c nodeControl) DeleteNode(ctx context.Context, id resource.NodeID) error {
	return c.coordinator.DeleteNode(ctx, id)
}

func (c nodeControl) RecordNodeExit(ctx context.Context, id resource.NodeID, expectedEgressRevision int64, address netip.Addr, validUntil time.Time) (resource.AccessNode, error) {
	component, err := c.coordinator.RegisterComponent()
	if err != nil {
		return resource.AccessNode{}, err
	}
	defer func() { _ = c.coordinator.CancelComponent(context.Background(), component) }()

	lease, err := c.coordinator.AcquireNodeMaintenance(ctx, component, id)
	if err != nil {
		return resource.AccessNode{}, err
	}
	defer func() { _ = c.coordinator.Release(lease.Token) }()

	if lease.Snapshot.EgressRevision != expectedEgressRevision {
		return resource.AccessNode{}, postgres.ErrResourceRevisionConflict
	}
	return c.coordinator.ConfirmNodeExit(ctx, lease.Token, address, time.Now().UTC(), validUntil)
}

// combinationControl is the occupancy-guarded combination API facade.
type combinationControl struct {
	store       *postgres.Store
	coordinator *resource.Coordinator
}

func (c combinationControl) ListCombinations(ctx context.Context) ([]resource.AccountNodeCombination, error) {
	return c.store.ListCombinations(ctx)
}

func (c combinationControl) Combination(ctx context.Context, id resource.CombinationID) (resource.AccountNodeCombination, bool, error) {
	return c.store.Combination(ctx, id)
}

func (c combinationControl) CreateCombination(ctx context.Context, accountID resource.AccountID, nodeID resource.NodeID) (resource.AccountNodeCombination, error) {
	return c.coordinator.CreateCombination(ctx, accountID, nodeID)
}

func (c combinationControl) DeleteCombination(ctx context.Context, id resource.CombinationID) error {
	return c.coordinator.DeleteCombination(ctx, id)
}

type collectionControl struct {
	store   *postgres.Store
	runtime collection.WorkerRuntimeReader
}

func (c collectionControl) ListTargets(ctx context.Context) ([]collection.Target, error) {
	return c.store.Targets(ctx)
}

func (c collectionControl) Target(ctx context.Context, id collection.TargetID) (collection.Target, bool, error) {
	return c.store.Target(ctx, id)
}

func (c collectionControl) CreateSummaryTarget(ctx context.Context, platform collection.Platform, appID int64, side market.Side, desired collection.DesiredState) (collection.Target, error) {
	return c.store.CreateSummaryTarget(ctx, platform, appID, side, desired)
}

func (c collectionControl) SetTargetDesired(ctx context.Context, id collection.TargetID, expected collection.Revision, desired collection.DesiredState) (collection.Target, error) {
	return c.store.SetTargetDesired(ctx, id, expected, desired)
}

func (c collectionControl) SetTargetSortOrder(ctx context.Context, id collection.TargetID, expected collection.Revision, order collection.SortOrder) (collection.Target, error) {
	return c.store.SetTargetSortOrder(ctx, id, expected, order)
}

func (c collectionControl) SetTargetPriceRange(ctx context.Context, id collection.TargetID, expected collection.Revision, bounds collection.PriceRange) (collection.Target, error) {
	return c.store.SetTargetPriceRange(ctx, id, expected, bounds)
}

func (c collectionControl) SetTargetSteamFacets(ctx context.Context, id collection.TargetID, expected collection.Revision, facets collection.SteamFacets) (collection.Target, error) {
	return c.store.SetTargetSteamFacets(ctx, id, expected, facets)
}

func (c collectionControl) DeleteTarget(ctx context.Context, id collection.TargetID) error {
	return c.store.DeleteTarget(ctx, id)
}

func (c collectionControl) ListWorkers(ctx context.Context) ([]collection.WorkerSnapshot, error) {
	workers, err := c.store.ListWorkers(ctx)
	if err != nil || c.runtime == nil {
		return workers, err
	}
	return mergeWorkerRuntime(workers, c.runtime.WorkerRuntime()), nil
}

func mergeWorkerRuntime(
	workers []collection.WorkerSnapshot,
	runtime collection.WorkerRuntimeSnapshot,
) []collection.WorkerSnapshot {
	byCombination := make(map[resource.CombinationID]int, len(workers))
	seenWaits := make([]map[collection.WorkerWait]struct{}, len(workers))
	for index := range workers {
		byCombination[workers[index].Combination.ID] = index
		seenWaits[index] = make(map[collection.WorkerWait]struct{}, len(workers[index].ActiveWaits))
		unique := workers[index].ActiveWaits[:0]
		for _, wait := range workers[index].ActiveWaits {
			if _, found := seenWaits[index][wait]; found {
				continue
			}
			seenWaits[index][wait] = struct{}{}
			unique = append(unique, wait)
		}
		workers[index].ActiveWaits = unique
	}
	for _, active := range runtime.Claims {
		index, found := byCombination[active.CombinationID]
		if !found {
			continue
		}
		claim := workers[index].Claim
		if claim == nil || claim.TargetID != active.TargetID {
			claim = &collection.WorkerClaim{
				TargetID: active.TargetID, AppID: active.AppID,
				Side: active.Side, Platform: active.Platform,
			}
			workers[index].Claim = claim
		}
		claim.TaskID = active.TaskID
		claim.Kind = active.Kind
		claim.Endpoint = active.Endpoint
		claim.ClaimedAt = active.ClaimedAt
		claim.Active = true
		workers[index].Idle = false
	}
	for _, wait := range runtime.Waits {
		for index := range workers {
			worker := workers[index]
			matches := false
			switch wait.Scope {
			case collection.WorkerWaitScopeRate:
				matches = collection.Platform(worker.Combination.Platform) == wait.Platform &&
					worker.Combination.AccountID == wait.AccountID && worker.ExitAddress == wait.ExitAddress
			case collection.WorkerWaitScopeNode:
				matches = collection.Platform(worker.Combination.Platform) == wait.Platform &&
					worker.Combination.NodeID == wait.NodeID
			case collection.WorkerWaitScopeCombination:
				matches = worker.Combination.ID == wait.CombinationID
			}
			if matches {
				if _, found := seenWaits[index][wait]; found {
					continue
				}
				seenWaits[index][wait] = struct{}{}
				workers[index].ActiveWaits = append(workers[index].ActiveWaits, wait)
			}
		}
	}
	for index := range workers {
		sort.Slice(workers[index].ActiveWaits, func(left, right int) bool {
			return workerWaitLess(workers[index].ActiveWaits[left], workers[index].ActiveWaits[right])
		})
	}
	return workers
}

func workerWaitLess(left, right collection.WorkerWait) bool {
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

type marketControl struct {
	store *postgres.Store
}

func (c marketControl) ListQuotes(ctx context.Context, filter market.QuoteFilter) (market.QuoteResult, error) {
	return c.store.ListMarketQuotes(ctx, filter)
}

func (c marketControl) QuoteFacets(ctx context.Context, appid int64) (market.QuoteFacets, error) {
	return c.store.QuoteFacets(ctx, appid)
}

func (c marketControl) ListPriceTicks(ctx context.Context, filter market.PriceTickFilter) ([]market.PriceTick, error) {
	return c.store.ListPriceTicks(ctx, filter)
}

type providerControl struct {
	store *postgres.Store
}

func (c providerControl) ListWatermarks(ctx context.Context) ([]resource.RegionWatermark, error) {
	return c.store.ListWatermarks(ctx)
}

func (c providerControl) SetWatermark(ctx context.Context, region resource.NodeRegion, expectedRevision int64, minUsable int) (resource.RegionWatermark, error) {
	return c.store.SetWatermark(ctx, region, expectedRevision, minUsable)
}

func (c providerControl) ListProviders(ctx context.Context) ([]resource.ProxyProvider, error) {
	return c.store.ListProviders(ctx)
}

func (c providerControl) CreateProvider(ctx context.Context, name string, enabled bool, priority int, regions []resource.NodeRegion, credential []byte) (resource.ProxyProvider, error) {
	return c.store.CreateProvider(ctx, name, enabled, priority, regions, credential)
}

func (c providerControl) UpdateProvider(ctx context.Context, id resource.ProviderID, expectedRevision int64, name string, enabled bool, priority int, regions []resource.NodeRegion) (resource.ProxyProvider, error) {
	return c.store.UpdateProvider(ctx, id, expectedRevision, name, enabled, priority, regions)
}

func (c providerControl) ReplaceProviderCredential(ctx context.Context, id resource.ProviderID, expectedRevision int64, credential []byte) (resource.ProxyProvider, error) {
	return c.store.ReplaceProviderCredential(ctx, id, expectedRevision, credential)
}

func (c providerControl) DeleteProvider(ctx context.Context, id resource.ProviderID) error {
	return c.store.DeleteProvider(ctx, id)
}

func newControlServices(store *postgres.Store, coordinator *resource.Coordinator) (accountControl, nodeControl, combinationControl, collectionControl, marketControl, error) {
	if store == nil || coordinator == nil {
		return accountControl{}, nodeControl{}, combinationControl{}, collectionControl{}, marketControl{}, fmt.Errorf("control services require store and coordinator")
	}
	return accountControl{store: store, coordinator: coordinator},
		nodeControl{store: store, coordinator: coordinator},
		combinationControl{store: store, coordinator: coordinator},
		collectionControl{store: store},
		marketControl{store: store},
		nil
}
