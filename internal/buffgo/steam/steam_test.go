package steam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseSearchPage(t *testing.T) {
	body := []byte(`{
		"success": true,
		"start": 0,
		"pagesize": 10,
		"total_count": 2,
		"results": [
			{
				"name": "Cloth",
				"hash_name": "Cloth",
				"sell_listings": 100,
				"sell_price": 16,
				"sell_price_text": "$0.16",
				"sale_price_text": "$0.15",
				"app_name": "Rust",
				"asset_description": {
					"appid": 252490,
					"classid": "1",
					"icon_url": "x",
					"market_hash_name": "Cloth",
					"commodity": 1,
					"type": ""
				}
			},
			{
				"name": "Metal",
				"hash_name": "Metal",
				"sell_listings": 50,
				"sell_price": 85,
				"sell_price_text": "$0.85",
				"asset_description": {
					"appid": 252490,
					"classid": "2",
					"market_hash_name": "Metal",
					"commodity": 1
				}
			}
		]
	}`)
	page, err := ParseSearchPage(body, 252490)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 2 || len(page.Results) != 2 {
		t.Fatalf("%+v", page)
	}
	if page.Results[0].SellPriceCents != 16 || page.Results[0].MarketHashName != "Cloth" {
		t.Fatalf("first: %+v", page.Results[0])
	}
	if MinorToMajor(page.Results[0].SellPriceCents) != 0.16 {
		t.Fatalf("major: %v", MinorToMajor(16))
	}
}

func TestParseSearchPageDoesNotInventHashFromDisplayName(t *testing.T) {
	page, err := ParseSearchPage([]byte(`{"success":true,"results":[{"name":"Display Name","asset_description":{"appid":252490}}]}`), 252490)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Results) != 1 || page.Results[0].Name != "Display Name" || page.Results[0].MarketHashName != "" {
		t.Fatalf("display name was promoted: %+v", page.Results)
	}
}

func TestParseHistogram(t *testing.T) {
	body := []byte(`{
		"success": 1,
		"highest_buy_order": "15614",
		"lowest_sell_order": "18419",
		"price_prefix": "$",
		"price_suffix": "",
		"buy_order_graph": [[156.14, 2, "2 buy orders"]],
		"sell_order_graph": [[184.19, 1, "1 sell orders"]]
	}`)
	ob, err := ParseItemOrdersHistogram(body, ItemBookParams{
		AppID: 252490, MarketHashName: "Christmas Lights", ItemNameID: "175945821", Currency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ob.LowestSell != 18419 || ob.HighestBuy != 15614 {
		t.Fatalf("best: sell=%d buy=%d", ob.LowestSell, ob.HighestBuy)
	}
	bp := BestPricesFromBook(ob)
	if bp.LowestSellMajor != 184.19 || bp.HighestBuyMajor != 156.14 {
		t.Fatalf("major: %+v", bp)
	}
	if bp.SpreadMinor != 18419-15614 {
		t.Fatalf("spread: %d", bp.SpreadMinor)
	}
	if len(ob.SellGraph) != 1 || ob.SellGraph[0].Price != 184.19 {
		t.Fatalf("graph: %+v", ob.SellGraph)
	}
}

func TestParseOrderBook(t *testing.T) {
	body := []byte(`{
		"success": true,
		"data": {
			"amtMaxBuyOrder": 19966,
			"amtMinSellOrder": 23552,
			"eCurrency": 13,
			"cBuyOrders": 100,
			"cSellOrders": 14,
			"rgCompactBuyOrders": [19966, 2, 19964, 5],
			"rgCompactSellOrders": [23552, 1, 24852, 1]
		}
	}`)
	ob, err := ParseOrderBookJSON(body, 252490, "Christmas Lights")
	if err != nil {
		t.Fatal(err)
	}
	if ob.LowestSell != 23552 || ob.HighestBuy != 19966 || ob.SellOrders != 14 {
		t.Fatalf("%+v", ob)
	}
	if len(ob.SellCompact) != 2 || ob.SellCompact[0].Quantity != 1 {
		t.Fatalf("compact: %+v", ob.SellCompact)
	}
}

func TestParsePriceHistory(t *testing.T) {
	body := []byte(`{
		"success": true,
		"price_prefix": "¥",
		"price_suffix": "",
		"prices": [
			["Dec 21 2017 01: +0", 44.863, "50"],
			["Dec 22 2017 01: +0", 38.346, "90"]
		]
	}`)
	h, err := ParsePriceHistory(body, 252490, "Christmas Lights")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Points) != 2 || h.Points[0].Price != 44.863 || h.Points[0].Volume != "50" {
		t.Fatalf("%+v", h.Points)
	}
	if _, err := ParsePriceHistory([]byte("[]"), 1, "x"); err == nil {
		t.Fatal("expected error for empty history")
	}
}

func TestParseItemNameID(t *testing.T) {
	html := []byte(`<script>Market_LoadOrderSpread( 175945821 );</script>`)
	id, err := ParseItemNameID(html)
	if err != nil || id != "175945821" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestParseCookieJSON(t *testing.T) {
	raw := []byte(`[
		{"name":"sessionid","value":"abc","domain":"steamcommunity.com"},
		{"name":"steamLoginSecure","value":"tok","domain":"steamcommunity.com"}
	]`)
	h, err := ParseCookieJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, "sessionid=abc") || !strings.Contains(h, "steamLoginSecure=tok") {
		t.Fatalf("header: %q", h)
	}
}

func TestClientSearch_httptest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "search/render") {
			t.Fatalf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("appid") != "252490" {
			t.Fatalf("appid: %s", r.URL.Query().Get("appid"))
		}
		if r.URL.Query().Get("sort_column") != "price" {
			t.Fatalf("sort: %s", r.URL.Query().Get("sort_column"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "start": 0, "pagesize": 10, "total_count": 1,
			"results": []map[string]any{
				{
					"name": "Cloth", "hash_name": "Cloth", "sell_listings": 9, "sell_price": 16,
					"sell_price_text": "$0.16",
					"asset_description": map[string]any{
						"appid": 252490, "market_hash_name": "Cloth", "commodity": 1, "classid": "1",
					},
				},
			},
		})
	}))
	defer srv.Close()

	// inject base by calling Parse only — Search uses hard-coded host.
	// Exercise client get via custom transport: rewrite to test server.
	c := NewClient(Options{HTTPClient: &http.Client{Transport: hostRewrite{to: srv.URL}}})

	page, err := c.Search(context.Background(), SearchParams{
		AppID: 252490, Count: 10, SortColumn: "price", SortDir: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Results) != 1 || page.Results[0].SellPriceCents != 16 {
		t.Fatalf("%+v", page)
	}
}

