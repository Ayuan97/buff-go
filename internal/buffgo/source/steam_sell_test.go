package source

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"buff-go/internal/buffgo/steam"
	"buff-go/internal/market"
)

type errorRoundTripper struct {
	err error
}

func (r errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, r.err
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/buffgo/source -> repo root
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func TestParseSteamMarketSell_RustFixture(t *testing.T) {
	root := findRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	offers, total, err := ParseSteamMarketSell(data, 252490, "USD", now)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("total_count: %d", total)
	}
	if len(offers) != 3 {
		t.Fatalf("offers: %d", len(offers))
	}
	o := offers[0]
	if o.MarketHashName != "Metal Facemask" || o.AppID != 252490 {
		t.Fatalf("first identity: %+v", o)
	}
	if o.Platform != PlatformSteam || o.Observation == nil || o.Observation.Side != SideAsk {
		t.Fatalf("platform/observation: %+v", o)
	}
	if o.ExactName != "" {
		t.Fatalf("raw Steam fields became verified exact name: %+v", o)
	}
	if o.Observation.Status != market.StatusFailed {
		t.Fatalf("unverified fixture must not become present: %+v", o.Observation)
	}
	if o.RawPriceMinor == nil || *o.RawPriceMinor != 1250 {
		t.Fatalf("raw price minor: %+v", o.RawPriceMinor)
	}
	if o.RawQuantity == nil || *o.RawQuantity != 40 {
		t.Fatalf("raw quantity: %+v", o.RawQuantity)
	}
	if o.RawCurrency != "USD" {
		t.Fatalf("raw currency: %q", o.RawCurrency)
	}
	if o.Source != SourceSteamSearch {
		t.Fatalf("source: %q want %q", o.Source, SourceSteamSearch)
	}
	if o.SourceMeta[MetaEndpoint] != "market/search/render" {
		t.Fatalf("endpoint: %+v", o.SourceMeta)
	}
	if !o.Observation.CollectedAt.Equal(now) || o.Observation.SourceTime != nil {
		t.Fatalf("observation time: %+v", o.Observation)
	}
	// Second row
	if offers[1].RawPriceMinor == nil || *offers[1].RawPriceMinor != 800 || offers[1].MarketHashName != "Road Sign Kilt" {
		t.Fatalf("second: %+v", offers[1])
	}
	if offers[2].RawPriceMinor == nil || *offers[2].RawPriceMinor != 350 || offers[2].RawQuantity == nil || *offers[2].RawQuantity != 100 {
		t.Fatalf("third: %+v", offers[2])
	}
}

func TestParseSteamMarketSell_CSFixture(t *testing.T) {
	root := findRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "testdata", "steam_sell_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	offers, _, err := ParseSteamMarketSell(data, 730, "USD", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 || offers[0].RawPriceMinor == nil || *offers[0].RawPriceMinor != 4200 {
		t.Fatalf("offers: %+v", offers)
	}
	if offers[0].AppID != 730 {
		t.Fatalf("appid: %d", offers[0].AppID)
	}
}

