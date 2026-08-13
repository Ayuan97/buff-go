// Package api provides the local HTTP control-plane security boundary.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"buff-go/internal/resource"
	"buff-go/internal/storage/postgres"
)

const (
	// SessionCookieName is the host-only browser control-session cookie.
	SessionCookieName = "buffgo_control_session"
	// CSRFHeaderName carries the token bound to the control session.
	CSRFHeaderName      = "X-Buffgo-CSRF"
	securityContextPath = "/api/security/context"
	defaultSessionTTL   = 15 * time.Minute
	maxSessions         = 1024
)

type controlSession struct {
	token     string
	expiresAt time.Time
}

// AccountService is the credential-safe account control boundary.
type AccountService interface {
	ListAccounts(context.Context) ([]resource.PlatformAccount, error)
	Account(context.Context, resource.AccountID) (resource.PlatformAccount, bool, error)
	CreateAccount(context.Context, resource.Platform, string, []byte) (resource.PlatformAccount, error)
	ReplaceAccountSession(context.Context, resource.AccountID, int64, []byte) (resource.PlatformAccount, error)
	DeleteAccount(context.Context, resource.AccountID) error
}

// Handler validates the local browser boundary before dispatching API routes.
type Handler struct {
	next             http.Handler
	accounts         AccountService
	nodes            NodeService
	combinations     CombinationService
	collection       CollectionService
	market           MarketService
	allowedAuthority string
	now              func() time.Time
	random           io.Reader
	mu               sync.Mutex
	sessions         map[string]controlSession
}

// ValidateListenAddress accepts only an explicit loopback IP and TCP port.
// Hostnames and wildcard addresses are rejected to keep the local control
// plane from becoming an accidental network service.
func ValidateListenAddress(value string) error {
	host, port, ok := splitAuthority(value)
	if !ok || port == "" {
		return fmt.Errorf("listen address must be loopback IP:port")
	}
	if net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("listen address must use a loopback IP")
	}
	return nil
}

// NewHandler creates the local control-plane handler. Business routes are
// injected separately and receive only requests that passed the common gate.
func NewHandler(next http.Handler) *Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return &Handler{
		next:     next,
		now:      time.Now,
		random:   rand.Reader,
		sessions: make(map[string]controlSession),
	}
}

// NewHandlerWithAccounts adds the account management routes to the security
// boundary. It does not expose or store raw session material in responses.
func NewHandlerWithAccounts(accounts AccountService) *Handler {
	h := NewHandler(nil)
	h.accounts = accounts
	return h
}

// NewHandlerForAuthority binds requests to the exact authority used by the
// explicitly configured loopback listener.
func NewHandlerForAuthority(next http.Handler, authority string, services ControlServices) *Handler {
	h := NewHandler(next)
	if host, port, ok := splitAuthority(authority); ok && port != "" {
		h.allowedAuthority = net.JoinHostPort(host, port)
	}
	h.accounts = services.Accounts
	h.nodes = services.Nodes
	h.combinations = services.Combinations
	h.collection = services.Collection
	h.market = services.Market
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())
	if !h.validHost(r.Host) {
		writeError(w, http.StatusMisdirectedRequest, "invalid_host")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(r, origin) {
		writeError(w, http.StatusForbidden, "origin_rejected")
		return
	}
	if r.URL.Path == securityContextPath {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.issueSecurityContext(w, r)
		return
	}
	if r.Method == http.MethodPost {
		if status, code := h.validatePOST(r); code != "" {
			writeError(w, status, code)
			return
		}
	}
	if r.URL.Path == "/api/capabilities" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		// 无详情适配器时只声明不可用，不创建永远跑不了的详情任务。
		writeAccountJSON(w, http.StatusOK, struct {
			Detail string `json:"detail"`
		}{Detail: "detail_unavailable"})
		return
	}
	if h.accounts != nil && (r.URL.Path == "/api/accounts" || strings.HasPrefix(r.URL.Path, "/api/accounts/")) {
		h.serveAccounts(w, r)
		return
	}
	if h.nodes != nil && (r.URL.Path == "/api/nodes" || strings.HasPrefix(r.URL.Path, "/api/nodes/")) {
		h.serveNodes(w, r)
		return
	}
	if h.combinations != nil && (r.URL.Path == "/api/combinations" || strings.HasPrefix(r.URL.Path, "/api/combinations/")) {
		h.serveCombinations(w, r)
		return
	}
	if h.collection != nil && (r.URL.Path == "/api/targets" || strings.HasPrefix(r.URL.Path, "/api/targets/") || r.URL.Path == "/api/runs") {
		h.serveCollection(w, r)
		return
	}
	if h.market != nil && r.URL.Path == "/api/quotes" {
		h.serveQuotes(w, r)
		return
	}
	h.next.ServeHTTP(w, r)
}

