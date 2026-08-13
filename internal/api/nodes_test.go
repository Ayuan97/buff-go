package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"buff-go/internal/market"
	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

type nodeServiceStub struct {
	created bool
	deleted bool
	err     error
}

func (s *nodeServiceStub) sample() resource.AccessNode {
	return resource.AccessNode{
		ID: 7, Name: "home", Kind: resource.NodeKindDirect, Region: resource.NodeRegionForeign,
		EgressMode: resource.EgressModeStatic, State: resource.NodeStateAvailable,
		EgressRevision: 2, AssignmentRevision: 3, AppID: 730,
		Sides: []resource.NodeSideAssignment{{Platform: "steam", Side: market.SideAsk}},
	}
}

func (s *nodeServiceStub) ListNodes(context.Context) ([]resource.AccessNode, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []resource.AccessNode{s.sample()}, nil
}

func (s *nodeServiceStub) Node(_ context.Context, id resource.NodeID) (resource.AccessNode, bool, error) {
	if s.err != nil {
		return resource.AccessNode{}, false, s.err
	}
	if id != 7 {
		return resource.AccessNode{}, false, nil
	}
	return s.sample(), true, nil
}

func (s *nodeServiceStub) CreateNode(_ context.Context, name string, _ resource.NodeConnectionInput) (resource.AccessNode, error) {
	if s.err != nil {
		return resource.AccessNode{}, s.err
	}
	s.created = true
	node := s.sample()
	node.ID = 8
	node.Name = name
	node.State = resource.NodeStateValidating
	node.EgressRevision = 1
	node.AssignmentRevision = 1
	node.AppID = 0
	node.Sides = nil
	return node, nil
}

func (s *nodeServiceStub) ReplaceNodeConnection(_ context.Context, id resource.NodeID, _ int64, _ resource.NodeConnectionInput) (resource.AccessNode, error) {
	if s.err != nil {
		return resource.AccessNode{}, s.err
	}
	node := s.sample()
	node.ID = id
	node.State = resource.NodeStateValidating
	return node, nil
}

func (s *nodeServiceStub) RecordNodeExit(_ context.Context, id resource.NodeID, _ int64, _ netip.Addr, _ time.Time) (resource.AccessNode, error) {
	if s.err != nil {
		return resource.AccessNode{}, s.err
	}
	node := s.sample()
	node.ID = id
	return node, nil
}

func (s *nodeServiceStub) AssignNodeGame(_ context.Context, id resource.NodeID, _ int64, appID int64) (resource.AccessNode, error) {
	if s.err != nil {
		return resource.AccessNode{}, s.err
	}
	node := s.sample()
	node.ID = id
	node.AppID = appID
	return node, nil
}

func (s *nodeServiceStub) AssignNodeSide(_ context.Context, id resource.NodeID, _ int64, _ resource.Platform, _ market.Side) (resource.AccessNode, error) {
	if s.err != nil {
		return resource.AccessNode{}, s.err
	}
	node := s.sample()
	node.ID = id
	return node, nil
}

func (s *nodeServiceStub) DeleteNode(context.Context, resource.NodeID) error {
	if s.err != nil {
		return s.err
	}
	s.deleted = true
	return nil
}

func TestNodeRoutes(t *testing.T) {
	service := &nodeServiceStub{}
	handler := NewHandlerForAuthority(nil, "", ControlServices{Nodes: service})
	createBody := `{"name":"home","kind":"direct","region":"foreign","egress_mode":"static"}`
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "list", method: http.MethodGet, path: "/api/nodes", status: http.StatusOK},
		{name: "get", method: http.MethodGet, path: "/api/nodes/7", status: http.StatusOK},
		{name: "create", method: http.MethodPost, path: "/api/nodes", body: createBody, status: http.StatusCreated},
		{name: "connection", method: http.MethodPost, path: "/api/nodes/7/connection", body: `{"expected_egress_revision":2,"kind":"direct","region":"foreign","egress_mode":"static"}`, status: http.StatusOK},
		{name: "exit", method: http.MethodPost, path: "/api/nodes/7/exit", body: `{"expected_egress_revision":2,"address":"203.0.113.8","valid_until":"2030-01-01T00:00:00Z"}`, status: http.StatusOK},
		{name: "assign-game", method: http.MethodPost, path: "/api/nodes/7/assign-game", body: `{"expected_assignment_revision":3,"appid":730}`, status: http.StatusOK},
		{name: "assign-side", method: http.MethodPost, path: "/api/nodes/7/assign-side", body: `{"expected_assignment_revision":3,"platform":"steam","side":"ask"}`, status: http.StatusOK},
		{name: "delete", method: http.MethodPost, path: "/api/nodes/7/delete", status: http.StatusOK},
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
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatal("response echoed credential")
			}
		})
	}
	if !service.created || !service.deleted {
		t.Fatalf("service calls create=%v delete=%v", service.created, service.deleted)
	}
}

func TestNodeNotFoundJSON(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{Nodes: &nodeServiceStub{}})
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/nodes/99", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "node_not_found" {
		t.Fatalf("code=%q", body.Code)
	}
}

func TestNodeRoutesMapProtectedErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "occupied", err: resource.ErrResourceOccupied, status: http.StatusConflict, code: "node_occupied"},
		{name: "in use", err: postgres.ErrResourceDependency, status: http.StatusConflict, code: "node_in_use"},
		{name: "revision", err: postgres.ErrResourceRevisionConflict, status: http.StatusConflict, code: "node_revision_conflict"},
		{name: "invalid", err: postgres.ErrInvalidResource, status: http.StatusBadRequest, code: "invalid_node"},
		{name: "name conflict", err: postgres.ErrNodeNameConflict, status: http.StatusConflict, code: "node_name_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandlerForAuthority(nil, "", ControlServices{Nodes: &nodeServiceStub{err: test.err}})
			cookie, token := issueContext(t, handler)
			request := httptest.NewRequest(http.MethodPost, "http://localhost/api/nodes/7/delete", nil)
			request.Host = "localhost"
			request.Header.Set("Origin", "http://localhost")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(CSRFHeaderName, token)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
