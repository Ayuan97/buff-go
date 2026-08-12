// Package pool manages Redis-backed resource leases and per-(proxy, platform) cooldowns.
//
// P3.1: Acquire / Renew / Release + (proxy, platform) cooldown.
// P3.2: proxy line_type must be in platform prefer (pool.platform_lines).
// P3.3: proxy only_appids (hard game split) + game max_proxy_leases soft quota;
//
//	dedicated pool ordered before shared via OrderProxiesForAppID.
//
// P3.4: multi-worker behaviour verification (two Manager instances / concurrent
//
//	Acquire): no double lease; Buff cooldown then Steam same IP; line prefer +
//	only_appids / max_proxy_leases (see multi_worker_test.go).
//
// Proxy listing (SLICE C): ProxyProvider + StaticProvider from config [[proxies]]
// (or empty → proxy_id=direct). Manager does not own the list — worker loops
// CandidatesFromProvider then Acquire. No proxy CRUD CLI in buffgo.
package pool

import (
	"fmt"
	"strings"
	"time"

	"buff-go/internal/buffgo/source"
)

// Lease is a resource reservation stored in Redis with TTL (crash-safe reclaim).
// Matches architecture: Lease = {proxy, account, platform, appid, worker_id, exp}.
type Lease struct {
	ID       string    `json:"id"`
	Proxy    string    `json:"proxy"`
	Account  string    `json:"account,omitempty"`
	Platform string    `json:"platform"`
	LineType string    `json:"line_type,omitempty"`
	AppID    int64     `json:"appid"`
	WorkerID string    `json:"worker_id"`
	Exp      time.Time `json:"exp"`
}

// ToSourceLease maps a pool lease into the Fetch pipeline placeholder.
func (l Lease) ToSourceLease() source.Lease {
	return source.Lease{
		WorkerID: l.WorkerID,
		Proxy:    l.Proxy,
		Account:  l.Account,
		Platform: l.Platform,
		AppID:    l.AppID,
	}
}

// AcquireRequest asks for a lease on a specific proxy (+ optional account).
// Callers that need multi-candidate selection filter/order via
// OrderProxiesForAppID then loop Acquire themselves.
// LineType is the proxy's line_type and must fall in the platform prefer set.
// OnlyAppIDs is the proxy's only_appids (empty = shared pool).
type AcquireRequest struct {
	WorkerID   string
	Proxy      string  // required: proxy id or endpoint
	Account    string  // optional; when set, exclusive account lock
	Platform   string  // required
	LineType   string  // required: proxy line_type (cn/hk/oversea/dual)
	AppID      int64   // required (>0)
	OnlyAppIDs []int64 // proxy only_appids; empty = shared (P3.3)
}

// Validate checks required Acquire fields (line / only_appids / quota enforced in Manager).
func (r AcquireRequest) Validate() error {
	if strings.TrimSpace(r.WorkerID) == "" {
		return fmt.Errorf("worker_id is required")
	}
	if strings.TrimSpace(r.Proxy) == "" {
		return fmt.Errorf("proxy is required")
	}
	if strings.TrimSpace(r.Platform) == "" {
		return fmt.Errorf("platform is required")
	}
	if strings.TrimSpace(r.LineType) == "" {
		return fmt.Errorf("line_type is required")
	}
	if !ValidLineType(r.LineType) {
		return fmt.Errorf("line_type must be one of cn/hk/oversea/dual")
	}
	if r.AppID <= 0 {
		return fmt.Errorf("appid is required")
	}
	return nil
}

// ReleaseOptions controls side effects when freeing a lease.
type ReleaseOptions struct {
	// SetCooldown, when true, marks (proxy, platform) cooling so the same
	// platform cannot re-lease until the cooldown TTL expires. Other platforms
	// remain free to lease the same proxy (cross-platform reuse).
	SetCooldown bool
	// Cooldown overrides Manager default platform cooldown when > 0.
	Cooldown time.Duration
}

// Sentinel errors for lease operations.
var (
	ErrNotFound        = fmt.Errorf("lease not found")
	ErrNotOwner        = fmt.Errorf("lease not owned by worker")
	ErrBusy            = fmt.Errorf("resource already leased")
	ErrCooling         = fmt.Errorf("proxy platform is cooling")
	ErrLineMismatch    = fmt.Errorf("proxy line_type not in platform prefer")
	ErrAppIDNotAllowed = fmt.Errorf("proxy only_appids does not allow appid")
	ErrQuotaExceeded   = fmt.Errorf("appid max_proxy_leases exceeded")
	ErrInvalidRequest  = fmt.Errorf("invalid acquire request")
)
