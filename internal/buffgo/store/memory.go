package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"buff-go/internal/market"
)

// MemoryQuoteStore is an in-process present-summary store for unit tests.
type MemoryQuoteStore struct {
	mu    sync.Mutex
	byKey map[quoteKey]Quote
}

type quoteKey struct {
	itemID   int64
	platform string
	side     market.Side
}

// NewMemoryQuoteStore builds an empty in-memory quote store.
func NewMemoryQuoteStore() *MemoryQuoteStore {
	return &MemoryQuoteStore{byKey: make(map[quoteKey]Quote)}
}

// Ready reports that the in-memory test writer accepts present summaries.
func (m *MemoryQuoteStore) Ready() error {
	if m == nil {
		return fmt.Errorf("nil quote store")
	}
	return nil
}

// UpsertQuotes stores present summaries, deduplicating the batch by identity.
func (m *MemoryQuoteStore) UpsertQuotes(_ context.Context, quotes []Quote) (UpsertResult, error) {
	var res UpsertResult
	if m == nil {
		return res, fmt.Errorf("nil quote store")
	}
	if len(quotes) == 0 {
		return res, nil
	}

	byKey := make(map[quoteKey]Quote, len(quotes))
	order := make([]quoteKey, 0, len(quotes))
	for i := range quotes {
		q := quotes[i]
		if err := q.Normalize(); err != nil {
			return res, fmt.Errorf("quote[%d]: %w", i, err)
		}
		k := quoteKey{itemID: q.ItemID, platform: q.Platform, side: q.Side}
		if _, ok := byKey[k]; !ok {
			order = append(order, k)
		}
		byKey[k] = q
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byKey == nil {
		m.byKey = make(map[quoteKey]Quote)
	}
	for _, k := range order {
		q := byKey[k]
		if existing, ok := m.byKey[k]; ok && strings.TrimSpace(q.Source) == "" {
			q.Source = existing.Source
		}
		m.byKey[k] = q
		res.Written++
		res.Affected++
	}
	return res, nil
}

// UpsertQuote writes a single quote.
func (m *MemoryQuoteStore) UpsertQuote(ctx context.Context, q Quote) error {
	_, err := m.UpsertQuotes(ctx, []Quote{q})
	return err
}

// CountByAppIDPlatformSide counts matching present summaries.
func (m *MemoryQuoteStore) CountByAppIDPlatformSide(_ context.Context, appid int64, platform, side string) (int64, error) {
	if m == nil {
		return 0, fmt.Errorf("nil quote store")
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	wantSide := market.Side(strings.ToLower(strings.TrimSpace(side)))
	if wantSide != market.SideBid && wantSide != market.SideAsk {
		return 0, fmt.Errorf("side must be bid or ask")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, q := range m.byKey {
		if q.AppID == appid && q.Platform == platform && q.Side == wantSide {
			n++
		}
	}
	return n, nil
}

// ListByAppIDPlatformSide returns summaries in executable price order:
// highest bid first and lowest ask first.
func (m *MemoryQuoteStore) ListByAppIDPlatformSide(_ context.Context, appid int64, platform, side string, limit int) ([]Quote, error) {
	if m == nil {
		return nil, fmt.Errorf("nil quote store")
	}
	if limit <= 0 {
		limit = 20
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	wantSide := market.Side(strings.ToLower(strings.TrimSpace(side)))
	if wantSide != market.SideBid && wantSide != market.SideAsk {
		return nil, fmt.Errorf("side must be bid or ask")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Quote, 0)
	for _, q := range m.byKey {
		if q.AppID == appid && q.Platform == platform && q.Side == wantSide {
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PriceCents != out[j].PriceCents {
			if wantSide == market.SideBid {
				return out[i].PriceCents > out[j].PriceCents
			}
			return out[i].PriceCents < out[j].PriceCents
		}
		return out[i].ItemID < out[j].ItemID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Get returns one present quote by identity.
func (m *MemoryQuoteStore) Get(itemID int64, platform string, side market.Side) (Quote, bool) {
	if m == nil {
		return Quote{}, false
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.byKey[quoteKey{itemID: itemID, platform: platform, side: side}]
	return q, ok
}

// Len returns the number of stored present summaries.
func (m *MemoryQuoteStore) Len() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byKey)
}
