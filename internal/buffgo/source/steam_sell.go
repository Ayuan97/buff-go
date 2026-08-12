package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"buff-go/internal/buffgo/steam"
	"buff-go/internal/market"
	"buff-go/internal/telemetry"
)

const (
	// DefaultSteamMarketSearchURL is the community market search/render endpoint.
	DefaultSteamMarketSearchURL = "https://steamcommunity.com/market/search/render/"
	// These page sizes are legacy compatibility values, not a verified Steam cap.
	defaultSteamSellCount = 100
	maxSteamSellCount     = 100
	// DefaultPageDelay preserves the legacy compatibility pause between pages.
	// Goal 0B has not verified it as a safe Steam interval.
	DefaultPageDelay = 2 * time.Second
)

// SteamSellOptions configures a one-shot or multi-page Steam sell fetch.
type SteamSellOptions struct {
	AppID      int64
	Start      int
	Count      int
	MaxPages   int           // 0 = fetch to the last page; >0 = safety cap
	PageDelay  time.Duration // Legacy pause between pages; platform-safe delay is unverified.
	BaseURL    string
	HTTPClient *http.Client
	UserAgent  string
	Currency   string // Legacy raw label; empty uses the unverified USD assumption.
	// ProxyID keys search rate limits (empty → "direct"). Optional.
	ProxyID string
	// SearchLimiter applies the legacy per-proxy budget and 429 cooldown.
	// Nil disables it; its configured values are not verified platform policy.
	SearchLimiter steam.SearchLimiter
}

// SteamSellSource fetches legacy search/render sell_* wire fields for one appid.
// Their ask, amount, and count semantics remain unverified.
type SteamSellSource struct {
	client        *http.Client
	baseURL       string
	userAgent     string
	currency      string
	proxyID       string
	searchLimiter steam.SearchLimiter
}

// NewSteamSellSource builds a Steam sell Source with defaults.
func NewSteamSellSource(opts SteamSellOptions) *SteamSellSource {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = DefaultSteamMarketSearchURL
	}
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = "buffgo-source-steam-sell/1.0 (+https://github.com/local/buff-go)"
	}
	cur := strings.TrimSpace(opts.Currency)
	if cur == "" {
		cur = "USD"
	}
	return &SteamSellSource{
		client:        client,
		baseURL:       base,
		userAgent:     ua,
		currency:      cur,
		proxyID:       strings.TrimSpace(opts.ProxyID),
		searchLimiter: opts.SearchLimiter,
	}
}

// SetSearchLimiter installs or replaces the per-proxy search rate limiter.
func (s *SteamSellSource) SetSearchLimiter(lim steam.SearchLimiter) {
	if s == nil {
		return
	}
	s.searchLimiter = lim
}

// SetProxyID sets the rate-limit key (empty → direct).
func (s *SteamSellSource) SetProxyID(proxyID string) {
	if s == nil {
		return
	}
	s.proxyID = strings.TrimSpace(proxyID)
}

// SetHTTPClient replaces the HTTP client used for market requests.
// nil resets to a default client with a 30s timeout.
func (s *SteamSellSource) SetHTTPClient(c *http.Client) {
	if s == nil {
		return
	}
	if c == nil {
		s.client = &http.Client{Timeout: 30 * time.Second}
		return
	}
	s.client = c
}

// ProxyID returns the rate-limit key currently set on the source (may be empty).
func (s *SteamSellSource) ProxyID() string {
	if s == nil {
		return ""
	}
	return s.proxyID
}

func (s *SteamSellSource) proxyKey() string {
	if s == nil {
		return steam.ProxyDirect
	}
	return steam.NormalizeProxyID(s.proxyID)
}

// Name implements Source.
func (s *SteamSellSource) Name() string { return NameSteamAsk }

