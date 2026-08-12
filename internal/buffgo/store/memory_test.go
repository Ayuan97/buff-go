package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"buff-go/internal/buffgo/source"
	"buff-go/internal/market"
)

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func TestQuoteNormalizeRejectsInvalidMarketValues(t *testing.T) {
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	negative := int64(-1)
	tests := []struct {
		name  string
		quote Quote
	}{
		{name: "missing item", quote: Quote{AppID: 252490, Platform: "steam", Side: market.SideAsk, CollectedAt: collectedAt}},
		{name: "missing appid", quote: Quote{ItemID: 1, Platform: "steam", Side: market.SideAsk, CollectedAt: collectedAt}},
		{name: "missing platform", quote: Quote{ItemID: 1, AppID: 252490, Side: market.SideAsk, CollectedAt: collectedAt}},
		{name: "invalid side", quote: Quote{ItemID: 1, AppID: 252490, Platform: "steam", Side: market.Side("sell"), CollectedAt: collectedAt}},
		{name: "negative cents", quote: Quote{ItemID: 1, AppID: 252490, Platform: "steam", Side: market.SideAsk, PriceCents: -1, CollectedAt: collectedAt}},
		{name: "negative count", quote: Quote{ItemID: 1, AppID: 252490, Platform: "steam", Side: market.SideAsk, OrderCount: &negative, CollectedAt: collectedAt}},
		{name: "missing collected time", quote: Quote{ItemID: 1, AppID: 252490, Platform: "steam", Side: market.SideAsk}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quote := tt.quote
			if err := quote.Normalize(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestMemoryQuoteStoreOrdersBidAndAskCorrectly(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryQuoteStore()
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	quotes := []Quote{
		{ItemID: 1, AppID: 252490, Platform: "steam", Side: market.SideAsk, PriceCents: 1250, CollectedAt: collectedAt},
		{ItemID: 2, AppID: 252490, Platform: "steam", Side: market.SideAsk, PriceCents: 800, CollectedAt: collectedAt},
		{ItemID: 1, AppID: 252490, Platform: "steam", Side: market.SideBid, PriceCents: 700, CollectedAt: collectedAt},
		{ItemID: 2, AppID: 252490, Platform: "steam", Side: market.SideBid, PriceCents: 900, CollectedAt: collectedAt},
	}
	if _, err := store.UpsertQuotes(ctx, quotes); err != nil {
		t.Fatal(err)
	}
	asks, err := store.ListByAppIDPlatformSide(ctx, 252490, "steam", "ask", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(asks) != 2 || asks[0].PriceCents != 800 || asks[1].PriceCents != 1250 {
		t.Fatalf("asks = %+v", asks)
	}
	bids, err := store.ListByAppIDPlatformSide(ctx, 252490, "steam", "bid", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bids) != 2 || bids[0].PriceCents != 900 || bids[1].PriceCents != 700 {
		t.Fatalf("bids = %+v", bids)
	}
}

func TestMemoryQuoteStoreLastWriteWinsPerSide(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryQuoteStore()
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	result, err := store.UpsertQuotes(ctx, []Quote{
		{ItemID: 1, AppID: 252490, Platform: "steam", Side: market.SideAsk, PriceCents: 1250, CollectedAt: collectedAt},
		{ItemID: 1, AppID: 252490, Platform: "steam", Side: market.SideAsk, PriceCents: 1200, CollectedAt: collectedAt.Add(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written != 1 || store.Len() != 1 {
		t.Fatalf("result=%+v len=%d", result, store.Len())
	}
	quote, ok := store.Get(1, "steam", market.SideAsk)
	if !ok || quote.PriceCents != 1200 {
		t.Fatalf("quote=%+v ok=%v", quote, ok)
	}
}

func TestSteamUSDFixtureCannotEnterUnifiedMarket(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(findRepoRoot(t), "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	offers, _, err := source.ParseSteamMarketSell(data, 252490, "USD", time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryQuoteStore()
	for i, offer := range offers {
		if _, err := QuoteFromOffer(int64(i+1), offer); err == nil {
			t.Fatalf("offer %d unexpectedly became a CNY quote", i)
		}
	}
	if store.Len() != 0 {
		t.Fatalf("unverified fixture wrote %d quotes", store.Len())
	}
}

func TestNonPresentAttemptDoesNotOverwriteLastPresent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryQuoteStore()
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	present := source.RawOffer{
		Platform: "steam", AppID: 252490, MarketHashName: "A",
		Observation: presentObservation(t, market.SideAsk, 1250, collectedAt),
	}
	quote, err := QuoteFromOffer(1, present)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertQuotes(ctx, []Quote{quote}); err != nil {
		t.Fatal(err)
	}
	failed := source.RawOffer{
		Platform: "steam", AppID: 252490, MarketHashName: "A",
		Observation: &market.Observation{
			Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: collectedAt.Add(time.Minute),
		},
	}
	if _, err := QuoteFromOffer(1, failed); err == nil {
		t.Fatal("failed attempt unexpectedly became a price")
	}
	got, ok := store.Get(1, "steam", market.SideAsk)
	if !ok || got.PriceCents != 1250 || !got.CollectedAt.Equal(collectedAt) {
		t.Fatalf("last present changed: %+v ok=%v", got, ok)
	}
}

func TestUpsertQuotesNilAndEmpty(t *testing.T) {
	ctx := context.Background()
	var nilStore *QuoteStore
	if _, err := nilStore.UpsertQuotes(ctx, []Quote{{ItemID: 1}}); err == nil {
		t.Fatal("expected nil store error")
	}
	store := NewMemoryQuoteStore()
	result, err := store.UpsertQuotes(ctx, nil)
	if err != nil || result.Written != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
