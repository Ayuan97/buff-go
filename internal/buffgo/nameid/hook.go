package nameid

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"buff-go/internal/buffgo/steam"
	"buff-go/internal/telemetry"
)

// MaybeBackfillAfterSell runs a capped nameid backfill when limit > 0.
// Failures are logged only — callers must not fail steam.sell jobs because of nameid.
//
// limit: use LimitFromEnv() at the call site (0 = off). Page delay uses DelayFromEnv().
// resolver should be built with NewDefaultResolver / NewResolverWithClient so listing
// HTML uses the same HTTP egress (proxy) as the sell job when available.
func MaybeBackfillAfterSell(ctx context.Context, itemStore Store, resolver Resolver, appid int64, limit int) Stats {
	if limit <= 0 || itemStore == nil || resolver == nil || appid <= 0 {
		return Stats{}
	}
	// Bound wall time so a slow HTML path cannot pin the worker forever.
	bctx, cancel := context.WithTimeout(ctx, time.Duration(limit)*15*time.Second+30*time.Second)
	defer cancel()

	stats, err := Backfill(bctx, itemStore, resolver, Options{
		AppID:     appid,
		Limit:     limit,
		PageDelay: DelayFromEnv(),
		Quiet:     false,
	})
	if err != nil {
		log.Printf("buffgo nameid backfill: appid=%d error_ref=%s (sell job unaffected)", appid, telemetry.SafeErrorRef(err))
		return stats
	}
	if stats.Attempted > 0 {
		log.Printf("buffgo nameid backfill: appid=%d attempted=%d updated=%d failed=%d",
			appid, stats.Attempted, stats.Updated, stats.Failed)
	}
	return stats
}

// NewDefaultResolver returns a steam.Client suitable for listing HTML nameid resolve.
// When hc is non-nil it is used for HTTP egress (e.g. same proxy client as steam.sell).
// When hc is nil, steam.NewClient installs its default timeout client (direct egress).
// Cookie is optional for public listing pages.
func NewDefaultResolver(hc *http.Client) *steam.Client {
	return steam.NewClient(steam.Options{HTTPClient: hc})
}

// NewResolverWithClient builds a nameid resolver with optional proxy-aware egress.
//
//   - hc: when non-nil, used for all listing HTML requests (same proxy as steam.sell).
//     When nil, steam default client is used (direct).
//   - proxyID: stored on steam.Client for consistency with sell rate-limit keys.
//     nameid resolve uses listing HTML (not search/render), so SearchLimiter is unused;
//     ProxyID is optional but kept for observability / future hooks.
//
// Callers that only have a client (or only want defaults) can use NewDefaultResolver.
func NewResolverWithClient(hc *http.Client, proxyID string) *steam.Client {
	return steam.NewClient(steam.Options{
		HTTPClient: hc,
		ProxyID:    strings.TrimSpace(proxyID),
	})
}
