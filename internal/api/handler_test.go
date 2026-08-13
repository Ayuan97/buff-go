package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

type securityContextResponse struct {
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type accountServiceStub struct {
	accounts []resource.PlatformAccount
	created  bool
	updated  bool
	deleted  bool
	err      error
}

func (s *accountServiceStub) ListAccounts(context.Context) ([]resource.PlatformAccount, error) {
	return append([]resource.PlatformAccount(nil), s.accounts...), s.err
}
func (s *accountServiceStub) Account(_ context.Context, id resource.AccountID) (resource.PlatformAccount, bool, error) {
	if s.err != nil {
		return resource.PlatformAccount{}, false, s.err
	}
	for _, account := range s.accounts {
		if account.ID == id {
			return account, true, nil
		}
	}
	return resource.PlatformAccount{}, false, nil
}
func (s *accountServiceStub) CreateAccount(_ context.Context, platform resource.Platform, alias string, session []byte) (resource.PlatformAccount, error) {
	if s.err != nil {
		return resource.PlatformAccount{}, s.err
	}
	s.created = true
	return resource.PlatformAccount{ID: 7, Platform: platform, Alias: alias, SessionState: resource.AccountSessionStateUnverified, SessionRevision: 1}, nil
}
func (s *accountServiceStub) ReplaceAccountSession(_ context.Context, id resource.AccountID, _ int64, _ []byte) (resource.PlatformAccount, error) {
	if s.err != nil {
		return resource.PlatformAccount{}, s.err
	}
	s.updated = true
	return resource.PlatformAccount{ID: id, Platform: "steam", Alias: "main", SessionState: resource.AccountSessionStateUnverified, SessionRevision: 2}, nil
}
func (s *accountServiceStub) DeleteAccount(_ context.Context, _ resource.AccountID) error {
	if s.err != nil {
		return s.err
	}
	s.deleted = true
	return nil
}

func TestAccountRoutesNeverEchoSession(t *testing.T) {
	service := &accountServiceStub{accounts: []resource.PlatformAccount{{ID: 3, Platform: "steam", Alias: "main", SessionState: resource.AccountSessionStateValid, SessionRevision: 4}}}
	handler := NewHandlerWithAccounts(service)
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "list", method: http.MethodGet, path: "/api/accounts", status: http.StatusOK},
		{name: "get", method: http.MethodGet, path: "/api/accounts/3", status: http.StatusOK},
		{name: "create", method: http.MethodPost, path: "/api/accounts", body: `{"platform":"steam","alias":"new","session":"secret-session"}`, status: http.StatusCreated},
		{name: "replace", method: http.MethodPost, path: "/api/accounts/3/session", body: `{"expected_revision":4,"session":"new-secret"}`, status: http.StatusOK},
		{name: "delete", method: http.MethodPost, path: "/api/accounts/3/delete", status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://localhost"+test.path, strings.NewReader(test.body))
			request.Host = "localhost"
			request.Header.Set("Origin", "http://localhost")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			// Obtain the security context and attach its cookie/token to every POST.
			if test.method == http.MethodPost {
				cookie, token := issueContext(t, handler)
				request.AddCookie(cookie)
				request.Header.Set(CSRFHeaderName, token)
			}
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "secret-session") || strings.Contains(response.Body.String(), "new-secret") {
				t.Fatal("response echoed account session")
			}
		})
	}
	if !service.created || !service.updated || !service.deleted {
		t.Fatalf("service calls create=%v replace=%v delete=%v", service.created, service.updated, service.deleted)
	}
}

func TestAccountRoutesMapProtectedErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "occupied", err: resource.ErrResourceOccupied, status: http.StatusConflict, code: "account_occupied"},
		{name: "dependency", err: postgres.ErrResourceDependency, status: http.StatusConflict, code: "account_in_use"},
		{name: "revision", err: postgres.ErrResourceRevisionConflict, status: http.StatusConflict, code: "account_revision_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &accountServiceStub{err: test.err}
			handler := NewHandlerWithAccounts(service)
			cookie, token := issueContext(t, handler)
			request := httptest.NewRequest(http.MethodPost, "http://localhost/api/accounts/3/session", strings.NewReader(`{"expected_revision":1,"session":"secret"}`))
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
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatal("error echoed secret")
			}
		})
	}
}

func TestCapabilitiesDetailUnavailable(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/capabilities", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "detail_unavailable") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCapabilitiesRejectsPOST(t *testing.T) {
	handler := NewHandler(nil)
	cookie, token := issueContext(t, handler)
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/capabilities", strings.NewReader(`{}`))
	request.Host = "localhost"
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(CSRFHeaderName, token)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSecurityContextIssuesBoundStrictSession(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/security/context", nil)
	request.Host = "localhost:8080"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body securityContextResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.CSRFToken == "" || body.ExpiresAt.IsZero() {
		t.Fatalf("invalid response: %+v err=%v", body, err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieName || cookie.Value == "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("unsafe cookie: %+v", cookie)
	}
	if cookie.Domain != "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cookie domain=%q cache=%q", cookie.Domain, response.Header().Get("Cache-Control"))
	}
	assertSecurityHeaders(t, response)
}