func TestParseSteamMarketSell_Unsuccessful(t *testing.T) {
	_, _, err := ParseSteamMarketSell([]byte(`{"success":false}`), 252490, "USD", time.Time{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInferCurrencyDoesNotTreatYenSymbolAsCNY(t *testing.T) {
	if got := inferCurrency("¥ 12.34", "CNY"); got != "" {
		t.Fatalf("ambiguous yen symbol = %q, want unknown", got)
	}
	if got := inferCurrency("$1.00", "CNY"); got != "USD" {
		t.Fatalf("dollar currency = %q", got)
	}
}

func TestSteamSellSource_FetchHTTPTest(t *testing.T) {
	root := findRepoRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	src := NewSteamSellSource(SteamSellOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})
	if src.Name() != NameSteamAsk {
		t.Fatalf("name: %s", src.Name())
	}

	offers, err := src.Fetch(context.Background(), Lease{}, JobSpec{
		Platform: PlatformSteam,
		Side:     SideAsk,
		AppID:    252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 3 {
		t.Fatalf("offers: %d", len(offers))
	}
	if !strings.Contains(gotQuery, "appid=252490") {
		t.Fatalf("query missing appid: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "norender=1") {
		t.Fatalf("query missing norender: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "sort_column=price") {
		t.Fatalf("expected price sort for sell: %s", gotQuery)
	}
}

func TestSteamSellSource_PreservesDistinctNameOnlyEvidence(t *testing.T) {
	payload := []byte(`{"success":true,"total_count":2,"results":[{"name":"A","appid":252490,"sell_price":100,"sell_price_text":"$1.00"},{"name":"B","appid":252490,"sell_price":200,"sell_price_text":"$2.00"}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	src := NewSteamSellSource(SteamSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client()})
	offers, err := src.Fetch(context.Background(), Lease{}, JobSpec{
		Platform: PlatformSteam, Side: SideAsk, AppID: 252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 2 || offers[0].NameRaw != "A" || offers[1].NameRaw != "B" {
		t.Fatalf("name-only evidence collapsed: %+v", offers)
	}
}

func TestSteamSellSource_SearchRateLimit(t *testing.T) {
	root := findRepoRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	lim := steam.NewMemorySearchLimiter(steam.SearchLimitConfig{
		SoftMax: 1, HardMax: 1, Window: time.Minute, CooldownOn429: time.Minute,
	})
	src := NewSteamSellSource(SteamSellOptions{
		BaseURL:       srv.URL,
		HTTPClient:    srv.Client(),
		SearchLimiter: lim,
		ProxyID:       "lease-proxy-1",
	})
	ctx := context.Background()
	if _, err := src.Pull(ctx, SteamSellOptions{AppID: 252490, MaxPages: 1}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err = src.Pull(ctx, SteamSellOptions{AppID: 252490, MaxPages: 1})
	if err == nil || !steam.IsSearchRateLimit(err) {
		t.Fatalf("want rate limit, got %v hits=%d", err, hits)
	}
	if hits != 1 {
		t.Fatalf("hits=%d want 1", hits)
	}

	// 429 path: new limiter + server returns 429 → cooldown error, process continues.
	hits429 := 0
	srv429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits429++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("response-body-secret"))
	}))
	defer srv429.Close()
	lim2 := steam.NewMemorySearchLimiter(steam.SearchLimitConfig{
		SoftMax: 80, HardMax: 100, Window: time.Minute, CooldownOn429: 3 * time.Minute,
	})
	src2 := NewSteamSellSource(SteamSellOptions{
		BaseURL: srv429.URL, HTTPClient: srv429.Client(), SearchLimiter: lim2,
		ProxyID: "http://proxy-user:proxy-secret@10.0.0.1:8080",
	})
	_, err = src2.Pull(ctx, SteamSellOptions{AppID: 252490})
	if err == nil || !steam.IsSearchRateLimit(err) {
		t.Fatalf("429: got %v", err)
	}
	for _, secret := range []string{"response-body-secret", "proxy-user", "proxy-secret", "10.0.0.1"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("429 error leaked %q: %v", secret, err)
		}
	}
	_, err = src2.Pull(ctx, SteamSellOptions{AppID: 252490})
	if err == nil || !steam.IsSearchRateLimit(err) {
		t.Fatalf("cooling: got %v", err)
	}
	// Direct key when lease empty.
	if _, err := src.Fetch(ctx, Lease{Proxy: "other"}, JobSpec{
		Platform: PlatformSteam, Side: SideAsk, AppID: 252490,
	}); err != nil {
		// other proxy has independent budget; first pull on other should work with shared lim soft=1 on lease-proxy-1 only
		// Wait - lim already exhausted for lease-proxy-1; Fetch with Proxy other uses opts.ProxyID from lease.
		// soft=1 was used by Pull with default ProxyID lease-proxy-1; "other" is free.
		// actually first Fetch above might fail if... we used Pull with source's proxyID.
		// Fetch passes lease.Proxy as opts.ProxyID which overrides. "other" should allow.
		t.Fatalf("other proxy: %v", err)
	}
}

func TestSteamSellSource_TransportErrorIsSafe(t *testing.T) {
	const secret = "Cookie=session-secret proxy=http://user:pass@10.0.0.2 token=query-secret"
	src := NewSteamSellSource(SteamSellOptions{
		BaseURL:    "https://example.test/market?token=query-secret",
		HTTPClient: &http.Client{Transport: errorRoundTripper{err: fmt.Errorf("transport failed: %s", secret)}},
	})
	_, err := src.Pull(context.Background(), SteamSellOptions{AppID: 252490, MaxPages: 1})
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "error_ref=detail_v1_") {
		t.Fatalf("unsafe transport error: %v", err)
	}
}

// TestSteamSellSource_PartialPullOnRateLimit (R9): first page ok, second hits budget →
// PartialPullError with offers from page 1 (not discarded).
func TestSteamSellSource_PartialPullOnRateLimit(t *testing.T) {
	root := findRepoRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Fixture total_count=3; use count=1 so multi-page continues past page 0.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		// Keep total_count high so Pull continues to page 2 under MaxPages=2.
		body := strings.Replace(string(payload), `"total_count":3`, `"total_count":200`, 1)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	lim := steam.NewMemorySearchLimiter(steam.SearchLimitConfig{
		SoftMax: 1, HardMax: 1, Window: time.Minute, CooldownOn429: time.Minute,
		MinInterval: time.Nanosecond,
	})
	src := NewSteamSellSource(SteamSellOptions{
		BaseURL: srv.URL, HTTPClient: srv.Client(), SearchLimiter: lim, ProxyID: "p-partial",
	})
	offers, err := src.Pull(context.Background(), SteamSellOptions{
		AppID: 252490, Count: 1, MaxPages: 2, PageDelay: time.Millisecond,
	})
	if err == nil {
		t.Fatal("want partial rate-limit error")
	}
	pe, ok := AsPartialPull(err)
	if !ok {
		t.Fatalf("want PartialPullError, got %T %v", err, err)
	}
	if len(pe.Offers) == 0 {
		t.Fatal("partial offers empty")
	}
	if len(offers) == 0 {
		t.Fatal("Pull should also return partial offers as first value")
	}
	if !steam.IsSearchRateLimit(err) {
		t.Fatalf("IsSearchRateLimit: %v", err)
	}
	if hits != 1 {
		t.Fatalf("HTTP hits=%d want 1 (second page blocked before request)", hits)
	}
	if pe.Page != 1 {
		t.Fatalf("failed page=%d want 1", pe.Page)
	}
	if pe.Start != 1 {
		t.Fatalf("resume start=%d want 1", pe.Start)
	}
}

func TestSteamSellSource_MaxPagesZeroFetchesToTotal(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		start := r.URL.Query().Get("start")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"success":true,"start":%s,"total_count":3,"results":[{"name":"Item %s","hash_name":"Item %s","sell_price":100,"sell_price_text":"$1.00","sell_listings":1,"appid":252490}]}`, start, start, start)
	}))
	defer srv.Close()

	src := NewSteamSellSource(SteamSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client()})
	offers, err := src.Pull(context.Background(), SteamSellOptions{
		AppID: 252490, Count: 1, MaxPages: 0, PageDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 3 || len(offers) != 3 {
		t.Fatalf("full crawl hits=%d offers=%d want 3/3", hits, len(offers))
	}
	for i := 1; i < len(offers); i++ {
		previous := offers[i-1].Observation
		current := offers[i].Observation
		if previous == nil || current == nil || !previous.CollectedAt.Before(current.CollectedAt) {
			t.Fatalf("page collected_at must advance: previous=%+v current=%+v", previous, current)
		}
		if previous.SourceTime != nil || current.SourceTime != nil {
			t.Fatalf("Steam source_time is not present in the response: previous=%+v current=%+v", previous, current)
		}
	}

	hits = 0
	offers, err = src.Pull(context.Background(), SteamSellOptions{
		AppID: 252490, Count: 1, MaxPages: 2, PageDelay: time.Nanosecond,
	})
	if !errors.Is(err, ErrPageLimit) {
		t.Fatalf("capped crawl error = %v", err)
	}
	if hits != 2 || len(offers) != 2 {
		t.Fatalf("capped crawl hits=%d offers=%d want 2/2", hits, len(offers))
	}
	partial, ok := AsPartialPull(err)
	if !ok || partial.Start != 2 {
		t.Fatalf("partial = %+v ok=%v", partial, ok)
	}
}

func TestSteamSellSource_MissingTotalKeepsCompletedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"start":0,"total_count":0,"results":[{"name":"A","hash_name":"A","sell_price":100,"sell_price_text":"$1.00","sell_listings":1,"appid":252490}]}`))
	}))
	defer srv.Close()

	src := NewSteamSellSource(SteamSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client()})
	offers, err := src.Pull(context.Background(), SteamSellOptions{AppID: 252490, Count: 1, MaxPages: 0})
	partial, ok := AsPartialPull(err)
	if !ok || len(offers) != 1 || len(partial.Offers) != 1 || partial.Start != 1 {
		t.Fatalf("offers=%d partial=%+v ok=%v err=%v", len(offers), partial, ok, err)
	}
}

func TestSteamSellSource_KnownTotalDoesNotStopOnShortPage(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		start := r.URL.Query().Get("start")
		_, _ = fmt.Fprintf(w, `{"success":true,"start":%s,"total_count":3,"results":[{"name":"Item %s","hash_name":"Item %s","sell_price":100,"sell_price_text":"$1.00","sell_listings":1,"appid":252490}]}`, start, start, start)
	}))
	defer srv.Close()

	src := NewSteamSellSource(SteamSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client()})
	offers, err := src.Pull(context.Background(), SteamSellOptions{AppID: 252490, Count: 2, MaxPages: 0, PageDelay: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 2 || len(offers) != 2 {
		t.Fatalf("hits=%d offers=%d want 2/2", hits, len(offers))
	}
}

func TestSteamSellSource_CancelDuringPageDelayKeepsCompletedPage(t *testing.T) {
	firstPage := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("start")
		_, _ = fmt.Fprintf(w, `{"success":true,"start":%s,"total_count":2,"results":[{"name":"Item %s","hash_name":"Item %s","sell_price":100,"sell_price_text":"$1.00","sell_listings":1,"appid":252490}]}`, start, start, start)
		if start == "0" {
			close(firstPage)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-firstPage
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	src := NewSteamSellSource(SteamSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client()})
	offers, err := src.Pull(ctx, SteamSellOptions{AppID: 252490, Count: 1, MaxPages: 0, PageDelay: time.Second})
	partial, ok := AsPartialPull(err)
	if !ok || !errors.Is(err, context.Canceled) || len(offers) != 1 || len(partial.Offers) != 1 || partial.Start != 1 {
		t.Fatalf("offers=%d partial=%+v ok=%v err=%v", len(offers), partial, ok, err)
	}
}

func TestAsPartialPull_Nil(t *testing.T) {
	if _, ok := AsPartialPull(nil); ok {
		t.Fatal("nil")
	}
	if _, ok := AsPartialPull(context.Canceled); ok {
		t.Fatal("not partial")
	}
}

// TestSetHTTPClient_NilResetsDefault ensures SetHTTPClient(nil) is safe.
func TestSetHTTPClient_NilResetsDefault(t *testing.T) {
	src := NewSteamSellSource(SteamSellOptions{})
	src.SetHTTPClient(nil)
	src.SetProxyID("p1")
	if src.ProxyID() != "p1" {
		t.Fatalf("ProxyID: %q", src.ProxyID())
	}
}

func TestSetHTTPClient_Replace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"start":0,"total_count":0,"results":[]}`))
	}))
	defer srv.Close()
	src := NewSteamSellSource(SteamSellOptions{BaseURL: srv.URL})
	src.SetHTTPClient(srv.Client())
	// empty results still parse; ensure client is used (no network error to real Steam)
	_, err := src.Pull(context.Background(), SteamSellOptions{AppID: 252490, Count: 1, MaxPages: 1})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
}

