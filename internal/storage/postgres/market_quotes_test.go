package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"buff-go/internal/market"
)

func TestQuoteItemTypeFilter(t *testing.T) {
	types, apply := quoteItemTypeFilter(MarketQuoteFilter{})
	if apply || len(types) != 0 {
		t.Fatalf("empty filter apply=%v types=%v", apply, types)
	}

	types, apply = quoteItemTypeFilter(MarketQuoteFilter{ItemTypes: []string{"Hoodie"}, SteamCats: []string{"steamcat.clothing"}})
	if !apply || len(types) != 1 || types[0] != "Hoodie" {
		t.Fatalf("clothing+hoodie = %v apply=%v", types, apply)
	}

	types, apply = quoteItemTypeFilter(MarketQuoteFilter{ItemTypes: []string{"Hoodie"}, SteamCats: []string{"steamcat.armor"}})
	if !apply || len(types) != 0 {
		t.Fatalf("armor+hoodie intersection = %v apply=%v", types, apply)
	}

	types, apply = quoteItemTypeFilter(MarketQuoteFilter{SteamCats: []string{"steamcat.armor"}})
	if !apply || len(types) == 0 {
		t.Fatalf("armor labels = %v apply=%v", types, apply)
	}
}

func TestQuoteOrderByDropDesc(t *testing.T) {
	got := quoteOrderBy(QuoteSortDropDesc)
	if !strings.Contains(got, "DESC NULLS LAST") || !strings.Contains(got, "tk.high_cents") {
		t.Fatalf("drop sort = %q", got)
	}
}

func TestQuoteOrderByDropPctDesc(t *testing.T) {
	got := quoteOrderBy(QuoteSortDropPctDesc)
	if !strings.Contains(got, "10000") || !strings.Contains(got, "DESC NULLS LAST") {
		t.Fatalf("drop pct sort = %q", got)
	}
}

func TestQuoteWhereDropsOnly(t *testing.T) {
	where, args := quoteWhere(MarketQuoteFilter{DropsOnly: true}, 1)
	if !strings.Contains(where, "tk.high_cents") || len(args) != 0 {
		t.Fatalf("drops-only where=%q args=%v", where, args)
	}
}

func TestQuoteWhereMinDrop(t *testing.T) {
	min := int64(150)
	where, args := quoteWhere(MarketQuoteFilter{DropsOnly: true, MinDropCents: &min}, 1)
	if !strings.Contains(where, ">=") || !strings.Contains(where, quoteDropExpr) ||
		len(args) != 1 || args[0] != int64(150) {
		t.Fatalf("min-drop where=%q args=%v", where, args)
	}
}

func TestDropSince(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	if got := dropSince("", now); !got.Equal(now.Add(-24 * time.Hour)) {
		t.Fatalf("default window = %s", got)
	}
	if got := dropSince(DropWindow7d, now); !got.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("7d window = %s", got)
	}
	if got := dropSince(DropWindow30d, now); !got.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("30d window = %s", got)
	}
}

func TestQuoteFromPlacesSinceFirst(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	from, where, args := quoteFrom(MarketQuoteFilter{DropsOnly: true, AppID: 252490}, quoteTicksGrouped, now)
	if !strings.Contains(from, "market_price_ticks") || !strings.Contains(where, "$2") {
		t.Fatalf("from=%s where=%s", from, where)
	}
	if len(args) != 2 || args[0] != now || args[1] != int64(252490) {
		t.Fatalf("args=%v", args)
	}
}

func TestQuoteTickModes(t *testing.T) {
	minDrop := int64(1)
	cases := []struct {
		name      string
		filter    MarketQuoteFilter
		countMode quoteTickJoinMode
		listMode  quoteTickJoinMode
	}{
		{name: "ordinary sort", countMode: quoteTicksNone, listMode: quoteTicksLateral},
		{name: "drops filter", filter: MarketQuoteFilter{DropsOnly: true}, countMode: quoteTicksGrouped, listMode: quoteTicksGrouped},
		{name: "drop threshold", filter: MarketQuoteFilter{MinDropCents: &minDrop}, countMode: quoteTicksGrouped, listMode: quoteTicksGrouped},
		{name: "drop sort", filter: MarketQuoteFilter{Sort: QuoteSortDropDesc}, countMode: quoteTicksNone, listMode: quoteTicksGrouped},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := quoteCountTickMode(testCase.filter); got != testCase.countMode {
				t.Fatalf("count mode = %d, want %d", got, testCase.countMode)
			}
			if got := quoteListTickMode(testCase.filter); got != testCase.listMode {
				t.Fatalf("list mode = %d, want %d", got, testCase.listMode)
			}
		})
	}
}

func TestGroupedQuoteTickJoinGroupsByQuoteKey(t *testing.T) {
	for _, predicate := range []string{
		"LEFT JOIN (",
		"GROUP BY t.product_id, t.platform, t.side",
		"tk.product_id = a.product_id",
		"tk.platform = a.platform",
		"tk.side = a.side",
	} {
		if !strings.Contains(quoteTickGroupedJoin, predicate) {
			t.Fatalf("grouped quote tick join missing %q: %s", predicate, quoteTickGroupedJoin)
		}
	}
}

func TestQuoteTickJoinAggregatesOnlyCurrentQuote(t *testing.T) {
	for _, predicate := range []string{
		"LEFT JOIN LATERAL",
		"t.product_id = a.product_id",
		"t.platform = a.platform",
		"t.side = a.side",
		"t.collected_at >= $1",
		"HAVING COUNT(*) > 0",
		") tk ON TRUE",
	} {
		if !strings.Contains(quoteTickJoin, predicate) {
			t.Fatalf("quote tick join missing %q: %s", predicate, quoteTickJoin)
		}
	}
	if strings.Contains(quoteTickJoin, "GROUP BY") {
		t.Fatalf("quote tick join still pre-aggregates the full window: %s", quoteTickJoin)
	}
}

func TestMarketQueryErrorPreservesClassificationAndCause(t *testing.T) {
	cause := errors.New("database unavailable")
	err := marketQueryError(context.Background(), "list quotes", cause)
	if !errors.Is(err, market.ErrStorage) || !errors.Is(err, cause) {
		t.Fatalf("error chain = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := marketQueryError(canceled, "list quotes", cause); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
}