// Fetch implements Source for job.platform=steam, job.side=sell.
// lease.Proxy (when set) keys the per-proxy search rate limiter.
func (s *SteamSellSource) Fetch(ctx context.Context, lease Lease, job JobSpec) ([]RawOffer, error) {
	if job.AppID <= 0 {
		return nil, fmt.Errorf("steam.sell: appid is required")
	}
	if job.Platform != "" && !strings.EqualFold(job.Platform, PlatformSteam) {
		return nil, fmt.Errorf("steam.sell: unexpected platform %q", job.Platform)
	}
	if job.Side != "" && job.Side != SideAsk {
		return nil, fmt.Errorf("steam.sell: unexpected side %q", job.Side)
	}
	return s.Pull(ctx, SteamSellOptions{
		AppID:     job.AppID,
		Start:     job.Start,
		Count:     job.Count,
		MaxPages:  job.MaxPages,
		PageDelay: job.PageDelay,
		ProxyID:   lease.Proxy,
	})
}

// PartialPullError is returned when some pages were fetched successfully before
// a later page fails. Offers keeps completed work and Start is the resume offset.
//
// Unwrap returns the rate-limit cause so steam.IsSearchRateLimit and pipeline
// soft-skip still apply after a partial write.
type PartialPullError struct {
	Offers []RawOffer
	Page   int // 0-based page index that failed
	Start  int // market start offset of the failed page
	Err    error
}

func (e *PartialPullError) Error() string {
	if e == nil {
		return "steam.sell: partial pull"
	}
	return fmt.Sprintf("steam.sell partial pages=%d page=%d start=%d: %v", len(e.Offers), e.Page, e.Start, e.Err)
}

func (e *PartialPullError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// AsPartialPull extracts a PartialPullError from err (including wrapped).
func AsPartialPull(err error) (*PartialPullError, bool) {
	var pe *PartialPullError
	if errors.As(err, &pe) && pe != nil {
		return pe, true
	}
	return nil, false
}

// Pull fetches one or more market search pages and returns sell RawOffers.
// When SearchLimiter is set, it applies the configured legacy budget and 429
// cooldown. Those values are compatibility settings, not verified Steam policy.
//
// If a later page hits a search rate limit and earlier pages already produced
// offers, Pull returns those offers with a *PartialPullError (R9) so callers can
// persist partial results then soft-skip.
func (s *SteamSellSource) Pull(ctx context.Context, opts SteamSellOptions) ([]RawOffer, error) {
	if opts.AppID <= 0 {
		return nil, fmt.Errorf("steam.sell: appid is required")
	}
	count := opts.Count
	if count <= 0 {
		count = defaultSteamSellCount
	}
	if count > maxSteamSellCount {
		count = maxSteamSellCount
	}
	maxPages := opts.MaxPages
	if maxPages < 0 {
		return nil, fmt.Errorf("steam.sell: max_pages cannot be negative")
	}
	start := opts.Start
	if start < 0 {
		start = 0
	}
	currency := strings.TrimSpace(opts.Currency)
	if currency == "" {
		currency = s.currency
	}
	// Per-call overrides for proxy / limiter (lease wiring).
	proxyKey := s.proxyKey()
	if strings.TrimSpace(opts.ProxyID) != "" {
		proxyKey = steam.NormalizeProxyID(opts.ProxyID)
	}
	limiter := s.searchLimiter
	if opts.SearchLimiter != nil {
		limiter = opts.SearchLimiter
	}
	pageDelay := opts.PageDelay
	// Multi-page legacy calls use the compatibility delay when none is supplied.
	if (maxPages == 0 || maxPages > 1) && pageDelay <= 0 {
		pageDelay = DefaultPageDelay
	}

	var out []RawOffer
	seen := make(map[string]struct{})

	for page := 0; maxPages == 0 || page < maxPages; page++ {
		if page > 0 && pageDelay > 0 {
			select {
			case <-ctx.Done():
				if len(out) > 0 {
					return out, &PartialPullError{
						Offers: append([]RawOffer(nil), out...),
						Page:   page,
						Start:  start,
						Err:    ctx.Err(),
					}
				}
				return nil, ctx.Err()
			case <-time.After(pageDelay):
			}
		}
		body, err := s.fetchPage(ctx, opts.AppID, start, count, proxyKey, limiter)
		if err != nil {
			if len(out) > 0 {
				return out, &PartialPullError{
					Offers: append([]RawOffer(nil), out...),
					Page:   page,
					Start:  start,
					Err:    fmt.Errorf("steam.sell page=%d start=%d: %w", page, start, err),
				}
			}
			// Rate-limit denials (no data yet) or hard errors: caller skips/retries later.
			return nil, fmt.Errorf("steam.sell page=%d start=%d: %w", page, start, err)
		}
		collectedAt := time.Now().UTC()
		offers, total, err := ParseSteamMarketSell(body, opts.AppID, currency, collectedAt)
		if err != nil {
			if len(out) > 0 {
				return out, &PartialPullError{
					Offers: append([]RawOffer(nil), out...),
					Page:   page,
					Start:  start,
					Err:    fmt.Errorf("steam.sell parse page=%d start=%d: %w", page, start, err),
				}
			}
			return nil, fmt.Errorf("steam.sell parse page=%d: %w", page, err)
		}
		for _, o := range offers {
			keyKind, keyValue := "hash", o.MarketHashName
			if keyValue == "" {
				keyKind, keyValue = "raw_name", o.NameRaw
			}
			key := fmt.Sprintf("%d\x00%s\x00%s", o.AppID, keyKind, keyValue)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, o)
		}
		start += count
		if total > 0 {
			if start >= total {
				break
			}
		} else if len(offers) < count {
			break
		}
		if maxPages > 0 && page+1 >= maxPages {
			return out, &PartialPullError{
				Offers: append([]RawOffer(nil), out...),
				Page:   page + 1,
				Start:  start,
				Err:    ErrPageLimit,
			}
		}
		if maxPages == 0 && total <= 0 {
			return out, &PartialPullError{
				Offers: append([]RawOffer(nil), out...),
				Page:   page + 1,
				Start:  start,
				Err:    fmt.Errorf("steam.sell: full crawl requires total_count when page is full"),
			}
		}
	}
	return out, nil
}

