package collection

import (
	"time"

	"buff-go/internal/market"
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
	TargetID TargetID
	AppID    int64
	Side     market.Side
	Platform Platform
	Items    []WorkerItem
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
	LastPage     *WorkerPage
}
