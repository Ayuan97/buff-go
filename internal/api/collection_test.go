package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

type collectionServiceStub struct {
	created bool
	deleted bool
	desired collection.DesiredState
	sort    collection.SortOrder
	bounds  collection.PriceRange
	facets  collection.SteamFacets
	err     error
}

func (s *collectionServiceStub) sample() collection.Target {
	recheck := time.Date(2026, 8, 13, 12, 1, 0, 0, time.UTC)
	target, err := collection.NewSummaryTarget(collection.SummaryTargetInput{
		ID: 4, Revision: 2, Platform: "steam", AppID: 730, Side: market.SideAsk,
		Desired: collection.DesiredEnabled, Actual: collection.ActualWaiting,
		SwitchVersion: 2, Reason: collection.TargetReasonNextCycle,
		Recovery: collection.RecoveryAutomatic, RecheckAt: &recheck,
		ChangedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		panic(err)
	}
	return target
}

func (s *collectionServiceStub) ListTargets(context.Context) ([]collection.Target, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []collection.Target{s.sample()}, nil
}

func (s *collectionServiceStub) Target(_ context.Context, id collection.TargetID) (collection.Target, bool, error) {
	if s.err != nil {
		return collection.Target{}, false, s.err
	}
	if id != 4 {
		return collection.Target{}, false, nil
	}
	return s.sample(), true, nil
}

func (s *collectionServiceStub) CreateSummaryTarget(_ context.Context, _ collection.Platform, _ int64, _ market.Side, desired collection.DesiredState) (collection.Target, error) {
	if s.err != nil {
		return collection.Target{}, s.err
	}
	s.created = true
	s.desired = desired
	return s.sample(), nil
}

func (s *collectionServiceStub) SetTargetDesired(_ context.Context, id collection.TargetID, _ collection.Revision, desired collection.DesiredState) (collection.Target, error) {
	if s.err != nil {
		return collection.Target{}, s.err
	}
	if id != 4 {
		return collection.Target{}, collection.ErrNotFound
	}
	s.desired = desired
	return s.sample(), nil
}

func (s *collectionServiceStub) SetTargetSortOrder(_ context.Context, id collection.TargetID, _ collection.Revision, order collection.SortOrder) (collection.Target, error) {
	if s.err != nil {
		return collection.Target{}, s.err
	}
	if id != 4 {
		return collection.Target{}, collection.ErrNotFound
	}
	s.sort = order
	return s.sample(), nil
}

func (s *collectionServiceStub) SetTargetPriceRange(_ context.Context, id collection.TargetID, _ collection.Revision, bounds collection.PriceRange) (collection.Target, error) {
	if s.err != nil {
		return collection.Target{}, s.err
	}
	if id != 4 {
		return collection.Target{}, collection.ErrNotFound
	}
	s.bounds = bounds
	return s.sample(), nil
}

func (s *collectionServiceStub) SetTargetSteamFacets(_ context.Context, id collection.TargetID, _ collection.Revision, facets collection.SteamFacets) (collection.Target, error) {
	if s.err != nil {
		return collection.Target{}, s.err
	}
	if id != 4 {
		return collection.Target{}, collection.ErrNotFound
	}
	s.facets = facets
	return s.sample(), nil
}

func (s *collectionServiceStub) DeleteTarget(_ context.Context, id collection.TargetID) error {
	if s.err != nil {
		return s.err
	}
	if id != 4 {
		return collection.ErrNotFound
	}
	s.deleted = true
	return nil
}

func (s *collectionServiceStub) ListWorkers(context.Context) ([]collection.WorkerSnapshot, error) {
	if s.err != nil {
		return nil, s.err
	}
	cents := int64(21)
	return []collection.WorkerSnapshot{{
		Combination: resource.AccountNodeCombination{
			ID: 1, Platform: "steam", AccountID: 2, NodeID: 3,
		},
		AccountAlias: "steam",
		SessionState: resource.AccountSessionStateValid,
		NodeName:     "hk-1",
		ExitAddress:  "38.175.103.188",
		Region:       resource.NodeRegionHongKong,
		Idle:         false,
		Claim: &collection.WorkerClaim{
			TargetID: 4, TaskID: 9, AppID: 730, Side: market.SideAsk, Platform: "steam",
			Kind: collection.TaskKindAskPage, Endpoint: "market_summary",
			ClaimedAt: time.Date(2026, 8, 15, 0, 59, 0, 0, time.UTC), Active: true,
			Items: []collection.WorkerItem{{ProductID: 3, Name: "Sealed Graffiti | GLHF (SWAT Blue)", Status: "present", PriceCents: &cents}},
		},
		ActiveWaits: []collection.WorkerWait{{
			Scope: collection.WorkerWaitScopeRate, Reason: collection.WorkerWaitReasonDeferred,
			RetryAt: time.Date(2026, 8, 15, 1, 1, 0, 0, time.UTC), Platform: "steam",
			Endpoint: "market_orderbook", Side: market.SideBid, AccountID: 2,
			ExitAddress: "38.175.103.188",
		}},
		LastPage: &collection.WorkerPage{
			TargetID: 4, AppID: 730, Side: market.SideAsk, Platform: "steam",
			CommittedAt: time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC),
			Items:       []collection.WorkerItem{{ProductID: 3, Name: "Sealed Graffiti | GLHF (SWAT Blue)", Status: "present", PriceCents: &cents}},
		},
	}}, nil
}

