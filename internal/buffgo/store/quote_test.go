package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"buff-go/internal/buffgo/source"
	"buff-go/internal/market"
)

func presentObservation(t *testing.T, side market.Side, cents int64, collectedAt time.Time) *market.Observation {
	t.Helper()
	price := market.CNYCents(cents)
	observation, err := market.NewPresentObservation(market.PresentInput{
		Currency: market.CurrencyCNY, Side: side, PriceCents: &price, CollectedAt: collectedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &observation
}

func TestQuoteFromOfferAcceptsOnlyPresentCNYObservation(t *testing.T) {
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	offer := source.RawOffer{
		Platform:       source.PlatformSteam,
		AppID:          252490,
		MarketHashName: "Metal Facemask",
		Observation:    presentObservation(t, market.SideAsk, 1250, collectedAt),
		Source:         source.SourceSteamSearch,
		SourceMeta:     map[string]string{source.MetaEndpoint: "market/search/render"},
	}
	quote, err := QuoteFromOffer(99, offer)
	if err != nil {
		t.Fatal(err)
	}
	if quote.ItemID != 99 || quote.AppID != 252490 || quote.Platform != "steam" {
		t.Fatalf("identity: %+v", quote)
	}
	if quote.Side != market.SideAsk || quote.PriceCents != 1250 || !quote.CollectedAt.Equal(collectedAt) {
		t.Fatalf("market summary: %+v", quote)
	}
	if quote.Source != source.SourceSteamSearch || quote.SourceMeta[source.MetaEndpoint] == "" {
		t.Fatalf("source evidence: %+v", quote)
	}
}

func TestQuoteFromOfferRejectsRawOrNonPresentEvidence(t *testing.T) {
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		offer source.RawOffer
	}{
		{
			name: "raw USD has no unified observation",
			offer: source.RawOffer{
				Platform: "steam", AppID: 252490, MarketHashName: "A", RawCurrency: "USD", RawPriceText: "$1.00",
			},
		},
		{
			name: "raw CNY hint is not verification",
			offer: source.RawOffer{
				Platform: "buff", AppID: 252490, MarketHashName: "A", RawCurrency: "CNY", RawPriceText: "1.00",
			},
		},
		{
			name: "empty is not a price",
			offer: source.RawOffer{
				Platform: "steam", AppID: 252490, MarketHashName: "A",
				Observation: &market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := QuoteFromOffer(1, tt.offer); err == nil {
				t.Fatal("expected conversion error")
			}
		})
	}
}

func TestLegacyQuoteStoreFailsBeforeSQL(t *testing.T) {
	store := NewQuoteStore(&sql.DB{})
	quote := Quote{
		ItemID: 1, AppID: 252490, Platform: "steam", Side: market.SideAsk,
		PriceCents: 1250, CollectedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
	}
	if _, err := store.UpsertQuotes(context.Background(), []Quote{quote}); !errors.Is(err, ErrLegacyQuoteSchema) {
		t.Fatalf("UpsertQuotes() error = %v", err)
	}
	if _, err := store.CountByAppIDPlatformSide(context.Background(), 252490, "steam", "ask"); !errors.Is(err, ErrLegacyQuoteSchema) {
		t.Fatalf("Count() error = %v", err)
	}
	if _, err := store.ListByAppIDPlatformSide(context.Background(), 252490, "steam", "ask", 5); !errors.Is(err, ErrLegacyQuoteSchema) {
		t.Fatalf("List() error = %v", err)
	}
}
