package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"buff-go/internal/market"
	"buff-go/internal/telemetry"
)

// ErrHTTP429 is returned (wrapped) when Buff responds with HTTP 429.
// Pipeline Release uses this to set (proxy, platform=buff) pool cooldown.
var ErrHTTP429 = errors.New("buff http 429")

// BuffPartialPullError keeps completed pages when a later Buff page fails.
type BuffPartialPullError struct {
	Offers   []RawOffer
	NextPage int
	Err      error
}

func (e *BuffPartialPullError) Error() string {
	if e == nil {
		return "buff.sell: partial pull"
	}
	return fmt.Sprintf("buff.sell partial offers=%d next_page=%d: %v", len(e.Offers), e.NextPage, e.Err)
}

func (e *BuffPartialPullError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// AsBuffPartialPull extracts a partial Buff crawl error.
func AsBuffPartialPull(err error) (*BuffPartialPullError, bool) {
	var pe *BuffPartialPullError
	if errors.As(err, &pe) && pe != nil {
		return pe, true
	}
	return nil, false
}

// IsBuffRateLimited reports whether err is (or wraps) a Buff HTTP 429.
func IsBuffRateLimited(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrHTTP429) {
		return true
	}
	// Tolerate string-shaped errors from older call paths / wrappers.
	return strings.Contains(err.Error(), "http 429")
}

const (
	// DefaultBuffGoodsSellingURL is Buff market goods selling list endpoint.
	DefaultBuffGoodsSellingURL = "https://buff.163.com/api/market/goods/selling"
	defaultBuffSellPageSize    = 80
	maxBuffSellPageSize        = 80
)

// BuffSellOptions configures a one-shot or multi-page Buff sell fetch.
// Buff is account-backed (needs_account): Cookie / Lease.Account supplies session.
type BuffSellOptions struct {
	AppID      int64
	Game       string // Buff game code, e.g. "rust"; empty → BuffGameFromAppID
	PageNum    int    // 1-based; default 1
	PageSize   int
	MaxPages   int // 0 = fetch to the last page; >0 = safety cap
	MinPrice   float64
	MaxPrice   float64 // 0 = omit upper bound
	BaseURL    string
	HTTPClient *http.Client
	UserAgent  string
	// Cookie is a raw Cookie header value (session=…; csrf_token=…).
	// Prefer this or Lease.Account when Fetching with needs_account.
	Cookie   string
	Currency string // optional raw request hint; not response currency evidence
}

// BuffSellSource fetches Buff market sell listings (sell_min_price / sell_num)
// via /api/market/goods/selling for a given appid (mapped to Buff game code).
type BuffSellSource struct {
	client    *http.Client
	baseURL   string
	userAgent string
	currency  string
	cookie    string
}

// NewBuffSellSource builds a Buff sell Source with defaults.
func NewBuffSellSource(opts BuffSellOptions) *BuffSellSource {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = DefaultBuffGoodsSellingURL
	}
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = "buffgo-source-buff-sell/1.0 (+https://github.com/local/buff-go)"
	}
	cur := strings.TrimSpace(opts.Currency)
	return &BuffSellSource{
		client:    client,
		baseURL:   base,
		userAgent: ua,
		currency:  cur,
		cookie:    strings.TrimSpace(opts.Cookie),
	}
}

// Name implements Source.
func (s *BuffSellSource) Name() string { return NameBuffAsk }

// SetCookie updates the session cookie used on subsequent Pull/Fetch calls.
func (s *BuffSellSource) SetCookie(cookie string) {
	if s == nil {
		return
	}
	s.cookie = strings.TrimSpace(cookie)
}

// Cookie returns the configured session cookie (may be empty).
func (s *BuffSellSource) Cookie() string {
	if s == nil {
		return ""
	}
	return s.cookie
}

// SetHTTPClient replaces the HTTP client used for market requests.
// nil resets to a default client with a 30s timeout.
// Used by the pipeline to inject a StaticProvider / leased-proxy client per job.
func (s *BuffSellSource) SetHTTPClient(c *http.Client) {
	if s == nil {
		return
	}
	if c == nil {
		s.client = &http.Client{Timeout: 30 * time.Second}
		return
	}
	s.client = c
}

