package api

import (
	"context"
	"net/netip"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
)

// ControlServices are occupancy-guarded control-plane facades.
// PlatformRegions 来自采集侧的平台档案，控制面用它拦截地域不符的组合和换线路。
type ControlServices struct {
	Accounts        AccountService
	Nodes           NodeService
	Combinations    CombinationService
	Collection      CollectionService
	Market          MarketService
	Providers       ProviderService
	PlatformRegions map[resource.Platform]resource.TargetRegion
}

// ProviderService is the vendor and short-pool watermark boundary.
type ProviderService interface {
	ListWatermarks(context.Context) ([]resource.RegionWatermark, error)
	SetWatermark(context.Context, resource.NodeRegion, int64, int) (resource.RegionWatermark, error)
	ListProviders(context.Context) ([]resource.ProxyProvider, error)
	CreateProvider(context.Context, string, bool, int, []resource.NodeRegion, []byte) (resource.ProxyProvider, error)
	UpdateProvider(context.Context, resource.ProviderID, int64, string, bool, int, []resource.NodeRegion) (resource.ProxyProvider, error)
	ReplaceProviderCredential(context.Context, resource.ProviderID, int64, []byte) (resource.ProxyProvider, error)
	DeleteProvider(context.Context, resource.ProviderID) error
}

// CollectionService is the summary-target and worker query boundary.
type CollectionService interface {
	ListTargets(context.Context) ([]collection.Target, error)
	Target(context.Context, collection.TargetID) (collection.Target, bool, error)
	CreateSummaryTarget(context.Context, collection.Platform, int64, market.Side, collection.DesiredState) (collection.Target, error)
	SetTargetDesired(context.Context, collection.TargetID, collection.Revision, collection.DesiredState) (collection.Target, error)
	SetTargetSortOrder(context.Context, collection.TargetID, collection.Revision, collection.SortOrder) (collection.Target, error)
	SetTargetPriceRange(context.Context, collection.TargetID, collection.Revision, collection.PriceRange) (collection.Target, error)
	SetTargetSteamFacets(context.Context, collection.TargetID, collection.Revision, collection.SteamFacets) (collection.Target, error)
	DeleteTarget(context.Context, collection.TargetID) error
	ListWorkers(context.Context) ([]collection.WorkerSnapshot, error)
}

// MarketService lists latest quotes without raw platform payloads.
type MarketService interface {
	ListQuotes(context.Context, market.QuoteFilter) (market.QuoteResult, error)
	QuoteFacets(context.Context, int64) (market.QuoteFacets, error)
	ListPriceTicks(context.Context, market.PriceTickFilter) ([]market.PriceTick, error)
}

// NodeService is the credential-safe node control boundary.
type NodeService interface {
	ListNodes(context.Context) ([]resource.AccessNode, error)
	Node(context.Context, resource.NodeID) (resource.AccessNode, bool, error)
	CreateNode(context.Context, string, resource.NodeConnectionInput) (resource.AccessNode, error)
	RenameNode(context.Context, resource.NodeID, string) (resource.AccessNode, error)
	ReplaceNodeConnection(context.Context, resource.NodeID, int64, resource.NodeConnectionInput) (resource.AccessNode, error)
	DeleteNode(context.Context, resource.NodeID) error
	RecordNodeExit(context.Context, resource.NodeID, int64, netip.Addr, time.Time) (resource.AccessNode, error)
}

// CombinationService is the account-node binding control boundary.
type CombinationService interface {
	ListCombinations(context.Context) ([]resource.AccountNodeCombination, error)
	Combination(context.Context, resource.CombinationID) (resource.AccountNodeCombination, bool, error)
	CreateCombination(context.Context, resource.AccountID, resource.NodeID) (resource.AccountNodeCombination, error)
	DeleteCombination(context.Context, resource.CombinationID) error
}