type marketServiceStub struct {
	err    error
	filter market.QuoteFilter
	ticks  market.PriceTickFilter
}

func (s *marketServiceStub) ListQuotes(_ context.Context, filter market.QuoteFilter) (market.QuoteResult, error) {
	if s.err != nil {
		return market.QuoteResult{}, s.err
	}
	s.filter = filter
	cents := market.CNYCents(21)
	return market.QuoteResult{
		Total: 1,
		Quotes: []market.Quote{{
			ProductID: 1, AppID: 730, Name: "Sealed Graffiti | Tilt (Desert Amber)",
			Media: catalog.ProductMedia{
				IconPath: "6TMcQ7eX6E0EZl2byXi7vaVKyDk", ItemType: "Base Grade Container", NameColor: "a7ec2e",
			},
			Platform: "steam", Side: market.SideAsk, Status: market.StatusPresent,
			CollectedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), PresentCents: &cents,
		}},
	}, nil
}

func (s *marketServiceStub) QuoteFacets(context.Context, int64) (market.QuoteFacets, error) {
	if s.err != nil {
		return market.QuoteFacets{}, s.err
	}
	return market.QuoteFacets{AppIDs: []int64{730}, ItemTypes: []string{"Base Grade Container"}}, nil
}

func (s *marketServiceStub) ListPriceTicks(_ context.Context, filter market.PriceTickFilter) ([]market.PriceTick, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.ticks = filter
	prev := int64(20)
	return []market.PriceTick{{
		TickID: 1, ProductID: 1, AppID: 730, Name: "Sealed Graffiti | Tilt (Desert Amber)",
		Platform: "steam", Side: market.SideAsk, PrevCents: &prev, PriceCents: 21,
		CollectedAt: time.Date(2026, 8, 15, 4, 0, 0, 0, time.UTC),
	}}, nil
}

