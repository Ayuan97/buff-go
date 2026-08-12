// Package nameid is a legacy adapter that stores candidate item_nameid values
// extracted from listing HTML. Goal 0B has not verified their stability,
// identity role, or necessity for any endpoint.
package nameid

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"buff-go/internal/buffgo/catalog"
	"buff-go/internal/buffgo/steam"
	"buff-go/internal/telemetry"
)

const (
	// EnvBackfillLimit enables a capped post-job nameid fill when > 0.
	// Default unset/0 keeps steam.sell light (no listing HTML fetches).
	EnvBackfillLimit = "BUFFGO_NAMEID_BACKFILL_LIMIT"
	// EnvBackfillDelay controls the legacy pause between HTML resolves.
	EnvBackfillDelay = "BUFFGO_NAMEID_BACKFILL_DELAY"

	defaultPageDelay = 2 * time.Second
	defaultLimit     = 10
)

// Resolver extracts the candidate item_nameid using legacy HTML patterns.
type Resolver interface {
	ResolveItemNameID(ctx context.Context, appid int64, marketHashName string) (string, error)
}

// Store lists rows missing the candidate value and persists extracted values.
type Store interface {
	ListMissingSteamNameID(ctx context.Context, appid int64, limit int) ([]catalog.Item, error)
	UpdateSteamNameID(ctx context.Context, appid int64, marketHashName, nameid string) error
}

// Compile-time checks that production types satisfy the interfaces.
var (
	_ Store    = (*catalog.Store)(nil)
	_ Resolver = (*steam.Client)(nil)
)

// Stats summarizes one Backfill pass.
type Stats struct {
	Attempted int
	Updated   int
	Failed    int
}

// Options configures a bounded nameid backfill batch.
type Options struct {
	AppID int64
	// Limit caps the legacy batch size; it is not a verified platform-safe value.
	// <=0 uses defaultLimit (10) only when Backfill is called explicitly; pipeline hook
	// uses LimitFromEnv and skips when 0.
	Limit int
	// PageDelay sleeps between HTML fetches after the first; zero uses the legacy default.
	PageDelay time.Duration
	// Quiet suppresses per-item failure logs when true.
	Quiet bool
}

// LimitFromEnv returns BUFFGO_NAMEID_BACKFILL_LIMIT (0 = disabled / unset).
func LimitFromEnv() int {
	v := strings.TrimSpace(os.Getenv(EnvBackfillLimit))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// DelayFromEnv returns BUFFGO_NAMEID_BACKFILL_DELAY or defaultPageDelay.
func DelayFromEnv() time.Duration {
	v := strings.TrimSpace(os.Getenv(EnvBackfillDelay))
	if v == "" {
		return defaultPageDelay
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return defaultPageDelay
	}
	return d
}

// Backfill resolves and persists steam_item_name_id for up to Limit items missing it.
// Individual resolve/update failures are counted and skipped; the batch continues.
// Returns a non-nil error only for hard setup/list failures (not per-item resolve errors).
func Backfill(ctx context.Context, st Store, resolver Resolver, opt Options) (Stats, error) {
	var stats Stats
	if st == nil {
		return stats, fmt.Errorf("nameid.Backfill: nil store")
	}
	if resolver == nil {
		return stats, fmt.Errorf("nameid.Backfill: nil resolver")
	}
	if opt.AppID <= 0 {
		return stats, fmt.Errorf("nameid.Backfill: appid required")
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	delay := opt.PageDelay
	if delay <= 0 {
		delay = defaultPageDelay
	}

	missing, err := st.ListMissingSteamNameID(ctx, opt.AppID, limit)
	if err != nil {
		return stats, fmt.Errorf("list missing nameid: %w", err)
	}
	for i, it := range missing {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if i > 0 && delay > 0 {
			select {
			case <-ctx.Done():
				return stats, ctx.Err()
			case <-time.After(delay):
			}
		}
		stats.Attempted++
		id, err := resolver.ResolveItemNameID(ctx, it.AppID, it.MarketHashName)
		if err != nil {
			stats.Failed++
			if !opt.Quiet {
				log.Printf("nameid.Backfill: resolve item_ref=%s appid=%d error_ref=%s",
					telemetry.SafeDetailRef(it.MarketHashName), it.AppID, telemetry.SafeErrorRef(err))
			}
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" {
			stats.Failed++
			if !opt.Quiet {
				log.Printf("nameid.Backfill: empty nameid item_ref=%s appid=%d",
					telemetry.SafeDetailRef(it.MarketHashName), it.AppID)
			}
			continue
		}
		if err := st.UpdateSteamNameID(ctx, it.AppID, it.MarketHashName, id); err != nil {
			stats.Failed++
			if !opt.Quiet {
				log.Printf("nameid.Backfill: update item_ref=%s appid=%d error_ref=%s",
					telemetry.SafeDetailRef(it.MarketHashName), it.AppID, telemetry.SafeErrorRef(err))
			}
			continue
		}
		stats.Updated++
	}
	return stats, nil
}
