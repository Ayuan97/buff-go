package steam

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Platform and endpoint class constants for rate-limit keys.
// Keys are always (proxy_id|direct, platform=steam, class=search) for search/render.
const (
	// PlatformSteam is the platform dimension of the rate-limit key.
	PlatformSteam = "steam"
	// EndpointSearch is market/search/render (and related list crawl).
	EndpointSearch = "search"
	// ProxyDirect is used when no proxy is configured (direct egress IP).
	ProxyDirect = "direct"
)

// Legacy compatibility defaults. Goal 0B has not verified them as Steam policy.
const (
	DefaultSearchSoftMax     = 80
	DefaultSearchHardMax     = 100
	DefaultSearchWindow      = 5 * time.Minute
	DefaultSearchCooldown429 = 180 * time.Second
	DefaultSearchMinInterval = 2 * time.Second
)

// Sentinel errors for search rate limiting. Callers should skip/retry later;
// never exit the process.
var (
	// ErrSearchCooling means this proxy is under a steam search 429 cooldown.
	ErrSearchCooling = errors.New("steam search: proxy cooling after 429")
	// ErrSearchBudget means soft/hard per-window search budget is exhausted.
	ErrSearchBudget = errors.New("steam search: per-proxy budget exhausted")
)

// IsSearchRateLimit reports whether err is a search budget or cooldown denial
// (no HTTP call was made, or HTTP 429 was observed).
func IsSearchRateLimit(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrSearchCooling) || errors.Is(err, ErrSearchBudget)
}

// SearchLimiter enforces per-proxy Steam search budgets, min spacing, and 429 cooldowns.
// Implementations must be safe for concurrent use. Redis-backed limiters
// share state across workers; MemorySearchLimiter is process-local.
//
// Key dimensions: proxy_id (or "direct"), platform=steam, endpoint class=search.
type SearchLimiter interface {
	// AllowSearch reserves one search slot for proxyID when under budget and
	// not cooling. Empty proxyID uses ProxyDirect ("direct").
	// May block (sleep) until MinInterval has elapsed since the last successful
	// allow for the same proxy; respects ctx cancellation during that wait.
	// Min-interval wait is pacing, not a hard error.
	AllowSearch(ctx context.Context, proxyID string) error
	// MarkSearch429 sets the configured search cooldown for proxyID.
	MarkSearch429(ctx context.Context, proxyID string) error
}

// SearchLimitConfig configures SoftMax / HardMax / window / cooldown / min interval.
// Zero values are replaced by legacy compatibility defaults.
type SearchLimitConfig struct {
	// SoftMax is the working max successful search calls per window (default 80).
	SoftMax int
	// HardMax is the absolute cap (default 100). SoftMax is clamped to HardMax.
	HardMax int
	// Window is the counter window duration (default 5m). Counter resets when
	// the window key expires (Redis) or wall clock advances (memory).
	Window time.Duration
	// CooldownOn429 is how long search is blocked on a proxy after HTTP 429 (default 180s).
	CooldownOn429 time.Duration
	// MinInterval is the minimum wall time between successful AllowSearch calls
	// for the same proxy (default 2s). AllowSearch sleeps for any remainder.
	MinInterval time.Duration
}

// Normalize fills defaults and applies env overrides:
//
//	BUFFGO_STEAM_SEARCH_MAX_PER_WINDOW  (int, soft max)
//	BUFFGO_STEAM_SEARCH_COOLDOWN        (duration, e.g. "3m")
//	BUFFGO_STEAM_SEARCH_HARD_MAX        (int, optional)
//	BUFFGO_STEAM_SEARCH_WINDOW          (duration, optional)
//	BUFFGO_STEAM_SEARCH_MIN_INTERVAL    (duration, e.g. "2s")
func (c SearchLimitConfig) Normalize() SearchLimitConfig {
	out := c
	if out.SoftMax <= 0 {
		out.SoftMax = DefaultSearchSoftMax
	}
	if out.HardMax <= 0 {
		out.HardMax = DefaultSearchHardMax
	}
	if out.Window <= 0 {
		out.Window = DefaultSearchWindow
	}
	if out.CooldownOn429 <= 0 {
		out.CooldownOn429 = DefaultSearchCooldown429
	}
	if out.MinInterval <= 0 {
		out.MinInterval = DefaultSearchMinInterval
	}
	// Env overrides (optional ops knobs).
	if v := strings.TrimSpace(os.Getenv("BUFFGO_STEAM_SEARCH_MAX_PER_WINDOW")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			out.SoftMax = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("BUFFGO_STEAM_SEARCH_HARD_MAX")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			out.HardMax = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("BUFFGO_STEAM_SEARCH_COOLDOWN")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			out.CooldownOn429 = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("BUFFGO_STEAM_SEARCH_WINDOW")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			out.Window = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("BUFFGO_STEAM_SEARCH_MIN_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			out.MinInterval = d
		}
	}
	if out.SoftMax > out.HardMax {
		out.SoftMax = out.HardMax
	}
	return out
}

// sleepCtx waits for d or until ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// NormalizeProxyID returns a stable rate-limit key for a proxy.
// Empty / whitespace → "direct".
func NormalizeProxyID(proxyID string) string {
	p := strings.ToLower(strings.TrimSpace(proxyID))
	if p == "" {
		return ProxyDirect
	}
	return p
}

// SearchRateKey builds a logical key (proxy, platform, class) for logs/tests.
func SearchRateKey(proxyID string) string {
	return fmt.Sprintf("%s:%s:%s", NormalizeProxyID(proxyID), PlatformSteam, EndpointSearch)
}