func (s *SteamSellSource) fetchPage(ctx context.Context, appid int64, start, count int, proxyKey string, limiter steam.SearchLimiter) ([]byte, error) {
	if limiter != nil {
		if err := limiter.AllowSearch(ctx, proxyKey); err != nil {
			return nil, err
		}
	}

	u, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, telemetry.WrapError("steam sell base URL parse", err)
	}
	q := u.Query()
	q.Set("query", "")
	q.Set("start", strconv.Itoa(start))
	q.Set("count", strconv.Itoa(count))
	q.Set("search_descriptions", "0")
	q.Set("sort_column", "price")
	q.Set("sort_dir", "asc")
	q.Set("appid", strconv.FormatInt(appid, 10))
	q.Set("norender", "1")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, telemetry.WrapError("steam sell request build", err)
	}
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://steamcommunity.com/market/search?appid="+strconv.FormatInt(appid, 10))

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, telemetry.WrapError("steam sell request", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, telemetry.WrapError("steam sell response read", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		if limiter != nil {
			_ = limiter.MarkSearch429(ctx, proxyKey)
		}
		return nil, fmt.Errorf("steam.sell http 429: %w", steam.ErrSearchCooling)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("steam.sell http status %d", resp.StatusCode)
	}
	return body, nil
}

// steamSearchSellResponse preserves candidate raw sell_* wire fields.
type steamSearchSellResponse struct {
	Success    bool `json:"success"`
	Start      int  `json:"start"`
	TotalCount int  `json:"total_count"`
	Results    []struct {
		Name          string `json:"name"`
		HashName      string `json:"hash_name"`
		SellPrice     *int   `json:"sell_price"`
		SellPriceText string `json:"sell_price_text"`
		SellListings  *int   `json:"sell_listings"`
		Appid         int    `json:"appid"`
		Asset         struct {
			Appid          int    `json:"appid"`
			IconURL        string `json:"icon_url"`
			MarketHashName string `json:"market_hash_name"`
		} `json:"asset_description"`
	} `json:"results"`
}

