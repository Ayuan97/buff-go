package collection

import (
	"time"

	"buff-go/internal/market"
	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

// WorkerItem is one product on the worker's current or last page.
type WorkerItem struct {
	ProductID  int64
	Name       string
	Status     string
	PriceCents *int64
}

// WorkerClaim is the direction a combination is currently collecting.
type WorkerClaim struct {
	TargetID  TargetID
	TaskID    TaskID
	AppID     int64
	Side      market.Side
	Platform  Platform
	Kind      TaskKind
	Endpoint  ratelimit.EndpointClass
	ClaimedAt time.Time
	Active    bool
	Items     []WorkerItem
}

// WorkerPage is the latest page this account+exit has committed.
type WorkerPage struct {
	TargetID    TargetID
	AppID       int64
	Side        market.Side
	Platform    Platform
	CommittedAt time.Time
	Items       []WorkerItem
}

// WorkerSnapshot is one account+IP row for the run page.
type WorkerSnapshot struct {
	Combination  resource.AccountNodeCombination
	AccountAlias string
	SessionState resource.AccountSessionState
	NodeName     string
	ExitAddress  string
	Region       resource.NodeRegion
	Idle         bool
	Claim        *WorkerClaim
	ActiveWaits  []WorkerWait
	LastPage     *WorkerPage
}

// WorkerWaitScope identifies the resource identity blocked by a runtime retry.
type WorkerWaitScope string

const (
	WorkerWaitScopeRate        WorkerWaitScope = "account_exit_endpoint"
	WorkerWaitScopeNode        WorkerWaitScope = "node_platform"
	WorkerWaitScopeCombination WorkerWaitScope = "combination"
)

// WorkerWaitReason is a controlled runtime retry reason exposed by the API.
type WorkerWaitReason string

const (
	WorkerWaitReasonRateLimit WorkerWaitReason = "rate_limit"
	WorkerWaitReasonDeferred  WorkerWaitReason = "deferred"
	WorkerWaitReasonNetwork   WorkerWaitReason = "network"
	WorkerWaitReasonTimeout   WorkerWaitReason = "timeout"
	WorkerWaitReasonTransient WorkerWaitReason = "transient"
)

// WorkerWait is one active resident retry fence and its exact identity.
type WorkerWait struct {
	Scope         WorkerWaitScope
	Reason        WorkerWaitReason
	BlockOrErr    BlockOrErr
	RetryAt       time.Time
	Platform      Platform
	Endpoint      ratelimit.EndpointClass
	Side          market.Side
	AccountID     resource.AccountID
	ExitAddress   string
	NodeID        resource.NodeID
	CombinationID resource.CombinationID
}

// WorkerRuntimeClaim is one task currently executing inside the resident daemon.
type WorkerRuntimeClaim struct {
	CombinationID resource.CombinationID
	TargetID      TargetID
	TaskID        TaskID
	AppID         int64
	Side          market.Side
	Platform      Platform
	Kind          TaskKind
	Endpoint      ratelimit.EndpointClass
	ClaimedAt     time.Time
}

// WorkerRuntimeSnapshot is a point-in-time, credential-free daemon view.
type WorkerRuntimeSnapshot struct {
	Claims []WorkerRuntimeClaim
	Waits  []WorkerWait
}

// WorkerRuntimeReader exposes the resident daemon view to the control plane.
type WorkerRuntimeReader interface {
	WorkerRuntime() WorkerRuntimeSnapshot
}
