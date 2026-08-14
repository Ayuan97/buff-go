package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

type providerServiceStub struct {
	created bool
	updated bool
	err     error
}

func (s *providerServiceStub) sample() resource.ProxyProvider {
	return resource.ProxyProvider{
		ID: 2, Name: "alpha", Enabled: true, Priority: 1,
		Regions: []resource.NodeRegion{resource.NodeRegionForeign}, HasCredential: true, Revision: 3,
	}
}

func (s *providerServiceStub) ListWatermarks(context.Context) ([]resource.RegionWatermark, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []resource.RegionWatermark{{Region: resource.NodeRegionForeign, MinUsable: 2, Revision: 1}}, nil
}

func (s *providerServiceStub) SetWatermark(_ context.Context, region resource.NodeRegion, _ int64, min int) (resource.RegionWatermark, error) {
	if s.err != nil {
		return resource.RegionWatermark{}, s.err
	}
	return resource.RegionWatermark{Region: region, MinUsable: min, Revision: 2}, nil
}

func (s *providerServiceStub) ListProviders(context.Context) ([]resource.ProxyProvider, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []resource.ProxyProvider{s.sample()}, nil
}

func (s *providerServiceStub) CreateProvider(context.Context, string, bool, int, []resource.NodeRegion, []byte) (resource.ProxyProvider, error) {
	if s.err != nil {
		return resource.ProxyProvider{}, s.err
	}
	s.created = true
	return s.sample(), nil
}

func (s *providerServiceStub) UpdateProvider(context.Context, resource.ProviderID, int64, string, bool, int, []resource.NodeRegion) (resource.ProxyProvider, error) {
	if s.err != nil {
		return resource.ProxyProvider{}, s.err
	}
	s.updated = true
	return s.sample(), nil
}

func (s *providerServiceStub) ReplaceProviderCredential(context.Context, resource.ProviderID, int64, []byte) (resource.ProxyProvider, error) {
	if s.err != nil {
		return resource.ProxyProvider{}, s.err
	}
	s.updated = true
	return s.sample(), nil
}

func (s *providerServiceStub) DeleteProvider(context.Context, resource.ProviderID) error {
	if s.err != nil {
		return s.err
	}
	return nil
}

func TestProviderRoutesNeverEchoCredential(t *testing.T) {
	service := &providerServiceStub{}
	handler := NewHandlerForAuthority(nil, "", ControlServices{Providers: service})
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "list watermarks", method: http.MethodGet, path: "/api/watermarks", status: http.StatusOK},
		{name: "set watermark", method: http.MethodPost, path: "/api/watermarks", body: `{"region":"foreign","expected_revision":1,"min_usable":3}`, status: http.StatusOK},
		{name: "list", method: http.MethodGet, path: "/api/providers", status: http.StatusOK},
		{name: "create", method: http.MethodPost, path: "/api/providers", body: `{"name":"alpha","priority":1,"regions":["foreign"],"credential":"vendor-secret"}`, status: http.StatusCreated},
		{name: "update", method: http.MethodPost, path: "/api/providers/2/update", body: `{"expected_revision":3,"name":"alpha","enabled":true,"priority":1,"regions":["foreign"]}`, status: http.StatusOK},
		{name: "credential", method: http.MethodPost, path: "/api/providers/2/credential", body: `{"expected_revision":3,"credential":"new-secret"}`, status: http.StatusOK},
		{name: "delete", method: http.MethodPost, path: "/api/providers/2/delete", status: http.StatusOK},
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
			if strings.Contains(response.Body.String(), "vendor-secret") || strings.Contains(response.Body.String(), "new-secret") {
				t.Fatal("response echoed provider credential")
			}
		})
	}
}

func TestProviderCreateRequiresCredential(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{Providers: &providerServiceStub{}})
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/providers", strings.NewReader(`{"name":"alpha","priority":1,"regions":["foreign"],"credential":""}`))
	request.Host = "localhost"
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("Content-Type", "application/json")
	cookie, token := issueContext(t, handler)
	request.AddCookie(cookie)
	request.Header.Set(CSRFHeaderName, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body["code"] != "credential_required" {
		t.Fatalf("body=%v", body)
	}
}

func TestProviderConflictMapping(t *testing.T) {
	handler := NewHandlerForAuthority(nil, "", ControlServices{Providers: &providerServiceStub{err: postgres.ErrProviderConflict}})
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/providers", strings.NewReader(`{"name":"alpha","priority":1,"regions":["foreign"],"credential":"x"}`))
	request.Host = "localhost"
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("Content-Type", "application/json")
	cookie, token := issueContext(t, handler)
	request.AddCookie(cookie)
	request.Header.Set(CSRFHeaderName, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d", response.Code)
	}
}
