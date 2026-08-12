package pool

import (
	"context"
	"log"
	"strings"
	"sync"

	"buff-go/internal/telemetry"
)

// ProxyIDDirect is the lease / rate-limit identity when no real proxy is configured.
// Rate-limit counters and cooldowns key by proxy_id (or "direct"), platform, endpoint class.
const ProxyIDDirect = "direct"

// ProxyEndpoint is one proxy row for pool acquire selection.
// Fields align with ARCHITECTURE §3.6 / PG proxies (endpoint, auth, line_type, only_appids, enabled).
// PreferAppIDs / MaxConcurrent are optional and used for ordering and future concurrency caps.
type ProxyEndpoint struct {
	// Endpoint is the proxy URL or host:port identity. Empty is treated as direct.
	Endpoint string
	// Auth is optional "user:pass" or vendor token material (not logged by pool).
	Auth string
	// LineType is cn / hk / oversea / dual. Empty normalizes to dual (steam- and buff-safe).
	LineType string
	// OnlyAppIDs hard-splits this IP to listed games; empty = shared pool.
	OnlyAppIDs []int64
	// PreferAppIDs soft-affinity for shared proxies (OrderProxiesForAppID).
	PreferAppIDs []int64
	// Enabled=false excludes the row from List (still kept for static config snapshots).
	Enabled bool
	// MaxConcurrent is reserved for per-IP lease caps (0 = unset / default 1 later).
	MaxConcurrent int
}

// ProxyID returns the identity used in Lease.Proxy and Redis keys (proxy_id or "direct").
func (e ProxyEndpoint) ProxyID() string {
	id := strings.TrimSpace(e.Endpoint)
	if id == "" || strings.EqualFold(id, ProxyIDDirect) {
		return ProxyIDDirect
	}
	return id
}

// IsDirect reports whether this row is the synthetic no-proxy endpoint.
func (e ProxyEndpoint) IsDirect() bool {
	return e.ProxyID() == ProxyIDDirect
}

// Candidate maps this endpoint into an Acquire selection candidate.
func (e ProxyEndpoint) Candidate() ProxyCandidate {
	return ProxyCandidate{
		ID:           e.ProxyID(),
		LineType:     NormalizeLineType(e.LineType),
		OnlyAppIDs:   append([]int64(nil), e.OnlyAppIDs...),
		PreferAppIDs: append([]int64(nil), e.PreferAppIDs...),
	}
}

// AcquireFields returns Proxy / LineType / OnlyAppIDs for AcquireRequest.
func (e ProxyEndpoint) AcquireFields() (proxy, lineType string, onlyAppIDs []int64) {
	return e.ProxyID(), NormalizeLineType(e.LineType), append([]int64(nil), e.OnlyAppIDs...)
}

// DirectEndpoint is the synthetic row used when the static list is empty (direct mode).
// line_type=dual matches both buff and steam default prefer lists.
func DirectEndpoint() ProxyEndpoint {
	return ProxyEndpoint{
		Endpoint: ProxyIDDirect,
		LineType: LineDual,
		Enabled:  true,
	}
}

// ProxyProvider lists proxies available for pool acquire.
// Implementations must be safe for concurrent List from multiple workers.
//
// StaticProvider loads from process config (or empty → direct).
// TODO: HTTPAPIProvider — pull enabled proxies from an internal control-plane
// HTTP API (CRUD lives there / in PG). buffgo must not grow proxy CRUD CLI.
type ProxyProvider interface {
	// List returns endpoints eligible for acquire. Implementations should omit
	// disabled rows. Empty configuration should surface as direct mode
	// (single ProxyIDDirect row) so rate limits still key by proxy_id=direct.
	List(ctx context.Context) ([]ProxyEndpoint, error)
}

// StaticProvider is an in-memory ProxyProvider (config file / test fixtures).
// No Redis or network is required.
type StaticProvider struct {
	// endpoints is the configured snapshot (may include disabled rows).
	endpoints []ProxyEndpoint
}

// NewStaticProvider copies endpoints into a StaticProvider.
// Nil or empty (or all-disabled) list → List returns DirectEndpoint (direct mode).
func NewStaticProvider(endpoints []ProxyEndpoint) *StaticProvider {
	if len(endpoints) == 0 {
		return &StaticProvider{endpoints: nil}
	}
	out := make([]ProxyEndpoint, len(endpoints))
	for i, e := range endpoints {
		out[i] = normalizeEndpoint(e)
	}
	return &StaticProvider{endpoints: out}
}

// IsDirectMode reports whether acquire will use the synthetic "direct" row
// (no enabled proxies configured).
func (p *StaticProvider) IsDirectMode() bool {
	if p == nil {
		return true
	}
	return len(p.enabled()) == 0
}

// LenConfigured returns the number of configured rows (including disabled).
func (p *StaticProvider) LenConfigured() int {
	if p == nil {
		return 0
	}
	return len(p.endpoints)
}

// List implements ProxyProvider. Returns enabled proxies, or [direct] when none.
func (p *StaticProvider) List(ctx context.Context) ([]ProxyEndpoint, error) {
	_ = ctx
	if p == nil {
		return []ProxyEndpoint{DirectEndpoint()}, nil
	}
	en := p.enabled()
	if len(en) == 0 {
		return []ProxyEndpoint{DirectEndpoint()}, nil
	}
	out := make([]ProxyEndpoint, len(en))
	copy(out, en)
	return out, nil
}