func TestMatchSources(t *testing.T) {
	if !MatchSources("", NameSteamAsk) {
		t.Fatal("empty allowlist should allow all")
	}
	if !MatchSources("steam.ask", NameSteamAsk) {
		t.Fatal("exact match")
	}
	if !MatchSources("buff.ask, steam.ask", NameSteamAsk) {
		t.Fatal("list match")
	}
	if MatchSources("buff.ask", NameSteamAsk) {
		t.Fatal("should exclude")
	}
}

func TestRawOffer_Normalize(t *testing.T) {
	o := RawOffer{
		Platform:       "Steam",
		AppID:          252490,
		MarketHashName: "  Metal Facemask  ",
	}
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	if o.Platform != PlatformSteam || o.MarketHashName != "Metal Facemask" {
		t.Fatalf("%+v", o)
	}
	if o.RawCurrency != "" {
		t.Fatalf("Normalize invented raw defaults: %+v", o)
	}

	nameOnly := RawOffer{Platform: PlatformSteam, AppID: 252490, NameRaw: "  Display Name  "}
	if err := nameOnly.Normalize(); err != nil {
		t.Fatal(err)
	}
	if nameOnly.NameRaw != "Display Name" || nameOnly.MarketHashName != "" || nameOnly.ExactName != "" {
		t.Fatalf("Normalize promoted raw name into verified identity: %+v", nameOnly)
	}
}

func TestParseSteamMarketSell_DoesNotPromoteDisplayName(t *testing.T) {
	body := []byte(`{"success":true,"total_count":1,"results":[{"name":"Display Name","appid":252490,"sell_price":100,"sell_price_text":"$1.00"}]}`)
	offers, _, err := ParseSteamMarketSell(body, 252490, "USD", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 || offers[0].NameRaw != "Display Name" || offers[0].MarketHashName != "" || offers[0].ExactName != "" {
		t.Fatalf("raw identity was promoted: %+v", offers)
	}
}
