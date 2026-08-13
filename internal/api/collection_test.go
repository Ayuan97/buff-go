package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/storage/postgres"
)

type collectionServiceStub struct {
	created bool
	desired collection.DesiredState
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
	err error
}

func (s *marketServiceStub) ListQuotes(context.Context, int64, string, int) ([]postgres.MarketQuote, error) {
	if s.err != nil {
		return nil, s.err
	}
	cents := market.CNYCents(21)
	return []postgres.MarketQuote{{
		ProductID: 1, AppID: 730, Name: "Sealed Graffiti | Tilt (Desert Amber)",
		Platform: "steam", Side: market.SideAsk, Status: market.StatusPresent,
		CollectedAt: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), PresentCents: &cents,
	}}, nil
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
		{name: "runs", method: http.MethodGet, path: "/api/runs", status: http.StatusOK},
		{name: "quotes", method: http.MethodGet, path: "/api/quotes?appid=730&platform=steam", status: http.StatusOK},
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
