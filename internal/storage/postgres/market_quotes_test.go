package postgres

import (
	"strings"
	"testing"
	"time"
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
	from, where, args := quoteFrom(MarketQuoteFilter{DropsOnly: true, AppID: 252490}, true, now)
	if !strings.Contains(from, "market_price_ticks") || !strings.Contains(where, "$2") {
		t.Fatalf("from=%s where=%s", from, where)
	}
	if len(args) != 2 || args[0] != now || args[1] != int64(252490) {
		t.Fatalf("args=%v", args)
	}
}