func TestClientSearch_HTTPErrorDoesNotExposeResponse(t *testing.T) {
	const secret = "response-secret Cookie=session-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(secret))
	}))
	defer srv.Close()

	c := NewClient(Options{HTTPClient: &http.Client{Transport: hostRewrite{to: srv.URL}}})
	_, err := c.Search(context.Background(), SearchParams{AppID: 252490})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "http status 500") {
		t.Fatalf("unsafe HTTP error: %v", err)
	}
}

func TestClientSearch_RateLimitBudget(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "start": 0, "pagesize": 1, "total_count": 1,
			"results": []map[string]any{
				{"name": "X", "hash_name": "X", "sell_listings": 1, "sell_price": 10,
					"asset_description": map[string]any{"appid": 252490, "market_hash_name": "X", "commodity": 1}},
			},
		})
	}))
	defer srv.Close()

	lim := NewMemorySearchLimiter(SearchLimitConfig{
		SoftMax: 2, HardMax: 2, Window: time.Minute, CooldownOn429: time.Minute,
		MinInterval: time.Nanosecond,
	})
	c := NewClient(Options{
		HTTPClient:    &http.Client{Transport: hostRewrite{to: srv.URL}},
		SearchLimiter: lim,
		ProxyID:       "p1",
	})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := c.Search(ctx, SearchParams{AppID: 252490, Count: 1}); err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
	}
	if _, err := c.Search(ctx, SearchParams{AppID: 252490, Count: 1}); !IsSearchRateLimit(err) {
		t.Fatalf("want budget, got %v hits=%d", err, hits)
	}
	if hits != 2 {
		t.Fatalf("http hits=%d want 2 (third blocked before request)", hits)
	}
}

func TestClientSearch_HTTP429Cooldown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("null"))
	}))
	defer srv.Close()

	lim := NewMemorySearchLimiter(SearchLimitConfig{
		SoftMax: 80, HardMax: 100, Window: time.Minute, CooldownOn429: 3 * time.Minute,
		MinInterval: time.Nanosecond,
	})
	c := NewClient(Options{
		HTTPClient:    &http.Client{Transport: hostRewrite{to: srv.URL}},
		SearchLimiter: lim,
	})
	ctx := context.Background()
	_, err := c.Search(ctx, SearchParams{AppID: 252490, Count: 1})
	if !IsSearchRateLimit(err) {
		t.Fatalf("want cooling from 429, got %v", err)
	}
	// Second call blocked by cooldown without needing another 429 path success.
	_, err = c.Search(ctx, SearchParams{AppID: 252490, Count: 1})
	if !errors.Is(err, ErrSearchCooling) {
		t.Fatalf("want ErrSearchCooling, got %v", err)
	}
}

type hostRewrite struct{ to string }

func (h hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := http.NewRequest(req.Method, h.to+req.URL.RequestURI(), req.Body)
	if err != nil {
		return nil, err
	}
	u.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(u)
}
