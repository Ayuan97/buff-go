package api

import (
	"context"
	"net/netip"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

// ControlServices are occupancy-guarded control-plane facades.
type ControlServices struct {
	Accounts     AccountService
	Nodes        NodeService
	Combinations CombinationService
	Collection   CollectionService
	Market       MarketService
}

// CollectionService is the summary-target and run query boundary.
type CollectionService interface {
	ListTargets(context.Context) ([]collection.Target, error)
	Target(context.Context, collection.TargetID) (collection.Target, bool, error)
	CreateSummaryTarget(context.Context, collection.Platform, int64, market.Side, collection.DesiredState) (collection.Target, error)
	SetTargetDesired(context.Context, collection.TargetID, collection.Revision, collection.DesiredState) (collection.Target, error)
	ListRecentRuns(context.Context, int) ([]collection.Run, error)
}

// MarketService lists latest quotes without raw platform payloads.
type MarketService interface {
	ListQuotes(context.Context, int64, string, int) ([]postgres.MarketQuote, error)
}

// NodeService is the credential-safe node control boundary.
type NodeService interface {
	ListNodes(context.Context) ([]resource.AccessNode, error)
	Node(context.Context, resource.NodeID) (resource.AccessNode, bool, error)
	CreateNode(context.Context, string, resource.NodeConnectionInput) (resource.AccessNode, error)
	ReplaceNodeConnection(context.Context, resource.NodeID, int64, resource.NodeConnectionInput) (resource.AccessNode, error)
	DeleteNode(context.Context, resource.NodeID) error
	AssignNodeGame(context.Context, resource.NodeID, int64, int64) (resource.AccessNode, error)
	AssignNodeSide(context.Context, resource.NodeID, int64, resource.Platform, market.Side) (resource.AccessNode, error)
	RecordNodeExit(context.Context, resource.NodeID, int64, netip.Addr, time.Time) (resource.AccessNode, error)
}

// CombinationService is the account-node binding control boundary.
type CombinationService interface {
	ListCombinations(context.Context) ([]resource.AccountNodeCombination, error)
	Combination(context.Context, resource.CombinationID) (resource.AccountNodeCombination, bool, error)
	CreateCombination(context.Context, resource.AccountID, resource.NodeID) (resource.AccountNodeCombination, error)
	DeleteCombination(context.Context, resource.CombinationID) error
}
