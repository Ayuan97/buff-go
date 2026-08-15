package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

type combinationServiceStub struct {
	created bool
	deleted bool
	err     error
}

func (s *combinationServiceStub) sample() resource.AccountNodeCombination {
	return resource.AccountNodeCombination{ID: 4, Platform: "steam", AccountID: 3, NodeID: 7}
}

func (s *combinationServiceStub) ListCombinations(context.Context) ([]resource.AccountNodeCombination, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []resource.AccountNodeCombination{s.sample()}, nil
}

func (s *combinationServiceStub) Combination(_ context.Context, id resource.CombinationID) (resource.AccountNodeCombination, bool, error) {
	if s.err != nil {
		return resource.AccountNodeCombination{}, false, s.err
	}
	if id != 4 {
		return resource.AccountNodeCombination{}, false, nil
	}
	return s.sample(), true, nil
}

func (s *combinationServiceStub) CreateCombination(context.Context, resource.AccountID, resource.NodeID) (resource.AccountNodeCombination, error) {
	if s.err != nil {
		return resource.AccountNodeCombination{}, s.err
	}
	s.created = true
	return s.sample(), nil
}

func (s *combinationServiceStub) DeleteCombination(context.Context, resource.CombinationID) error {
	if s.err != nil {
		return s.err
	}
	s.deleted = true
	return nil
}

func TestCombinationRoutes(t *testing.T) {
	service := &combinationServiceStub{}
	handler := NewHandlerForAuthority(nil, "", ControlServices{Combinations: service})
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "list", method: http.MethodGet, path: "/api/combinations", status: http.StatusOK},
		{name: "get", method: http.MethodGet, path: "/api/combinations/4", status: http.StatusOK},
		{name: "create", method: http.MethodPost, path: "/api/combinations", body: `{"account_id":3,"node_id":7}`, status: http.StatusCreated},
		{name: "delete", method: http.MethodPost, path: "/api/combinations/4/delete", status: http.StatusOK},
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
		})
	}
	if !service.created || !service.deleted {
		t.Fatalf("service calls create=%v delete=%v", service.created, service.deleted)
	}
}

func TestCombinationDeleteMapsDependency(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{Combinations: &combinationServiceStub{err: postgres.ErrResourceDependency}})
	cookie, token := issueContext(t, handler)
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/combinations/4/delete", nil)
	request.Host = "localhost"
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(CSRFHeaderName, token)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "resource_in_use") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCombinationRoutesMapProtectedErrors(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{Combinations: &combinationServiceStub{err: postgres.ErrCombinationIncompatible}})
	cookie, token := issueContext(t, handler)
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/combinations", strings.NewReader(`{"account_id":3,"node_id":7}`))
	request.Host = "localhost"
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(CSRFHeaderName, token)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "combination_incompatible") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateCombinationRejectsDomesticSteam(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{
		Accounts: &accountServiceStub{accounts: []resource.PlatformAccount{{
			ID: 3, Platform: "steam", Alias: "main",
			SessionState: resource.AccountSessionStateUnverified, SessionRevision: 1,
		}}},
		Nodes:           &nodeServiceStub{region: resource.NodeRegionDomestic},
		Combinations:    &combinationServiceStub{},
		PlatformRegions: steamRegions(),
	})
	assertCombinationRegionRejected(t, handler)
}

func TestCreateCombinationRejectsForeignBuff(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{
		Accounts: &accountServiceStub{accounts: []resource.PlatformAccount{{
			ID: 3, Platform: "buff", Alias: "buff",
			SessionState: resource.AccountSessionStateUnverified, SessionRevision: 1,
		}}},
		Nodes:           &nodeServiceStub{region: resource.NodeRegionForeign},
		Combinations:    &combinationServiceStub{},
		PlatformRegions: steamRegions(),
	})
	assertCombinationRegionRejected(t, handler)
}

func assertCombinationRegionRejected(t *testing.T, handler *Handler) {
	t.Helper()
	cookie, token := issueContext(t, handler)
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/combinations", strings.NewReader(`{"account_id":3,"node_id":7}`))
	request.Host = "localhost"
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(CSRFHeaderName, token)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "platform_region_mismatch") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
