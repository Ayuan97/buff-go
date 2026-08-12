// Package source contains legacy platform fetch adapters. RawOffer preserves
// raw evidence; it is not a verified market summary by itself.
package source

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"buff-go/internal/market"
)

// ErrPageLimit marks a caller cap before a legacy adapter's reported total.
var ErrPageLimit = errors.New("crawl page limit reached before upstream total")

// Platform identifiers used in quotes / jobs.
const (
	PlatformSteam = "steam"
	PlatformBuff  = "buff"
)

// Side keeps source contracts compatible while the market domain is migrated.
type Side = market.Side

const (
	SideBid = market.SideBid
	SideAsk = market.SideAsk
)

// Source names used by worker --sources allowlist (platform.side).
const (
	NameSteamAsk = "steam.ask"
	NameBuffAsk  = "buff.ask"
)

// Quote Source identifiers (docs/DATA_MODEL.md §4.4) — persisted on quotes.source.
const (
	SourceSteamSearch = "steam.search"
	SourceBuffGoods   = "buff.goods"
)

// SourceMeta keys for raw adapter evidence.
const (
	MetaEndpoint = "endpoint"
)

// JobSpec describes one scheduled scrape job (platform + appid + side).
type JobSpec struct {
	Key      string // config job key, e.g. steam_sell_rust
	Platform string
	Side     Side
	AppID    int64
	Enabled  bool
	// Optional legacy fetch sizing (MaxPages 0 follows the adapter-reported total).
	MaxPages  int
	Count     int
	PageDelay time.Duration
	// Start is the in-memory resume cursor: Steam offset or Buff page; 0 starts from the beginning.
	Start int
}

// SourceName returns the allowlist token "platform.side".
func (j JobSpec) SourceName() string {
	return strings.ToLower(strings.TrimSpace(j.Platform)) + "." + string(j.Side)
}

// Lease is a resource reservation placeholder (proxy/account filled in P3).
type Lease struct {
	WorkerID string
	Proxy    string
	Account  string
	Platform string
	AppID    int64
}

// RawOffer is an adapter row containing platform evidence and product identity.
// Raw fields are never comparable market values. A present Observation requires
// verified direction, CNY currency, unit, and timestamps; failed attempts may
// carry a non-present Observation without promoting raw fields.
type RawOffer struct {
	Platform       string
	AppID          int64
	PlatformItemID string
	NameRaw        string
	// ExactName is set only when an adapter has proved which platform field is
	// directly comparable with the Steam catalog name. Raw response names and
	// hashes must not be promoted into it automatically.
	ExactName      string
	MarketHashName string
	RawPriceMinor  *int64
	RawCurrency    string
	RawQuantity    *int64
	RawPriceText   string
	Observation    *market.Observation
	// Source is the quotes.source interface id (e.g. steam.search, buff.goods).
	Source     string
	SourceMeta map[string]string // small string bag persisted as JSON
}

// Normalize validates identity and raw evidence without inventing market facts.
func (o *RawOffer) Normalize() error {
	if o == nil {
		return fmt.Errorf("nil offer")
	}
	o.Platform = strings.ToLower(strings.TrimSpace(o.Platform))
	o.PlatformItemID = strings.TrimSpace(o.PlatformItemID)
	o.NameRaw = strings.TrimSpace(o.NameRaw)
	o.MarketHashName = strings.TrimSpace(o.MarketHashName)
	o.RawCurrency = strings.TrimSpace(o.RawCurrency)
	o.RawPriceText = strings.TrimSpace(o.RawPriceText)
	o.Source = strings.TrimSpace(o.Source)
	if o.Platform == "" {
		return fmt.Errorf("platform is required")
	}
	if o.AppID <= 0 {
		return fmt.Errorf("appid is required")
	}
	if o.MarketHashName == "" && o.PlatformItemID == "" && o.NameRaw == "" && o.ExactName == "" {
		return fmt.Errorf("platform_item_id, exact_name, or raw identity evidence is required")
	}
	if o.RawPriceMinor != nil && *o.RawPriceMinor < 0 {
		return fmt.Errorf("raw price minor units cannot be negative")
	}
	if o.RawQuantity != nil && *o.RawQuantity < 0 {
		return fmt.Errorf("raw quantity cannot be negative")
	}
	if o.Observation != nil {
		if err := o.Observation.Validate(); err != nil {
			return fmt.Errorf("market observation: %w", err)
		}
	}
	return nil
}

// Source is a pluggable market fetcher.
type Source interface {
	// Name is the allowlist token, e.g. "steam.ask".
	Name() string
	// Fetch pulls current offers for job (appid/side already on job).
	Fetch(ctx context.Context, lease Lease, job JobSpec) ([]RawOffer, error)
}

// MatchSources reports whether sourceName is allowed by the comma-separated
// allowlist. Empty allowlist means all sources.
func MatchSources(allowlist, sourceName string) bool {
	allowlist = strings.TrimSpace(allowlist)
	if allowlist == "" {
		return true
	}
	want := strings.ToLower(strings.TrimSpace(sourceName))
	for _, part := range strings.Split(allowlist, ",") {
		if strings.ToLower(strings.TrimSpace(part)) == want {
			return true
		}
	}
	return false
}
