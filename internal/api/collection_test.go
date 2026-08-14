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
	"buff-go/internal/storage/postgres"
)

type collectionServiceStub struct {
	created bool
	deleted bool
	desired collection.DesiredState
	sort    collection.SortOrder
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

func (s *collectionServiceStub) PageSummaries(_ context.Context, id collection.RunID) ([]postgres.PageSummary, error) {
	if s.err != nil {
		return nil, s.err
	}
	if id != 9 {
		return nil, collection.ErrNotFound
	}
	return []postgres.PageSummary{{
		PageSequence: 1,
		CursorBefore: "",
		CursorAfter:  "10",
		CollectedAt:  time.Date(2026, 8, 13, 12, 0, 2, 0, time.UTC),
		CommittedAt:  time.Date(2026, 8, 13, 12, 0, 3, 0, time.UTC),
		PayloadBytes: 10398,
		AccountID:    1,
		AccountAlias: "steam",
		ExitAddress:  "38.175.103.188",
	}}, nil
}

func (s *collectionServiceStub) PagePayload(_ context.Context, id collection.RunID, sequence collection.Sequence) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if id != 9 || sequence != 1 {
		return nil, postgres.ErrPagePayloadNotFound
	}
	return []byte(`{"success":true}`), nil
}

func (s *collectionServiceStub) PageAttempts(_ context.Context, id collection.RunID, sequence collection.Sequence) ([]postgres.PageAttempt, error) {
	if s.err != nil {
		return nil, s.err
	}
	if id != 9 || sequence != 1 {
		return nil, collection.ErrNotFound
	}
	cents := int64(21)
	return []postgres.PageAttempt{{
		ProductID: 3, AppID: 730, Name: "Sealed Graffiti | GLHF (SWAT Blue)",
		Platform: "steam", Side: "ask", Status: "present",
		CollectedAt: time.Date(2026, 8, 13, 12, 0, 2, 0, time.UTC),
		SourceTime:  ptrTime(time.Date(2026, 8, 13, 12, 0, 2, 0, time.UTC)),
		PriceCents:  &cents,
	}}, nil
}

func (s *collectionServiceStub) ListRecentRuns(context.Context, int) ([]collection.Run, error) {
	if s.err != nil {
		return nil, s.err
	}
	run, err := collection.NewSummaryRun(collection.SummaryRunInput{
		ID: 9, TargetID: 4, Platform: "steam", AppID: 730, Side: market.SideAsk,
		SwitchVersion: 2, RunSequence: 1, State: collection.RunRunning,
		CreatedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		StartedAt: ptrTime(time.Date(2026, 8, 13, 12, 0, 1, 0, time.UTC)),
	})
	if err != nil {
		panic(err)
	}
	return []collection.Run{run}, nil
}

type marketServiceStub struct {
	err    error
	filter postgres.MarketQuoteFilter
}

func (s *marketServiceStub) ListQuotes(_ context.Context, filter postgres.MarketQuoteFilter) (postgres.MarketQuoteResult, error) {
	if s.err != nil {
		return postgres.MarketQuoteResult{}, s.err
	}
	s.filter = filter
	cents := market.CNYCents(21)
	return postgres.MarketQuoteResult{
		Total: 1,
		Quotes: []postgres.MarketQuote{{
			ProductID: 1, AppID: 730, Name: "Sealed Graffiti | Tilt (Desert Amber)",
			Media: catalog.ProductMedia{
				IconPath: "6TMcQ7eX6E0EZl2byXi7vaVKyDk", ItemType: "Base Grade Container", NameColor: "a7ec2e",
			},
			Platform: "steam", Side: market.SideAsk, Status: market.StatusPresent,
			CollectedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), PresentCents: &cents,
		}},
	}, nil
}

func (s *marketServiceStub) QuoteFacets(context.Context, int64) (postgres.MarketFacets, error) {
	if s.err != nil {
		return postgres.MarketFacets{}, s.err
	}
	return postgres.MarketFacets{AppIDs: []int64{730}, ItemTypes: []string{"Base Grade Container"}}, nil
}

func ptrTime(value time.Time) *time.Time { return &value }

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
		{name: "delete", method: http.MethodPost, path: "/api/targets/4/delete", status: http.StatusOK},
		{name: "delete-rejects-body", method: http.MethodPost, path: "/api/targets/4/delete", body: `{}`, status: http.StatusBadRequest},
		{name: "runs", method: http.MethodGet, path: "/api/runs", status: http.StatusOK},
		{name: "run-pages", method: http.MethodGet, path: "/api/runs/9/pages", status: http.StatusOK},
		{name: "page-payload", method: http.MethodGet, path: "/api/runs/9/pages/1/payload", status: http.StatusOK},
		{name: "page-attempts", method: http.MethodGet, path: "/api/runs/9/pages/1/attempts", status: http.StatusOK},
		{name: "page-payload-evicted", method: http.MethodGet, path: "/api/runs/9/pages/7/payload", status: http.StatusNotFound},
		{name: "page-bad-sequence", method: http.MethodGet, path: "/api/runs/9/pages/0/payload", status: http.StatusBadRequest},
		{name: "page-bad-run", method: http.MethodGet, path: "/api/runs/abc/pages", status: http.StatusBadRequest},
		{name: "page-unknown-leaf", method: http.MethodGet, path: "/api/runs/9/pages/1/raw", status: http.StatusNotFound},
		{name: "page-post-rejected", method: http.MethodPost, path: "/api/runs/9/pages", status: http.StatusMethodNotAllowed},
		{name: "quotes", method: http.MethodGet, path: "/api/quotes?appid=730&platform=steam", status: http.StatusOK},
		{name: "quote-facets", method: http.MethodGet, path: "/api/quote-facets?appid=730", status: http.StatusOK},
		{name: "quotes-bad-price", method: http.MethodGet, path: "/api/quotes?min_cents=-1", status: http.StatusBadRequest},
		{name: "quotes-bad-offset", method: http.MethodGet, path: "/api/quotes?offset=-1", status: http.StatusBadRequest},
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
		"http://localhost/api/quotes?appid=730&platform=steam&side=ask&keyword=Tilt&item_type=Base%20Grade%20Container"+
			"&min_cents=10&max_cents=5000&sort=price_asc&limit=24&offset=48", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	got := quotes.filter
	if got.AppID != 730 || got.Platform != "steam" || got.Side != market.SideAsk ||
		got.Keyword != "Tilt" || got.ItemType != "Base Grade Container" ||
		got.Sort != postgres.QuoteSortPriceAsc || got.Limit != 24 || got.Offset != 48 {
		t.Fatalf("filter = %+v", got)
	}
	if got.MinCents == nil || *got.MinCents != 10 || got.MaxCents == nil || *got.MaxCents != 5000 {
		t.Fatalf("price range = %v %v", got.MinCents, got.MaxCents)
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

// 原始响应必须逐字节回传，控制台只展示不改写。
func TestPagePayloadIsReturnedVerbatim(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{Collection: &collectionServiceStub{}})
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/runs/9/pages/1/payload", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"success":true}` {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff header = %q", response.Header().Get("X-Content-Type-Options"))
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
