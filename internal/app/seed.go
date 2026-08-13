package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"buff-go/internal/ratelimit"
	"buff-go/internal/storage/postgres"
)

// seedSteamRateLimits writes the evidenced Steam policies if they are absent.
// The platform min-interval is local self-pacing, not a proven Steam QPS.
// The interface cooldown is the hour-scale 429 fallback (no Retry-After).
func steamRateLimitSpecs() []ratelimit.PolicySpec {
	return []ratelimit.PolicySpec{
		{
			Platform:        "steam",
			RuleKey:         "local_pacing",
			Scope:           ratelimit.ScopePlatform,
			Kind:            ratelimit.KindMinInterval,
			MinInterval:     10 * time.Second,
			DefaultCooldown: time.Hour,
		},
		{
			Platform:        "steam",
			RuleKey:         "search_render_429",
			Scope:           ratelimit.ScopeInterface,
			EndpointClass:   "market_summary",
			Kind:            ratelimit.KindCooldownOnly,
			DefaultCooldown: time.Hour,
		},
		{
			Platform:        "steam",
			RuleKey:         "orderbook_unexhausted",
			Scope:           ratelimit.ScopeInterface,
			EndpointClass:   "market_orderbook",
			Kind:            ratelimit.KindCooldownOnly,
			DefaultCooldown: time.Hour,
		},
	}
}

func seedSteamRateLimits(ctx context.Context, store *postgres.Store) error {
	for _, spec := range steamRateLimitSpecs() {
		if _, err := store.CreateRateLimitPolicy(ctx, spec, true); err != nil && !errors.Is(err, postgres.ErrRateLimitPolicyConflict) {
			return fmt.Errorf("seed steam rate-limit %s: %w", spec.RuleKey, err)
		}
	}
	return nil
}