type accountResponse struct {
	ID              resource.AccountID           `json:"id"`
	Platform        resource.Platform            `json:"platform"`
	Alias           string                       `json:"alias"`
	SessionState    resource.AccountSessionState `json:"session_state"`
	SessionRevision int64                        `json:"session_revision"`
	LastCheckedAt   *time.Time                   `json:"last_checked_at,omitempty"`
}

type accountCreateRequest struct {
	Platform resource.Platform `json:"platform"`
	Alias    string            `json:"alias"`
	Session  string            `json:"session"`
}

type accountReplaceRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Session          string `json:"session"`
}

func (h *Handler) serveAccounts(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/accounts" {
		if r.Method == http.MethodGet {
			accounts, err := h.accounts.ListAccounts(r.Context())
			if err != nil {
				writeAccountError(w, err)
				return
			}
			out := make([]accountResponse, 0, len(accounts))
			for _, account := range accounts {
				out = append(out, toAccountResponse(account))
			}
			writeAccountJSON(w, http.StatusOK, out)
			return
		}
		if r.Method == http.MethodPost {
			var input accountCreateRequest
			if !decodeJSON(w, r, &input) {
				return
			}
			if strings.TrimSpace(input.Session) == "" {
				writeError(w, http.StatusBadRequest, "session_required")
				return
			}
			if len(input.Session) > 256<<10 {
				writeError(w, http.StatusRequestEntityTooLarge, "session_too_large")
				return
			}
			account, err := h.accounts.CreateAccount(r.Context(), input.Platform, input.Alias, []byte(input.Session))
			if err != nil {
				writeAccountError(w, err)
				return
			}
			writeAccountJSON(w, http.StatusCreated, toAccountResponse(account))
			return
		}
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/accounts/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	value, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || value < 1 {
		writeError(w, http.StatusBadRequest, "invalid_account_id")
		return
	}
	id := resource.AccountID(value)
	if len(parts) == 1 && r.Method == http.MethodGet {
		account, found, err := h.accounts.Account(r.Context(), id)
		if err != nil {
			writeAccountError(w, err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "account_not_found")
			return
		}
		writeAccountJSON(w, http.StatusOK, toAccountResponse(account))
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	switch parts[1] {
	case "session":
		var input accountReplaceRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		if strings.TrimSpace(input.Session) == "" {
			writeError(w, http.StatusBadRequest, "session_required")
			return
		}
		if len(input.Session) > 256<<10 {
			writeError(w, http.StatusRequestEntityTooLarge, "session_too_large")
			return
		}
		account, err := h.accounts.ReplaceAccountSession(r.Context(), id, input.ExpectedRevision, []byte(input.Session))
		if err != nil {
			writeAccountError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, toAccountResponse(account))
	case "delete":
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if err != nil || len(body) != 0 {
			writeError(w, http.StatusBadRequest, "body_not_allowed")
			return
		}
		if err := h.accounts.DeleteAccount(r.Context(), id); err != nil {
			writeAccountError(w, err)
			return
		}
		writeAccountJSON(w, http.StatusOK, struct {
			Deleted bool `json:"deleted"`
		}{Deleted: true})
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func toAccountResponse(account resource.PlatformAccount) accountResponse {
	return accountResponse{ID: account.ID, Platform: account.Platform, Alias: account.Alias, SessionState: account.SessionState, SessionRevision: account.SessionRevision, LastCheckedAt: account.LastCheckedAt}
}

func writeAccountJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, value)
}

func writeAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, resource.ErrResourceOccupied):
		writeError(w, http.StatusConflict, "account_occupied")
	case errors.Is(err, postgres.ErrResourceDependency):
		writeError(w, http.StatusConflict, "account_in_use")
	case errors.Is(err, postgres.ErrResourceNotFound):
		writeError(w, http.StatusNotFound, "account_not_found")
	case errors.Is(err, postgres.ErrResourceRevisionConflict):
		writeError(w, http.StatusConflict, "account_revision_conflict")
	case errors.Is(err, postgres.ErrAccountConflict):
		writeError(w, http.StatusConflict, "account_conflict")
	case errors.Is(err, postgres.ErrInvalidResource):
		writeError(w, http.StatusBadRequest, "invalid_account")
	case errors.Is(err, postgres.ErrResourceStorage):
		writeError(w, http.StatusServiceUnavailable, "resource_storage_unavailable")
	case errors.Is(err, postgres.ErrResourceIntegrity):
		writeError(w, http.StatusInternalServerError, "resource_integrity")
	default:
		writeError(w, http.StatusInternalServerError, "resource_error")
	}
}

