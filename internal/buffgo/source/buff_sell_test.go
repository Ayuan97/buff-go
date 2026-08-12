package source

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"buff-go/internal/market"
)

func TestParseBuffGoodsSell_RustFixture(t *testing.T) {
	root := findRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "testdata", "rust_buff_sell_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	offers, totalPages, err := ParseBuffGoodsSell(data, 252490, "CNY", now)
	if err != nil {
		t.Fatal(err)
	}
	if totalPages != 1 {
		t.Fatalf("total_page: %d", totalPages)
	}
	if len(offers) != 3 {
		t.Fatalf("offers: %d", len(offers))
	}
	o := offers[0]
	if o.MarketHashName != "Metal Facemask" || o.AppID != 252490 {
		t.Fatalf("first identity: %+v", o)
	}
	if o.Platform != PlatformBuff || o.Observation == nil || o.Observation.Side != SideAsk {
		t.Fatalf("platform/observation: %+v", o)
	}
	if o.ExactName != "" {
		t.Fatalf("raw BUFF fields became verified exact name: %+v", o)
	}
	if o.PlatformItemID != "90001" {
		t.Fatalf("goods id: %q", o.PlatformItemID)
	}
	if o.RawPriceText != "88.50" {
		t.Fatalf("raw price text: %q", o.RawPriceText)
	}
	if o.RawQuantity == nil || *o.RawQuantity != 12 {
		t.Fatalf("raw quantity: %+v", o.RawQuantity)
	}
	if o.RawCurrency != "CNY" {
		t.Fatalf("raw currency hint: %q", o.RawCurrency)
	}
	if o.Observation.Status != market.StatusFailed || o.Observation.Summary != nil {
		t.Fatalf("BUFF fixture lacks currency evidence and must not become present: %+v", o.Observation)
	}
	if o.Source != SourceBuffGoods {
		t.Fatalf("source: %q want %q", o.Source, SourceBuffGoods)
	}
	if o.SourceMeta[MetaEndpoint] != "market/goods/selling" {
		t.Fatalf("endpoint: %+v", o.SourceMeta)
	}
	if !o.Observation.CollectedAt.Equal(now) || o.Observation.SourceTime != nil {
		t.Fatalf("observation time: %+v", o.Observation)
	}
	if offers[1].RawPriceText != "56.00" || offers[1].MarketHashName != "Road Sign Kilt" {
		t.Fatalf("second: %+v", offers[1])
	}
	if offers[2].RawPriceText != "24.75" || offers[2].RawQuantity == nil || *offers[2].RawQuantity != 30 {
		t.Fatalf("third: %+v", offers[2])
	}
}

func TestParseBuffGoodsSell_CSFixture(t *testing.T) {
	root := findRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "testdata", "buff_sell_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	offers, _, err := ParseBuffGoodsSell(data, 730, "CNY", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 || offers[0].RawPriceText != "45.30" {
		t.Fatalf("offers: %+v", offers)
	}
	if offers[0].AppID != 730 || offers[0].PlatformItemID != "22345" {
		t.Fatalf("identity: %+v", offers[0])
	}
}

func TestParseBuffGoodsSell_LoginRequired(t *testing.T) {
	_, _, err := ParseBuffGoodsSell([]byte(`{"code":"Login Required","data":{"items":[]}}`), 252490, "CNY", time.Time{})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "Login Required") || !strings.Contains(err.Error(), "detail_ref=detail_v1_") {
		t.Fatalf("error: %v", err)
	}
}