func TestValidPOSTReachesBusinessRoute(t *testing.T) {
	var calls atomic.Int32
	handler := NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	cookie, token := issueContext(t, handler)

	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/accounts", strings.NewReader(`{}`))
	request.Host = "localhost"
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set(CSRFHeaderName, token)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
}

func TestPOSTSecurityRejections(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	handler := NewHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	handler.now = func() time.Time { return now }
	cookie, token := issueContext(t, handler)
	otherCookie, otherToken := issueContext(t, handler)

	tests := []struct {
		name        string
		origin      string
		contentType string
		cookie      *http.Cookie
		token       string
	}{
		{name: "missing origin", contentType: "application/json", cookie: cookie, token: token},
		{name: "cross origin", origin: "http://evil.example", contentType: "application/json", cookie: cookie, token: token},
		{name: "form post", origin: "http://localhost", contentType: "application/x-www-form-urlencoded", cookie: cookie, token: token},
		{name: "missing session", origin: "http://localhost", contentType: "application/json", token: token},
		{name: "missing token", origin: "http://localhost", contentType: "application/json", cookie: cookie},
		{name: "wrong token", origin: "http://localhost", contentType: "application/json", cookie: cookie, token: "secret-invalid-token"},
		{name: "session mismatch", origin: "http://localhost", contentType: "application/json", cookie: otherCookie, token: token},
		{name: "reverse mismatch", origin: "http://localhost", contentType: "application/json", cookie: cookie, token: otherToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://localhost/api/accounts", bytes.NewBufferString(`{}`))
			request.Host = "localhost"
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.cookie != nil {
				request.AddCookie(test.cookie)
			}
			if test.token != "" {
				request.Header.Set(CSRFHeaderName, test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			wantStatus := http.StatusForbidden
			if test.name == "form post" {
				wantStatus = http.StatusUnsupportedMediaType
			}
			if response.Code != wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "secret-invalid-token") {
				t.Fatal("response echoed rejected credential")
			}
		})
	}

	now = now.Add(defaultSessionTTL)
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/accounts", bytes.NewBufferString(`{}`))
	request.Host = "localhost"
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(CSRFHeaderName, token)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expired session status=%d", response.Code)
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected requests reached route: %d", calls.Load())
	}
}

func TestHostOriginAndMethodBoundary(t *testing.T) {
	var calls atomic.Int32
	handler := NewHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name   string
		method string
		host   string
		origin string
		status int
	}{
		{name: "dns rebinding", method: http.MethodGet, host: "attacker.example", status: http.StatusMisdirectedRequest},
		{name: "malformed host", method: http.MethodGet, host: "localhost:bad", status: http.StatusMisdirectedRequest},
		{name: "cross origin get", method: http.MethodGet, host: "localhost", origin: "http://attacker.example", status: http.StatusForbidden},
		{name: "scheme mismatch", method: http.MethodGet, host: "localhost", origin: "https://localhost", status: http.StatusForbidden},
		{name: "unsupported method", method: http.MethodDelete, host: "localhost", status: http.StatusMethodNotAllowed},
		{name: "loopback ipv4", method: http.MethodGet, host: "127.0.0.1:8080", status: http.StatusNoContent},
		{name: "loopback ipv6", method: http.MethodGet, host: "[::1]:8080", status: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://localhost/api/status", nil)
			request.Host = test.host
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS response header was emitted")
			}
			assertSecurityHeaders(t, response)
		})
	}
	if calls.Load() != 2 {
		t.Fatalf("business calls=%d", calls.Load())
	}
}

func TestGETDoesNotRequireOrCreateControlSession(t *testing.T) {
	handler := NewHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/status", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || len(response.Result().Cookies()) != 0 {
		t.Fatalf("status=%d cookies=%d", response.Code, len(response.Result().Cookies()))
	}
}

func TestValidateListenAddress(t *testing.T) {
	for _, value := range []string{"127.0.0.1:8080", "[::1]:8080"} {
		if err := ValidateListenAddress(value); err != nil {
			t.Fatalf("ValidateListenAddress(%q): %v", value, err)
		}
	}
	for _, value := range []string{"localhost:8080", ":8080", "0.0.0.0:8080", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:65536"} {
		if err := ValidateListenAddress(value); err == nil {
			t.Fatalf("ValidateListenAddress(%q) accepted unsafe address", value)
		}
	}
}

func TestHandlerCanBindToExactAuthority(t *testing.T) {
	handler := NewHandlerForAuthority(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), "127.0.0.1:8080", ControlServices{})
	for _, test := range []struct {
		host   string
		status int
	}{
		{host: "127.0.0.1:8080", status: http.StatusNoContent},
		{host: "localhost:8080", status: http.StatusMisdirectedRequest},
		{host: "127.0.0.1:8081", status: http.StatusMisdirectedRequest},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/status", nil)
		request.Host = test.host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("host=%q status=%d want=%d", test.host, response.Code, test.status)
		}
	}
}

func issueContext(t *testing.T, handler *Handler) (*http.Cookie, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/security/context", nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("context status=%d body=%s", response.Code, response.Body.String())
	}
	var body securityContextResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return response.Result().Cookies()[0], body.CSRFToken
}

func assertSecurityHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("missing CSP: %q", response.Header().Get("Content-Security-Policy"))
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
}
