package steam

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"buff-go/internal/telemetry"
)

const (
	defaultUserAgent = "buffgo-steam/1.0 (+https://github.com/local/buff-go)"
	defaultTimeout   = 30 * time.Second
)

// Client talks to steamcommunity.com market endpoints.
type Client struct {
	http      *http.Client
	userAgent string
	// Cookie is sent as Cookie header (e.g. community steamLoginSecure + sessionid).
	// Required for PriceHistory and account endpoints; optional for public APIs.
	Cookie string
	// Country / Language used by histogram defaults.
	Country  string // e.g. US, SG
	Language string // e.g. english, tchinese
	// ProxyID keys search rate limits (empty → "direct"). Optional.
	ProxyID string
	// SearchLimiter enforces per-proxy search budget + 429 cooldown.
	// nil disables enforcement (tests / offline).
	SearchLimiter SearchLimiter
}

// Options configures NewClient.
type Options struct {
	HTTPClient *http.Client
	UserAgent  string
	Cookie     string
	Country    string
	Language   string
	// ProxyID for rate-limit keying (empty → "direct").
	ProxyID string
	// SearchLimiter optional; nil = no search rate limiting.
	SearchLimiter SearchLimiter
}

// NewClient builds a market client with defaults.
func NewClient(opts Options) *Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = defaultUserAgent
	}
	country := strings.TrimSpace(opts.Country)
	if country == "" {
		country = "US"
	}
	lang := strings.TrimSpace(opts.Language)
	if lang == "" {
		lang = "english"
	}
	return &Client{
		http:          hc,
		userAgent:     ua,
		Cookie:        strings.TrimSpace(opts.Cookie),
		Country:       country,
		Language:      lang,
		ProxyID:       strings.TrimSpace(opts.ProxyID),
		SearchLimiter: opts.SearchLimiter,
	}
}

// WithCookie returns a shallow copy using a different cookie string.
func (c *Client) WithCookie(cookie string) *Client {
	cp := *c
	cp.Cookie = strings.TrimSpace(cookie)
	return &cp
}

// WithProxyID returns a shallow copy keyed to a different proxy for rate limits.
func (c *Client) WithProxyID(proxyID string) *Client {
	cp := *c
	cp.ProxyID = strings.TrimSpace(proxyID)
	return &cp
}

// WithSearchLimiter returns a shallow copy using lim for search rate limits.
func (c *Client) WithSearchLimiter(lim SearchLimiter) *Client {
	cp := *c
	cp.SearchLimiter = lim
	return &cp
}

func (c *Client) proxyKey() string {
	if c == nil {
		return ProxyDirect
	}
	return NormalizeProxyID(c.ProxyID)
}

// allowSearch checks/reserves per-proxy search budget. No-op when limiter is nil.
func (c *Client) allowSearch(ctx context.Context) error {
	if c == nil || c.SearchLimiter == nil {
		return nil
	}
	if err := c.SearchLimiter.AllowSearch(ctx, c.proxyKey()); err != nil {
		return err
	}
	return nil
}

// markSearch429 records HTTP 429 cooldown for this client's proxy. No-op when limiter is nil.
func (c *Client) markSearch429(ctx context.Context) {
	if c == nil || c.SearchLimiter == nil {
		return
	}
	_ = c.SearchLimiter.MarkSearch429(ctx, c.proxyKey())
}

func (c *Client) get(ctx context.Context, rawURL string, referer string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, telemetry.WrapError("steam request build", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		req.Header.Set("Referer", referer)
	} else {
		req.Header.Set("Referer", "https://steamcommunity.com/market/")
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if c.Cookie != "" {
		req.Header.Set("Cookie", c.Cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, telemetry.WrapError("steam request", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, resp.StatusCode, telemetry.WrapError("steam response read", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("steam GET http status %d", resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func (c *Client) getHeader(ctx context.Context, rawURL, referer string, extra map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, telemetry.WrapError("steam request build", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Referer", referer)
	if c.Cookie != "" {
		req.Header.Set("Cookie", c.Cookie)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, telemetry.WrapError("steam request", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, resp.StatusCode, telemetry.WrapError("steam response read", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("steam GET http status %d", resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func listingReferer(appid int64, marketHashName string) string {
	return fmt.Sprintf("https://steamcommunity.com/market/listings/%d/%s", appid, url.PathEscape(marketHashName))
}

// MinorToMajor performs the legacy divide-by-100 conversion. Callers must first
// prove the raw field's currency and unit; Goal 0B has not done so.
func MinorToMajor(minor int64) float64 {
	return float64(minor) / 100.0
}

// ParseCookieJSON converts browser-exported cookie JSON array into a Cookie header.
// Accepts objects with name/value fields (Chrome extension export shape).
func ParseCookieJSON(raw []byte) (string, error) {
	// local import cycle avoidance: keep simple parser here
	return parseCookieJSON(raw)
}