func TestParseBuffGoodsSell_DoesNotPromoteDisplayName(t *testing.T) {
	body := []byte(`{"code":"OK","data":{"items":[{"id":1,"appid":252490,"name":"Display Name","sell_min_price":"1.00"}],"total_page":1}}`)
	offers, _, err := ParseBuffGoodsSell(body, 252490, "CNY", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 || offers[0].NameRaw != "Display Name" || offers[0].MarketHashName != "" || offers[0].ExactName != "" {
		t.Fatalf("raw identity was promoted: %+v", offers)
	}
}

func TestParseBuffGoodsSell_InvalidPriceDoesNotExposeValue(t *testing.T) {
	const secret = "sessionid=reflected-secret"
	body := []byte(`{"code":"OK","data":{"items":[{"id":1,"market_hash_name":"Item","sell_min_price":"` + secret + `","appid":252490}]}}`)
	_, _, err := ParseBuffGoodsSell(body, 252490, "CNY", time.Time{})
	if err == nil {
		t.Fatal("expected invalid price error")
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "detail_ref=detail_v1_") {
		t.Fatalf("unsafe invalid price error: %v", err)
	}
}

func TestBuffGameFromAppID(t *testing.T) {
	if BuffGameFromAppID(252490) != "rust" {
		t.Fatal("rust")
	}
	if BuffGameFromAppID(730) != "csgo" {
		t.Fatal("csgo")
	}
	if BuffGameFromAppID(999) != "" {
		t.Fatal("unknown")
	}
}

func TestBuffSellSource_FetchHTTPTest(t *testing.T) {
	root := findRepoRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_buff_sell_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gotQuery, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	src := NewBuffSellSource(BuffSellOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Cookie:     "session=test-session; csrf_token=abc",
	})
	if src.Name() != NameBuffAsk {
		t.Fatalf("name: %s", src.Name())
	}

	offers, err := src.Fetch(context.Background(), Lease{}, JobSpec{
		Platform: PlatformBuff,
		Side:     SideAsk,
		AppID:    252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 3 {
		t.Fatalf("offers: %d", len(offers))
	}
	if !strings.Contains(gotQuery, "game=rust") {
		t.Fatalf("query missing game=rust: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "page_num=1") {
		t.Fatalf("query missing page_num: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "sort_by=price.asc") {
		t.Fatalf("expected price.asc sort: %s", gotQuery)
	}
	if !strings.Contains(gotCookie, "session=test-session") {
		t.Fatalf("cookie not sent: %q", gotCookie)
	}
}

func TestBuffSellSource_PreservesDistinctNameOnlyEvidence(t *testing.T) {
	payload := []byte(`{"code":"OK","data":{"items":[{"name":"A","appid":252490,"sell_min_price":"1.00"},{"name":"B","appid":252490,"sell_min_price":"2.00"}],"total_page":1}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	src := NewBuffSellSource(BuffSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client(), Cookie: "session=test"})
	offers, err := src.Fetch(context.Background(), Lease{}, JobSpec{
		Platform: PlatformBuff, Side: SideAsk, AppID: 252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 2 || offers[0].NameRaw != "A" || offers[1].NameRaw != "B" {
		t.Fatalf("name-only evidence collapsed: %+v", offers)
	}
}

func TestBuffSellSource_LeaseAccountOverridesCookie(t *testing.T) {
	root := findRepoRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_buff_sell_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	src := NewBuffSellSource(BuffSellOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Cookie:     "session=from-source",
	})
	_, err = src.Fetch(context.Background(), Lease{Account: "session=from-lease"}, JobSpec{
		Platform: PlatformBuff, Side: SideAsk, AppID: 252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotCookie != "session=from-lease" {
		t.Fatalf("lease cookie: %q", gotCookie)
	}
}

func TestMatchSources_BuffSell(t *testing.T) {
	if !MatchSources("buff.ask", NameBuffAsk) {
		t.Fatal("exact")
	}
	if !MatchSources("steam.ask,buff.ask", NameBuffAsk) {
		t.Fatal("list")
	}
	if MatchSources("steam.ask", NameBuffAsk) {
		t.Fatal("exclude")
	}
}

func TestBuffSellSource_SetHTTPClient(t *testing.T) {
	src := NewBuffSellSource(BuffSellOptions{Cookie: "session=x"})
	if src.client == nil {
		t.Fatal("default client")
	}
	custom := &http.Client{Timeout: 5 * time.Second}
	src.SetHTTPClient(custom)
	if src.client != custom {
		t.Fatal("SetHTTPClient did not replace client")
	}
	src.SetHTTPClient(nil)
	if src.client == nil || src.client.Timeout != 30*time.Second {
		t.Fatalf("nil reset: %+v", src.client)
	}
}

func TestBuffSellSource_HTTP429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"Rate Limit","echo":"session-secret-response"}`))
	}))
	defer srv.Close()

	src := NewBuffSellSource(BuffSellOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Cookie:     "session=x",
	})
	_, err := src.Pull(context.Background(), BuffSellOptions{AppID: 252490})
	if err == nil {
		t.Fatal("expected 429 error")
	}
	if !IsBuffRateLimited(err) {
		t.Fatalf("IsBuffRateLimited: %v", err)
	}
	if !strings.Contains(err.Error(), "http 429") {
		t.Fatalf("error text: %v", err)
	}
	if strings.Contains(err.Error(), "session-secret-response") {
		t.Fatalf("response body leaked: %v", err)
	}
}