// Fetch implements Source for job.platform=buff, job.side=sell.
// Account cookie is taken from lease.Account when set, else source cookie.
func (s *BuffSellSource) Fetch(ctx context.Context, lease Lease, job JobSpec) ([]RawOffer, error) {
	if job.AppID <= 0 {
		return nil, fmt.Errorf("buff.sell: appid is required")
	}
	if job.Platform != "" && !strings.EqualFold(job.Platform, PlatformBuff) {
		return nil, fmt.Errorf("buff.sell: unexpected platform %q", job.Platform)
	}
	if job.Side != "" && job.Side != SideAsk {
		return nil, fmt.Errorf("buff.sell: unexpected side %q", job.Side)
	}
	opts := BuffSellOptions{AppID: job.AppID, PageNum: job.Start, MaxPages: job.MaxPages}
	if c := strings.TrimSpace(lease.Account); c != "" {
		opts.Cookie = c
	}
	return s.Pull(ctx, opts)
}

// Pull fetches one or more Buff goods/selling pages and returns sell RawOffers.
func (s *BuffSellSource) Pull(ctx context.Context, opts BuffSellOptions) ([]RawOffer, error) {
	if opts.AppID <= 0 {
		return nil, fmt.Errorf("buff.sell: appid is required")
	}
	game := strings.TrimSpace(opts.Game)
	if game == "" {
		game = BuffGameFromAppID(opts.AppID)
	}
	if game == "" {
		return nil, fmt.Errorf("buff.sell: unknown buff game for appid=%d (set opts.Game)", opts.AppID)
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultBuffSellPageSize
	}
	if pageSize > maxBuffSellPageSize {
		pageSize = maxBuffSellPageSize
	}
	maxPages := opts.MaxPages
	if maxPages < 0 {
		return nil, fmt.Errorf("buff.sell: max_pages cannot be negative")
	}
	pageNum := opts.PageNum
	if pageNum <= 0 {
		pageNum = 1
	}
	currency := strings.TrimSpace(opts.Currency)
	if currency == "" {
		currency = s.currency
	}
	cookie := strings.TrimSpace(opts.Cookie)
	if cookie == "" {
		cookie = s.cookie
	}

	var out []RawOffer
	seen := make(map[string]struct{})

	for page := 0; maxPages == 0 || page < maxPages; page++ {
		currentPage := pageNum + page
		body, err := s.fetchPage(ctx, game, currentPage, pageSize, opts.MinPrice, opts.MaxPrice, cookie)
		if err != nil {
			if len(out) > 0 {
				return out, &BuffPartialPullError{Offers: append([]RawOffer(nil), out...), NextPage: currentPage, Err: err}
			}
			return nil, err
		}
		collectedAt := time.Now().UTC()
		offers, totalPages, err := ParseBuffGoodsSell(body, opts.AppID, currency, collectedAt)
		if err != nil {
			if len(out) > 0 {
				return out, &BuffPartialPullError{Offers: append([]RawOffer(nil), out...), NextPage: currentPage, Err: err}
			}
			return nil, err
		}
		for _, o := range offers {
			keyKind, keyValue := "platform_id", o.PlatformItemID
			if keyValue == "" && o.MarketHashName != "" {
				keyKind, keyValue = "hash", o.MarketHashName
			}
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
		if totalPages > 0 {
			if currentPage >= totalPages {
				break
			}
		} else if len(offers) < pageSize {
			break
		}
		if maxPages > 0 && page+1 >= maxPages {
			return out, &BuffPartialPullError{
				Offers: append([]RawOffer(nil), out...), NextPage: currentPage + 1, Err: ErrPageLimit,
			}
		}
		if maxPages == 0 && totalPages <= 0 {
			return out, &BuffPartialPullError{
				Offers:   append([]RawOffer(nil), out...),
				NextPage: currentPage + 1,
				Err:      fmt.Errorf("buff.sell: full crawl requires total_page when page is full"),
			}
		}
	}
	return out, nil
}

func (s *BuffSellSource) fetchPage(ctx context.Context, game string, pageNum, pageSize int, minPrice, maxPrice float64, cookie string) ([]byte, error) {
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, telemetry.WrapError("buff sell base URL parse", err)
	}
	q := u.Query()
	q.Set("game", game)
	q.Set("page_num", strconv.Itoa(pageNum))
	q.Set("page_size", strconv.Itoa(pageSize))
	q.Set("sort_by", "price.asc")
	q.Set("use_suggestion", "0")
	q.Set("_", strconv.FormatInt(time.Now().UnixNano()/1e6, 10))
	if minPrice > 0 {
		q.Set("min_price", formatPrice(minPrice))
	}
	if maxPrice > 0 {
		q.Set("max_price", formatPrice(maxPrice))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, telemetry.WrapError("buff sell request build", err)
	}
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://buff.163.com/market/"+game)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, telemetry.WrapError("buff sell request", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, telemetry.WrapError("buff sell response read", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("buff.sell http 429: %w", ErrHTTP429)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("buff.sell http status %d", resp.StatusCode)
	}
	return body, nil
}

