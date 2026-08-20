package steam

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

func admitTestRequest(context.Context) (ratelimit.Admission, error) {
	return ratelimit.Admission{}, nil
}

type stubOpener struct {
	cookie string
}

func (s stubOpener) Open(context.Context, resource.Lease) (string, string, error) {
	return s.cookie, "", nil
}

type stubCatalog struct {
	products []catalog.SteamProduct
}

type stubSessionRecorder struct {
	err    error
	states []bool
}

func (recorder *stubSessionRecorder) Record(_ context.Context, _ resource.Lease, valid bool) error {
	recorder.states = append(recorder.states, valid)
	return recorder.err
}

func (s stubCatalog) ListSteamProductsAfter(_ context.Context, appID int64, after catalog.ProductID, limit int) ([]catalog.SteamProduct, error) {
	out := make([]catalog.SteamProduct, 0)
	for _, product := range s.products {
		if product.AppID != appID || product.ProductID <= after {
			continue
		}
		out = append(out, product)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func TestFetchAskSendsSteamPriceRange(t *testing.T) {
	var got url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/market/search/render/", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = io.WriteString(w, `{"success":true,"start":0,"pagesize":10,"total_count":0,"results":[]}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	fetcher := mustFetcher(t, server.URL, stubCatalog{})
	payload, err := collection.EncodeAskPage(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	minCents := int64(1000)
	maxCents := int64(879769)
	_, err = fetcher.FetchPage(context.Background(), collection.PageFetch{
		TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
		AppID: 252490, Side: market.SideAsk, Kind: collection.TaskKindAskPage, Payload: payload,
		PriceRange:     collection.PriceRange{MinCents: &minCents, MaxCents: &maxCents},
		AdmitRequest:   admitTestRequest,
		RequestStarted: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("price_min") != "1000" || got.Get("price_max") != "879769" || got.Get("price_currency") != "23" {
		t.Fatalf("query=%v", got)
	}
}

func TestFetchAskSendsSteamFacets(t *testing.T) {
	var got url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/market/search/render/", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = io.WriteString(w, `{"success":true,"start":0,"pagesize":10,"total_count":0,"results":[]}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	fetcher := mustFetcher(t, server.URL, stubCatalog{})
	payload, err := collection.EncodeAskPage(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetcher.FetchPage(context.Background(), collection.PageFetch{
		TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
		AppID: 252490, Side: market.SideAsk, Kind: collection.TaskKindAskPage, Payload: payload,
		SteamFacets: collection.SteamFacets{
			Cats:    []string{"steamcat.armor"},
			Classes: []string{"burlap.trousers"},
		},
		AdmitRequest:   admitTestRequest,
		RequestStarted: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("category_steamcat") != "steamcat.armor" || got.Get("category_itemclass") != "burlap.trousers" {
		t.Fatalf("query=%v", got)
	}
}

func TestFetchAskPageAndEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/market/search/render/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") != "0" {
			_, _ = io.WriteString(w, `{"success":true,"start":10,"pagesize":10,"total_count":11,"results":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{
			"success":true,"start":0,"pagesize":10,"total_count":11,
			"results":[{
				"name":"x","hash_name":"Sealed Graffiti | Tilt (Desert Amber)",
				"sell_listings":646,"sell_price":21,"sell_price_text":"¥ 0.21","sale_price_text":"¥ 0.14",
				"asset_description":{"appid":730,"market_hash_name":"Sealed Graffiti | Tilt (Desert Amber)"}
			}]
		}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	fetcher := mustFetcher(t, server.URL, stubCatalog{})
	firstPayload, err := collection.EncodeAskPage(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	page, err := fetcher.FetchPage(context.Background(), collection.PageFetch{
		TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
		AppID: 730, Side: market.SideAsk, Kind: collection.TaskKindAskPage, Payload: firstPayload,
		AdmitRequest:   admitTestRequest,
		RequestStarted: func() {},
	})
	if err != nil || len(page.Attempts) != 1 || page.TotalCount != 11 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Attempts[0].ExactName != "Sealed Graffiti | Tilt (Desert Amber)" || page.Attempts[0].Observation.Summary.PriceCents != 21 {
		t.Fatalf("attempt=%+v", page.Attempts[0])
	}
	nextPayload, err := collection.EncodeAskPage(10, 10)
	if err != nil {
		t.Fatal(err)
	}
	next, err := fetcher.FetchPage(context.Background(), collection.PageFetch{
		TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
		AppID: 730, Side: market.SideAsk, Kind: collection.TaskKindAskPage, Payload: nextPayload,
		AdmitRequest:   admitTestRequest,
		RequestStarted: func() {},
	})
	if err != nil || len(next.Attempts) != 0 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
}

func TestFetchAskDollarMarksSessionInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"success":true,"start":0,"pagesize":10,"total_count":1,
			"results":[{
				"name":"x","hash_name":"Sealed Graffiti | Tilt (Desert Amber)",
				"sell_listings":1,"sell_price":21,"sell_price_text":"$0.21","sale_price_text":"$0.14",
				"asset_description":{"appid":730,"market_hash_name":"Sealed Graffiti | Tilt (Desert Amber)"}
			}]
		}`)
	}))
	t.Cleanup(server.Close)
	fetcher := mustFetcher(t, server.URL, stubCatalog{})
	_, err := fetcher.FetchPage(context.Background(), collection.PageFetch{
		TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
		AppID: 730, Side: market.SideAsk, Kind: collection.TaskKindAskPage,
		AdmitRequest:   admitTestRequest,
		RequestStarted: func() {},
	})
	if !errors.Is(err, collection.ErrFetchSessionInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestRequestLooksLikeBrowser(t *testing.T) {
	var got http.Header
	mux := http.NewServeMux()
	mux.HandleFunc("/market/search/render/", func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = io.WriteString(w, `{"success":true,"start":0,"pagesize":10,"total_count":0,"results":[]}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	fetcher := mustFetcher(t, server.URL, stubCatalog{})
	if _, err := fetcher.FetchPage(context.Background(), collection.PageFetch{
		TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
		AppID: 730, Side: market.SideAsk, Kind: collection.TaskKindAskPage,
		AdmitRequest:   admitTestRequest,
		RequestStarted: func() {},
	}); err != nil {
		t.Fatal(err)
	}
	agent := got.Get("User-Agent")
	if !strings.Contains(agent, "Chrome/") || strings.Contains(strings.ToLower(agent), "buffgo") {
		t.Fatalf("User-Agent = %q", agent)
	}
	for _, name := range []string{"Accept-Language", "Referer", "Sec-Fetch-Mode", "Sec-Ch-Ua-Platform"} {
		if got.Get(name) == "" {
			t.Fatalf("%s header is missing", name)
		}
	}
	if got.Get("Accept-Encoding") != "gzip" {
		t.Fatalf("Accept-Encoding = %q, want the transport default", got.Get("Accept-Encoding"))
	}
}

func TestFetchAskRateLimitAndLogin(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Content-Length", "10")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "null")
			return
		}
		w.Header().Set("Location", "https://steamcommunity.com/login/home/")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)
	fetcher := mustFetcher(t, server.URL, stubCatalog{})
	_, err := fetcher.FetchPage(context.Background(), collection.PageFetch{
		TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
		AppID: 730, Side: market.SideAsk, Kind: collection.TaskKindAskPage,
		AdmitRequest:   admitTestRequest,
		RequestStarted: func() {},
	})
	var signal *collection.RateLimitSignal
	if !errors.As(err, &signal) {
		t.Fatalf("err=%v", err)
	}
	if len(signal.Scopes) != 1 || signal.Scopes[0] != ratelimit.ScopeAccountIP {
		t.Fatalf("rate-limit scopes=%v", signal.Scopes)
	}
	_, err = fetcher.FetchPage(context.Background(), collection.PageFetch{
		TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
		AppID: 730, Side: market.SideAsk, Kind: collection.TaskKindAskPage,
		AdmitRequest:   admitTestRequest,
		RequestStarted: func() {},
	})
	if !errors.Is(err, collection.ErrFetchSessionInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestFetchReportsSessionRecorderFailures(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			_, _ = io.WriteString(w, `{"success":true,"start":0,"pagesize":10,"total_count":0,"results":[]}`)
			return
		}
		w.Header().Set("Location", "https://steamcommunity.com/login/home/")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)
	recordErr := errors.New("session store failed")
	recorder := &stubSessionRecorder{err: recordErr}
	fetcher, err := NewFetcher(Options{
		BaseURL: server.URL, Opener: stubOpener{cookie: "steamLoginSecure=ok"},
		Catalog: stubCatalog{}, Sessions: recorder,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, err := fetcher.FetchPage(context.Background(), collection.PageFetch{
			TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
			AppID: 730, Side: market.SideAsk, Kind: collection.TaskKindAskPage,
			AdmitRequest:   admitTestRequest,
			RequestStarted: func() {},
		})
		if !errors.Is(err, recordErr) || errors.Is(err, collection.ErrFetchSessionInvalid) {
			t.Fatalf("error = %v", err)
		}
	}
	if len(recorder.states) != 2 || !recorder.states[0] || recorder.states[1] {
		t.Fatalf("recorded states = %v", recorder.states)
	}
}

func TestFetchBidEmptyAndPresent(t *testing.T) {
	admissions := 0
	admit := func(context.Context) (ratelimit.Admission, error) {
		admissions++
		return ratelimit.Admission{}, nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/market/orderbook", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("qp"), "Missing") {
			_, _ = io.WriteString(w, `{"success":false}`)
			return
		}
		if strings.Contains(r.URL.Query().Get("qp"), "Empty") {
			_, _ = io.WriteString(w, `{"success":true,"data":{"amtMaxBuyOrder":null,"amtMinSellOrder":21,"eCurrency":23,"cBuyOrders":0,"cSellOrders":1,"rgCompactBuyOrders":[],"rgCompactSellOrders":[21,1]}}`)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{"amtMaxBuyOrder":27567,"amtMinSellOrder":27958,"eCurrency":23,"cBuyOrders":2,"cSellOrders":1,"rgCompactBuyOrders":[27567,4,27293,1],"rgCompactSellOrders":[27958,1]}}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	catalogItems := []catalog.SteamProduct{
		{ProductID: 1, AppID: 730, Name: "Empty Bid"},
		{ProductID: 2, AppID: 730, Name: "AK-47 | Redline (Field-Tested)"},
		{ProductID: 3, AppID: 730, Name: "Missing Item"},
	}
	fetcher := mustFetcher(t, server.URL, stubCatalog{products: catalogItems})
	for _, test := range []struct {
		after      catalog.ProductID
		status     market.ObservationStatus
		priceCents market.CNYCents
	}{
		{after: 0, status: market.StatusEmpty},
		{after: 1, status: market.StatusPresent, priceCents: 27567},
		{after: 2, status: market.StatusUnavailable},
	} {
		payload, err := collection.EncodeBidBatch(test.after, collection.BidBatchSize)
		if err != nil {
			t.Fatal(err)
		}
		page, err := fetcher.FetchPage(context.Background(), collection.PageFetch{
			TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
			AppID: 730, Side: market.SideBid, Kind: collection.TaskKindBidBatch, Payload: payload,
			AdmitRequest:   admit,
			RequestStarted: func() {},
		})
		if err != nil || len(page.Attempts) != 1 || page.Attempts[0].Observation.Status != test.status {
			t.Fatalf("after=%d page=%+v err=%v", test.after, page, err)
		}
		if test.priceCents != 0 && page.Attempts[0].Observation.Summary.PriceCents != test.priceCents {
			t.Fatalf("after=%d attempts=%+v", test.after, page.Attempts)
		}
	}
	if admissions != 3 {
		t.Fatalf("request admissions = %d, want one per HTTP request", admissions)
	}
}

func TestFetchBidForeignCurrencyMarksSessionInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"success":true,"data":{"amtMaxBuyOrder":4114,"amtMinSellOrder":4114,"eCurrency":1,"cBuyOrders":1,"cSellOrders":1,"rgCompactBuyOrders":[4114,1],"rgCompactSellOrders":[4114,1]}}`)
	}))
	t.Cleanup(server.Close)
	fetcher := mustFetcher(t, server.URL, stubCatalog{products: []catalog.SteamProduct{{ProductID: 1, AppID: 730, Name: "AK-47"}}})
	payload, err := collection.EncodeBidBatch(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetcher.FetchPage(context.Background(), collection.PageFetch{
		TaskType: collection.TaskTypeSummary, Platform: collection.PlatformSteam,
		AppID: 730, Side: market.SideBid, Kind: collection.TaskKindBidBatch, Payload: payload,
		AdmitRequest:   admitTestRequest,
		RequestStarted: func() {},
	})
	if !errors.Is(err, collection.ErrFetchSessionInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func mustFetcher(t *testing.T, base string, catalog stubCatalog) *Fetcher {
	t.Helper()
	fetcher, err := NewFetcher(Options{
		BaseURL: base, Opener: stubOpener{cookie: "steamLoginSecure=ok"}, Catalog: catalog,
		Clock: func() time.Time { return time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return fetcher
}