var currencyFromText = regexp.MustCompile(`^\s*([^\d\s.,]+)`)

// ParseSteamMarketSell preserves Steam market search/render fields as raw
// evidence. It does not promote them into the unified CNY market contract.
func ParseSteamMarketSell(body []byte, defaultAppID int64, defaultCurrency string, observedAt time.Time) ([]RawOffer, int, error) {
	var data steamSearchSellResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, 0, fmt.Errorf("steam.sell json: %w", err)
	}
	if !data.Success && len(data.Results) == 0 {
		return nil, 0, fmt.Errorf("steam.sell search unsuccessful")
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if defaultCurrency == "" {
		defaultCurrency = "USD"
	}

	out := make([]RawOffer, 0, len(data.Results))
	for i, r := range data.Results {
		appid := int64(r.Appid)
		if appid == 0 {
			appid = int64(r.Asset.Appid)
		}
		if appid == 0 {
			appid = defaultAppID
		}
		hash := strings.TrimSpace(r.HashName)
		if hash == "" {
			hash = strings.TrimSpace(r.Asset.MarketHashName)
		}
		name := strings.TrimSpace(r.Name)
		currency := inferCurrency(r.SellPriceText, defaultCurrency)
		var rawPriceMinor *int64
		if r.SellPrice != nil {
			value := int64(*r.SellPrice)
			rawPriceMinor = &value
		}
		var rawQuantity *int64
		if r.SellListings != nil {
			value := int64(*r.SellListings)
			rawQuantity = &value
		}
		o := RawOffer{
			Platform:       PlatformSteam,
			AppID:          appid,
			NameRaw:        name,
			MarketHashName: hash,
			RawPriceMinor:  rawPriceMinor,
			RawCurrency:    currency,
			RawQuantity:    rawQuantity,
			RawPriceText:   strings.TrimSpace(r.SellPriceText),
			Observation: &market.Observation{
				Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: observedAt,
			},
			Source: SourceSteamSearch,
			SourceMeta: map[string]string{
				MetaEndpoint: "market/search/render",
			},
		}
		if icon := strings.TrimSpace(r.Asset.IconURL); icon != "" {
			o.SourceMeta["icon_url"] = icon
		}
		if err := o.Normalize(); err != nil {
			return nil, 0, fmt.Errorf("result[%d]: %w", i, err)
		}
		out = append(out, o)
	}
	return out, data.TotalCount, nil
}

func inferCurrency(priceText, fallback string) string {
	priceText = strings.TrimSpace(priceText)
	if priceText == "" {
		return fallback
	}
	// Common Steam prefixes: $, ¥, CDN$, £, € …
	if strings.HasPrefix(priceText, "CDN$") || strings.HasPrefix(priceText, "CDN $") {
		return "CAD"
	}
	if strings.HasPrefix(priceText, "A$") || strings.HasPrefix(priceText, "AU$") {
		return "AUD"
	}
	if strings.HasPrefix(priceText, "R$") {
		return "BRL"
	}
	if strings.HasPrefix(priceText, "$") {
		return "USD"
	}
	if strings.HasPrefix(priceText, "¥") || strings.HasPrefix(priceText, "￥") {
		return ""
	}
	if strings.HasPrefix(priceText, "£") {
		return "GBP"
	}
	if strings.HasPrefix(priceText, "€") {
		return "EUR"
	}
	if m := currencyFromText.FindStringSubmatch(priceText); len(m) == 2 {
		sym := strings.TrimSpace(m[1])
		if sym != "" && len(sym) <= 4 {
			return strings.ToUpper(sym)
		}
	}
	return fallback
}