// buffGoodsSellResponse is the goods/selling (or goods list) JSON shape for sell prices.
type buffGoodsSellResponse struct {
	Code string `json:"code"`
	Data struct {
		Items []struct {
			ID             int     `json:"id"`
			Name           string  `json:"name"`
			MarketHashName string  `json:"market_hash_name"`
			SellMinPrice   *string `json:"sell_min_price"`
			SellNum        *int    `json:"sell_num"`
			Appid          int     `json:"appid"`
		} `json:"items"`
		TotalPage  int `json:"total_page"`
		TotalCount int `json:"total_count"`
		PageNum    int `json:"page_num"`
		PageSize   int `json:"page_size"`
	} `json:"data"`
	Msg   string `json:"msg"`
	Error string `json:"error"`
}

// ParseBuffGoodsSell preserves Buff goods/selling fields as raw evidence.
// The response does not prove currency or quantity semantics, so it cannot
// construct a present unified market observation.
func ParseBuffGoodsSell(body []byte, defaultAppID int64, defaultCurrency string, observedAt time.Time) ([]RawOffer, int, error) {
	var data buffGoodsSellResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, 0, fmt.Errorf("buff.sell json: %w", err)
	}
	code := strings.TrimSpace(data.Code)
	if code != "" && !strings.EqualFold(code, "OK") {
		detail := strings.TrimSpace(data.Msg)
		if detail == "" {
			detail = strings.TrimSpace(data.Error)
		}
		if detail == "" {
			detail = code
		}
		return nil, 0, fmt.Errorf("buff.sell api rejected response detail_ref=%s", telemetry.SafeDetailRef(code+"\x00"+detail))
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	out := make([]RawOffer, 0, len(data.Data.Items))
	for i, r := range data.Data.Items {
		appid := int64(r.Appid)
		if appid == 0 {
			appid = defaultAppID
		}
		hash := strings.TrimSpace(r.MarketHashName)
		name := strings.TrimSpace(r.Name)
		priceStr := ""
		if r.SellMinPrice != nil {
			priceStr = strings.TrimSpace(*r.SellMinPrice)
			if priceStr != "" {
				if _, err := market.ParseCNYCents(priceStr); err != nil {
					return nil, 0, fmt.Errorf("buff.sell item[%d] goods_id=%d invalid price detail_ref=%s",
						i, r.ID, telemetry.SafeDetailRef(priceStr))
				}
			}
		}
		var rawQuantity *int64
		if r.SellNum != nil {
			value := int64(*r.SellNum)
			rawQuantity = &value
		}
		goodsID := ""
		if r.ID != 0 {
			goodsID = strconv.Itoa(r.ID)
		}
		// sell_num is preserved as raw evidence until its quantity semantics are verified.
		o := RawOffer{
			Platform:       PlatformBuff,
			AppID:          appid,
			PlatformItemID: goodsID,
			NameRaw:        name,
			MarketHashName: hash,
			RawCurrency:    defaultCurrency,
			RawQuantity:    rawQuantity,
			RawPriceText:   priceStr,
			Observation: &market.Observation{
				Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: observedAt,
			},
			Source: SourceBuffGoods,
			SourceMeta: map[string]string{
				MetaEndpoint: "market/goods/selling",
			},
		}
		if goodsID != "" {
			o.SourceMeta["goods_id"] = goodsID
		}
		if err := o.Normalize(); err != nil {
			return nil, 0, fmt.Errorf("item[%d]: %w", i, err)
		}
		out = append(out, o)
	}
	totalPages := data.Data.TotalPage
	return out, totalPages, nil
}

// BuffGameFromAppID maps Steam appid to Buff market game query param.
// Unknown appids return "" (caller must set Game explicitly).
func BuffGameFromAppID(appid int64) string {
	switch appid {
	case 252490:
		return "rust"
	case 730:
		// Buff still uses "csgo" for CS2 market.
		return "csgo"
	case 570:
		return "dota2"
	case 440:
		return "tf2"
	default:
		return ""
	}
}

func formatPrice(p float64) string {
	// Prefer compact decimals (0.01 style) without trailing noise.
	return strconv.FormatFloat(p, 'f', -1, 64)
}