func TestBuffSellSource_MaxPagesZeroFetchesToTotal(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits > 1 {
			time.Sleep(time.Millisecond)
		}
		page := r.URL.Query().Get("page_num")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"code":"OK","data":{"items":[{"id":%s,"name":"Item %s","market_hash_name":"Item %s","sell_min_price":"1.00","sell_num":1,"appid":252490}],"total_page":3,"page_num":%s,"page_size":1}}`, page, page, page, page)
	}))
	defer srv.Close()

	src := NewBuffSellSource(BuffSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client(), Cookie: "session=x"})
	offers, err := src.Pull(context.Background(), BuffSellOptions{AppID: 252490, PageSize: 1, MaxPages: 0})
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
			t.Fatalf("BUFF source_time is not present in the response: previous=%+v current=%+v", previous, current)
		}
	}

	hits = 0
	offers, err = src.Pull(context.Background(), BuffSellOptions{AppID: 252490, PageSize: 1, MaxPages: 2})
	if !errors.Is(err, ErrPageLimit) {
		t.Fatalf("capped crawl error = %v", err)
	}
	if hits != 2 || len(offers) != 2 {
		t.Fatalf("capped crawl hits=%d offers=%d want 2/2", hits, len(offers))
	}
	partial, ok := AsBuffPartialPull(err)
	if !ok || partial.NextPage != 3 {
		t.Fatalf("partial = %+v ok=%v", partial, ok)
	}
}

func TestBuffSellSource_MissingTotalKeepsCompletedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"OK","data":{"items":[{"id":1,"name":"A","market_hash_name":"A","sell_min_price":"1.00","sell_num":1,"appid":252490}],"total_page":0,"page_num":1,"page_size":1}}`))
	}))
	defer srv.Close()

	src := NewBuffSellSource(BuffSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client(), Cookie: "session=x"})
	offers, err := src.Pull(context.Background(), BuffSellOptions{AppID: 252490, PageSize: 1, MaxPages: 0})
	partial, ok := AsBuffPartialPull(err)
	if !ok || len(offers) != 1 || len(partial.Offers) != 1 || partial.NextPage != 2 {
		t.Fatalf("offers=%d partial=%+v ok=%v err=%v", len(offers), partial, ok, err)
	}
}

func TestBuffSellSource_KnownTotalDoesNotStopOnShortPage(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		page := r.URL.Query().Get("page_num")
		_, _ = fmt.Fprintf(w, `{"code":"OK","data":{"items":[{"id":%s,"name":"Item %s","market_hash_name":"Item %s","sell_min_price":"1.00","sell_num":1,"appid":252490}],"total_page":3,"page_num":%s,"page_size":2}}`, page, page, page, page)
	}))
	defer srv.Close()

	src := NewBuffSellSource(BuffSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client(), Cookie: "session=x"})
	offers, err := src.Pull(context.Background(), BuffSellOptions{AppID: 252490, PageSize: 2, MaxPages: 0})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 3 || len(offers) != 3 {
		t.Fatalf("hits=%d offers=%d want 3/3", hits, len(offers))
	}
}

func TestBuffSellSource_LaterFailureKeepsCompletedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page_num")
		if page == "2" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"OK","data":{"items":[{"id":1,"name":"Item 1","market_hash_name":"Item 1","sell_min_price":"1.00","sell_num":1,"appid":252490}],"total_page":3,"page_num":1,"page_size":1}}`))
	}))
	defer srv.Close()

	src := NewBuffSellSource(BuffSellOptions{BaseURL: srv.URL, HTTPClient: srv.Client(), Cookie: "session=x"})
	offers, err := src.Pull(context.Background(), BuffSellOptions{AppID: 252490, PageSize: 1, MaxPages: 0})
	if err == nil {
		t.Fatal("want partial error")
	}
	pe, ok := AsBuffPartialPull(err)
	if !ok {
		t.Fatalf("want BuffPartialPullError, got %T %v", err, err)
	}
	if len(offers) != 1 || len(pe.Offers) != 1 || pe.NextPage != 2 {
		t.Fatalf("offers=%d partial=%d next=%d", len(offers), len(pe.Offers), pe.NextPage)
	}
	if !IsBuffRateLimited(err) {
		t.Fatalf("want rate-limit cause: %v", err)
	}
}

func TestIsBuffRateLimited(t *testing.T) {
	if IsBuffRateLimited(nil) {
		t.Fatal("nil")
	}
	if !IsBuffRateLimited(ErrHTTP429) {
		t.Fatal("sentinel")
	}
	if !IsBuffRateLimited(fmt.Errorf("fetch: %w", ErrHTTP429)) {
		t.Fatal("wrapped")
	}
	if !IsBuffRateLimited(fmt.Errorf("buff.sell http 429: too many")) {
		t.Fatal("string")
	}
	if IsBuffRateLimited(fmt.Errorf("buff.sell http 500: oops")) {
		t.Fatal("500 not rate limited")
	}
}