// Candidates returns ProxyCandidate rows for appid ordering (dedicated → prefer → shared).
// Uses List (direct fallback) then OrderProxiesForAppID.
func CandidatesFromProvider(ctx context.Context, prov ProxyProvider, appid int64) ([]ProxyCandidate, error) {
	if prov == nil {
		return OrderProxiesForAppID(appid, []ProxyCandidate{DirectEndpoint().Candidate()}), nil
	}
	eps, err := prov.List(ctx)
	if err != nil {
		return nil, err
	}
	cands := make([]ProxyCandidate, 0, len(eps))
	for _, e := range eps {
		cands = append(cands, e.Candidate())
	}
	return OrderProxiesForAppID(appid, cands), nil
}

// EndpointsToCandidates maps endpoints to candidates without appid filtering.
func EndpointsToCandidates(eps []ProxyEndpoint) []ProxyCandidate {
	if len(eps) == 0 {
		return nil
	}
	out := make([]ProxyCandidate, 0, len(eps))
	for _, e := range eps {
		out = append(out, e.Candidate())
	}
	return out
}

func (p *StaticProvider) enabled() []ProxyEndpoint {
	if p == nil || len(p.endpoints) == 0 {
		return nil
	}
	var out []ProxyEndpoint
	for _, e := range p.endpoints {
		if e.Enabled {
			out = append(out, e)
		}
	}
	return out
}

// emptyLineTypeDefaultOnce logs at most once when non-direct proxies default empty line_type → dual.
var emptyLineTypeDefaultOnce sync.Once

func normalizeEndpoint(e ProxyEndpoint) ProxyEndpoint {
	e.Endpoint = strings.TrimSpace(e.Endpoint)
	e.Auth = strings.TrimSpace(e.Auth)
	e.LineType = NormalizeLineType(e.LineType)
	if e.LineType == "" {
		// Empty line_type → dual for all endpoints (direct and non-direct).
		// Misconfigured TOML without line_type previously left non-direct empty,
		// which LineAllowed rejects forever → permanent ErrNoProxyLease soft-skip.
		// dual is steam- and buff-safe (in both prefer sets).
		if e.ProxyID() != ProxyIDDirect {
			emptyLineTypeDefaultOnce.Do(func() {
				log.Printf("buffgo pool: empty line_type defaulted to dual (node_ref=%s; further empty defaults not logged)", telemetry.SafeNodeRef(e.ProxyID()))
			})
		}
		e.LineType = LineDual
	}
	if len(e.OnlyAppIDs) > 0 {
		e.OnlyAppIDs = append([]int64(nil), e.OnlyAppIDs...)
	} else {
		e.OnlyAppIDs = nil
	}
	if len(e.PreferAppIDs) > 0 {
		e.PreferAppIDs = append([]int64(nil), e.PreferAppIDs...)
	} else {
		e.PreferAppIDs = nil
	}
	return e
}

// --- Config-shaped input (avoids pool importing config) ---

// StaticProxyInput is the static-file / PG-shaped proxy row for NewStaticProviderFromInput.
// Mirrors config.ProxyConfig and schema proxies columns used at acquire time.
type StaticProxyInput struct {
	Endpoint      string
	Auth          string
	LineType      string
	OnlyAppIDs    []int64
	PreferAppIDs  []int64
	Enabled       bool
	MaxConcurrent int
}

// NewStaticProviderFromInput builds a StaticProvider from config-like rows.
// Wire from process config without importing config:
//
//	in := cfg.StaticProxyInputs()
//	rows := make([]pool.StaticProxyInput, len(in))
//	for i, r := range in {
//	  rows[i] = pool.StaticProxyInput{Endpoint: r.Endpoint, Auth: r.Auth, LineType: r.LineType,
//	    OnlyAppIDs: r.OnlyAppIDs, PreferAppIDs: r.PreferAppIDs, Enabled: r.Enabled, MaxConcurrent: r.MaxConcurrent}
//	}
//	prov := pool.NewStaticProviderFromInput(rows)
//	mo := cfg.PoolManagerOptions()
//	mgr, err := pool.NewManager(rdb, pool.OptionsFromConfig(mo.LeaseTTL, mo.DefaultCooldown, mo.PlatformLines, mo.MaxProxyLeases))
func NewStaticProviderFromInput(in []StaticProxyInput) *StaticProvider {
	if len(in) == 0 {
		return NewStaticProvider(nil)
	}
	eps := make([]ProxyEndpoint, 0, len(in))
	for _, row := range in {
		eps = append(eps, ProxyEndpoint{
			Endpoint:      row.Endpoint,
			Auth:          row.Auth,
			LineType:      row.LineType,
			OnlyAppIDs:    row.OnlyAppIDs,
			PreferAppIDs:  row.PreferAppIDs,
			Enabled:       row.Enabled,
			MaxConcurrent: row.MaxConcurrent,
		})
	}
	return NewStaticProvider(eps)
}