func TestCollectionRoutes(t *testing.T) {
	targets := &collectionServiceStub{}
	handler := NewHandlerForAuthority(nil, "", ControlServices{Collection: targets, Market: &marketServiceStub{}})
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "list-targets", method: http.MethodGet, path: "/api/targets", status: http.StatusOK},
		{name: "get-target", method: http.MethodGet, path: "/api/targets/4", status: http.StatusOK},
		{name: "create", method: http.MethodPost, path: "/api/targets", body: `{"platform":"steam","appid":730,"side":"ask","desired":"enabled"}`, status: http.StatusCreated},
		{name: "desired", method: http.MethodPost, path: "/api/targets/4/desired", body: `{"expected_revision":2,"desired":"disabled"}`, status: http.StatusOK},
		{name: "sort", method: http.MethodPost, path: "/api/targets/4/sort", body: `{"expected_revision":2,"sort_column":"quantity","sort_dir":"desc"}`, status: http.StatusOK},
		{name: "price-range", method: http.MethodPost, path: "/api/targets/4/price-range", body: `{"expected_revision":2,"min_cents":1000,"max_cents":879769}`, status: http.StatusOK},
		{name: "steam-facets", method: http.MethodPost, path: "/api/targets/4/steam-facets", body: `{"expected_revision":2,"steam_cats":["steamcat.armor"],"item_classes":["hoodie"]}`, status: http.StatusOK},
		{name: "steam-vocab", method: http.MethodGet, path: "/api/steam-facets?appid=252490", status: http.StatusOK},
		{name: "delete", method: http.MethodPost, path: "/api/targets/4/delete", status: http.StatusOK},
		{name: "delete-rejects-body", method: http.MethodPost, path: "/api/targets/4/delete", body: `{}`, status: http.StatusBadRequest},
		{name: "workers", method: http.MethodGet, path: "/api/workers", status: http.StatusOK},
		{name: "workers-post-rejected", method: http.MethodPost, path: "/api/workers", status: http.StatusMethodNotAllowed},
		{name: "quotes", method: http.MethodGet, path: "/api/quotes?appid=730&platform=steam", status: http.StatusOK},
		{name: "quote-facets", method: http.MethodGet, path: "/api/quote-facets?appid=730", status: http.StatusOK},
		{name: "quotes-bad-price", method: http.MethodGet, path: "/api/quotes?min_cents=-1", status: http.StatusBadRequest},
		{name: "quotes-bad-offset", method: http.MethodGet, path: "/api/quotes?offset=-1", status: http.StatusBadRequest},
		{name: "price-ticks", method: http.MethodGet, path: "/api/price-ticks?product_id=9&limit=20", status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://localhost"+test.path, strings.NewReader(test.body))
			request.Host = "localhost"
			request.Header.Set("Origin", "http://localhost")
			request.Header.Set("Content-Type", "application/json")
			if test.method == http.MethodPost {
				cookie, token := issueContext(t, handler)
				request.AddCookie(cookie)
				request.Header.Set(CSRFHeaderName, token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var payload map[string]any
			if test.name == "get-target" || test.name == "create" || test.name == "desired" {
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if payload["platform"] != "steam" || payload["actual"] != "waiting" {
					t.Fatalf("payload=%v", payload)
				}
			}
		})
	}
	if !targets.created {
		t.Fatal("create was not called")
	}
	if !targets.deleted {
		t.Fatal("delete was not called")
	}
	if targets.sort != (collection.SortOrder{Column: collection.SortColumnQuantity, Direction: collection.SortDescending}) {
		t.Fatalf("sort order = %+v", targets.sort)
	}
	if len(targets.facets.Cats) != 1 || targets.facets.Cats[0] != "steamcat.armor" ||
		len(targets.facets.Classes) != 1 || targets.facets.Classes[0] != "hoodie" {
		t.Fatalf("steam facets = %+v", targets.facets)
	}
}

