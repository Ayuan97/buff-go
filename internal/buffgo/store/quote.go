// Package store provides temporary persistence adapters for the market pipeline.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"buff-go/internal/buffgo/source"
	"buff-go/internal/market"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ErrLegacyQuoteSchema marks the old floating-point quote table as unsafe for
// the CNY-cent market contract. Goal 4A replaces this adapter and schema.
var ErrLegacyQuoteSchema = errors.New("legacy quote schema cannot store CNY-cent market summaries")

// Quote is one present CNY market summary after product resolution.
// Empty, unavailable, and failed attempts are not prices and cannot become Quote.
type Quote struct {
	ItemID      int64
	AppID       int64
	Platform    string
	Side        market.Side
	PriceCents  market.CNYCents
	OrderCount  *int64
	ItemCount   *int64
	SourceTime  *time.Time
	CollectedAt time.Time
	Source      string
	SourceMeta  map[string]string
}

// Normalize validates a present quote without inventing defaults.
func (q *Quote) Normalize() error {
	if q == nil {
		return fmt.Errorf("nil quote")
	}
	q.Platform = strings.ToLower(strings.TrimSpace(q.Platform))
	q.Source = strings.TrimSpace(q.Source)
	if q.ItemID <= 0 {
		return fmt.Errorf("item_id is required")
	}
	if q.AppID <= 0 {
		return fmt.Errorf("appid is required")
	}
	if q.Platform == "" {
		return fmt.Errorf("platform is required")
	}
	if q.Side != market.SideBid && q.Side != market.SideAsk {
		return fmt.Errorf("side must be bid or ask, got %q", q.Side)
	}
	if q.SourceTime != nil && q.SourceTime.IsZero() {
		return fmt.Errorf("source_time cannot be zero when provided")
	}
	if q.CollectedAt.IsZero() {
		return fmt.Errorf("collected_at is required")
	}
	return (market.PresentSummary{
		PriceCents: q.PriceCents,
		OrderCount: q.OrderCount,
		ItemCount:  q.ItemCount,
	}).Validate()
}

// QuoteStore is the legacy PostgreSQL quote adapter.
// Its old schema stores floating major units and buy/sell strings, so all
// market reads and writes fail closed until Goal 4A installs the new schema.
type QuoteStore struct {
	db *sql.DB
}

// NewQuoteStore wraps an open *sql.DB.
func NewQuoteStore(db *sql.DB) *QuoteStore {
	return &QuoteStore{db: db}
}

// Ready reports that the legacy schema cannot accept the new contract.
func (s *QuoteStore) Ready() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("nil quote store")
	}
	return ErrLegacyQuoteSchema
}

// Open opens pgx and returns the fail-closed legacy quote adapter.
func Open(dsn string) (*QuoteStore, *sql.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, nil, fmt.Errorf("empty postgres dsn")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(time.Minute)
	return NewQuoteStore(db), db, nil
}

// UpsertResult summarizes a batch quote write.
type UpsertResult struct {
	Written  int
	Affected int64
}

// UpsertQuote writes one present quote.
func (s *QuoteStore) UpsertQuote(ctx context.Context, q Quote) error {
	_, err := s.UpsertQuotes(ctx, []Quote{q})
	return err
}

// UpsertQuotes rejects writes to the incompatible legacy schema.
func (s *QuoteStore) UpsertQuotes(_ context.Context, quotes []Quote) (UpsertResult, error) {
	var res UpsertResult
	if s == nil || s.db == nil {
		return res, fmt.Errorf("nil quote store")
	}
	if len(quotes) == 0 {
		return res, nil
	}
	for i := range quotes {
		q := quotes[i]
		if err := q.Normalize(); err != nil {
			return res, fmt.Errorf("quote[%d]: %w", i, err)
		}
	}
	return res, ErrLegacyQuoteSchema
}

// CountByAppIDPlatformSide rejects reads from the incompatible legacy schema.
func (s *QuoteStore) CountByAppIDPlatformSide(context.Context, int64, string, string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("nil quote store")
	}
	return 0, ErrLegacyQuoteSchema
}

// ListByAppIDPlatformSide rejects reads from the incompatible legacy schema.
func (s *QuoteStore) ListByAppIDPlatformSide(context.Context, int64, string, string, int) ([]Quote, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("nil quote store")
	}
	return nil, ErrLegacyQuoteSchema
}

// QuoteFromOffer converts only an explicitly validated present CNY observation.
// Raw platform price, currency, quantity, and timestamps are never promoted.
func QuoteFromOffer(itemID int64, offer source.RawOffer) (Quote, error) {
	var q Quote
	if offer.Observation == nil {
		return q, fmt.Errorf("market observation is required")
	}
	if err := offer.Observation.Validate(); err != nil {
		return q, fmt.Errorf("market observation: %w", err)
	}
	if offer.Observation.Status != market.StatusPresent || offer.Observation.Summary == nil {
		return q, fmt.Errorf("market observation is not present")
	}
	summary := offer.Observation.Summary
	q = Quote{
		ItemID:      itemID,
		AppID:       offer.AppID,
		Platform:    offer.Platform,
		Side:        offer.Observation.Side,
		PriceCents:  summary.PriceCents,
		OrderCount:  summary.OrderCount,
		ItemCount:   summary.ItemCount,
		SourceTime:  offer.Observation.SourceTime,
		CollectedAt: offer.Observation.CollectedAt,
		Source:      offer.Source,
		SourceMeta:  offer.SourceMeta,
	}
	if err := q.Normalize(); err != nil {
		return Quote{}, err
	}
	return q, nil
}