func (h *Handler) validHost(authority string) bool {
	if h.allowedAuthority != "" {
		host, port, ok := splitAuthority(authority)
		return ok && port != "" && net.JoinHostPort(host, port) == h.allowedAuthority
	}
	return validLocalHost(authority)
}

func (h *Handler) validatePOST(r *http.Request) (int, string) {
	if r.Header.Get("Origin") == "" {
		return http.StatusForbidden, "origin_required"
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return http.StatusUnsupportedMediaType, "json_required"
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return http.StatusForbidden, "csrf_rejected"
	}
	token := r.Header.Get(CSRFHeaderName)
	if token == "" {
		return http.StatusForbidden, "csrf_rejected"
	}

	now := h.now()
	h.mu.Lock()
	session, ok := h.sessions[cookie.Value]
	if ok && !now.Before(session.expiresAt) {
		delete(h.sessions, cookie.Value)
		ok = false
	}
	h.mu.Unlock()
	if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(session.token)) != 1 {
		return http.StatusForbidden, "csrf_rejected"
	}
	return 0, ""
}

func (h *Handler) issueSecurityContext(w http.ResponseWriter, r *http.Request) {
	sessionID, err := randomToken(h.random)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	csrfToken, err := randomToken(h.random)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	now := h.now()
	expiresAt := now.Add(defaultSessionTTL)

	h.mu.Lock()
	h.pruneLocked(now)
	if len(h.sessions) >= maxSessions {
		h.evictOldestLocked()
	}
	h.sessions[sessionID] = controlSession{token: csrfToken, expiresAt: expiresAt}
	h.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(defaultSessionTTL / time.Second),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		CSRFToken string    `json:"csrf_token"`
		ExpiresAt time.Time `json:"expires_at"`
	}{CSRFToken: csrfToken, ExpiresAt: expiresAt})
}

func (h *Handler) pruneLocked(now time.Time) {
	for id, session := range h.sessions {
		if !now.Before(session.expiresAt) {
			delete(h.sessions, id)
		}
	}
}

func (h *Handler) evictOldestLocked() {
	var oldestID string
	var oldestExpiry time.Time
	for id, session := range h.sessions {
		if oldestID == "" || session.expiresAt.Before(oldestExpiry) {
			oldestID = id
			oldestExpiry = session.expiresAt
		}
	}
	delete(h.sessions, oldestID)
}

func randomToken(source io.Reader) (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func validLocalHost(authority string) bool {
	host, _, ok := splitAuthority(authority)
	if !ok {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sameOrigin(r *http.Request, rawOrigin string) bool {
	origin, err := url.Parse(rawOrigin)
	if err != nil || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if origin.Scheme != scheme {
		return false
	}
	originHost, originPort, ok := splitAuthority(origin.Host)
	if !ok {
		return false
	}
	requestHost, requestPort, ok := splitAuthority(r.Host)
	if !ok || !strings.EqualFold(originHost, requestHost) {
		return false
	}
	return effectivePort(originPort, scheme) == effectivePort(requestPort, scheme)
}

func splitAuthority(authority string) (string, string, bool) {
	if authority == "" || strings.TrimSpace(authority) != authority || strings.ContainsAny(authority, "@/?#\\") {
		return "", "", false
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		if strings.Contains(authority, ":") {
			return "", "", false
		}
		host = authority
		port = ""
	}
	if host == "" || strings.HasSuffix(host, ".") {
		return "", "", false
	}
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", "", false
		}
		port = strconv.Itoa(value)
	}
	return strings.ToLower(host), port, true
}

func effectivePort(port, scheme string) string {
	if port != "" {
		return port
	}
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Code string `json:"code"`
	}{Code: code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