func TestCollectionDeleteConflictMapping(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "not stopped", err: postgres.ErrTargetNotStopped, code: "target_not_stopped"},
		{name: "already ran", err: postgres.ErrTargetInUse, code: "collection_in_use"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandlerForAuthority(nil, "", ControlServices{Collection: &collectionServiceStub{err: test.err}})
			cookie, token := issueContext(t, handler)
			request := httptest.NewRequest(http.MethodPost, "http://localhost/api/targets/4/delete", nil)
			request.Host = "localhost"
			request.Header.Set("Origin", "http://localhost")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(CSRFHeaderName, token)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

// 筛选条件必须原样交给存储层，不能像以前那样在前端对截断结果上过滤。
func TestQuoteFiltersReachStorage(t *testing.T) {
	quotes := &marketServiceStub{}
	handler := NewHandlerForAuthority(nil, "", ControlServices{Market: quotes})
	request := httptest.NewRequest(http.MethodGet,
		"http://localhost/api/quotes?appid=730&product_id=9&platform=steam&side=ask&keyword=Tilt&item_type=Base%20Grade%20Container"+
			"&item_types=Hoodie&item_types=AK47u&steam_cats=steamcat.clothing"+
			"&min_cents=10&max_cents=5000&dropped=1&drop_window=7d&min_drop_cents=150&sort=drop_pct_desc&limit=24&offset=48", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	got := quotes.filter
	if got.AppID != 730 || got.ProductID != 9 || got.Platform != "steam" || got.Side != market.SideAsk ||
		got.Keyword != "Tilt" || got.ItemType != "Base Grade Container" ||
		len(got.ItemTypes) != 2 || got.ItemTypes[0] != "Hoodie" || got.ItemTypes[1] != "AK47u" ||
		len(got.SteamCats) != 1 || got.SteamCats[0] != "steamcat.clothing" ||
		!got.DropsOnly || got.DropWindow != market.DropWindow7d ||
		got.Sort != market.QuoteSortDropPctDesc || got.Limit != 24 || got.Offset != 48 {
		t.Fatalf("filter = %+v", got)
	}
	if got.MinCents == nil || *got.MinCents != 10 || got.MaxCents == nil || *got.MaxCents != 5000 {
		t.Fatalf("price range = %v %v", got.MinCents, got.MaxCents)
	}
	if got.MinDropCents == nil || *got.MinDropCents != 150 {
		t.Fatalf("min drop = %v", got.MinDropCents)
	}
	var payload struct {
		Total  int64 `json:"total"`
		Quotes []struct {
			IconPath  string `json:"icon_path"`
			ItemType  string `json:"item_type"`
			NameColor string `json:"name_color"`
		} `json:"quotes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != 1 || len(payload.Quotes) != 1 || payload.Quotes[0].IconPath == "" ||
		payload.Quotes[0].ItemType == "" || payload.Quotes[0].NameColor == "" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestMarketStorageUnavailable(t *testing.T) {
	for _, path := range []string{
		"/api/quotes",
		"/api/quote-facets",
		"/api/price-ticks",
	} {
		t.Run(path, func(t *testing.T) {
			handler := NewHandlerForAuthority(nil, "", ControlServices{Market: &marketServiceStub{err: market.ErrStorage}})
			request := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
			request.Host = "localhost"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "market_storage_unavailable") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPriceTickFiltersReachStorage(t *testing.T) {
	quotes := &marketServiceStub{}
	handler := NewHandlerForAuthority(nil, "", ControlServices{Market: quotes})
	request := httptest.NewRequest(http.MethodGet,
		"http://localhost/api/price-ticks?product_id=9&platform=steam&side=ask&limit=20", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	got := quotes.ticks
	if got.ProductID != 9 || got.Platform != "steam" || got.Side != market.SideAsk || got.Limit != 20 {
		t.Fatalf("filter = %+v", got)
	}
	var payload struct {
		Ticks []struct {
			PriceCents int64 `json:"price_cents"`
		} `json:"ticks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Ticks) != 1 || payload.Ticks[0].PriceCents != 21 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestWorkersJSON(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{Collection: &collectionServiceStub{}})
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/workers", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload[0]["account_alias"] != "steam" || payload[0]["idle"] != false {
		t.Fatalf("payload=%v", payload)
	}
	if _, ok := payload[0]["last_page"].(map[string]any); !ok {
		t.Fatalf("last_page missing: %v", payload[0])
	}
	claim, ok := payload[0]["claim"].(map[string]any)
	if !ok || claim["active"] != true || claim["endpoint"] != "market_summary" || claim["task_id"] != float64(9) {
		t.Fatalf("active claim = %v", payload[0]["claim"])
	}
	waits, ok := payload[0]["active_waits"].([]any)
	if !ok || len(waits) != 1 {
		t.Fatalf("active_waits = %v", payload[0]["active_waits"])
	}
	wait, ok := waits[0].(map[string]any)
	if !ok || wait["scope"] != "account_exit_endpoint" || wait["reason"] != "deferred" ||
		wait["endpoint"] != "market_orderbook" || wait["side"] != "bid" {
		t.Fatalf("wait = %v", waits[0])
	}
}

func TestSteamFacetsVocab(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{})
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/steam-facets?appid=252490", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Categories []struct {
			Slug string `json:"slug"`
		} `json:"categories"`
		ItemClasses []struct {
			Slug string `json:"slug"`
		} `json:"item_classes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Categories) == 0 || len(payload.ItemClasses) == 0 {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestCollectionNotFoundJSON(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{Collection: &collectionServiceStub{}})
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/targets/99", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}
