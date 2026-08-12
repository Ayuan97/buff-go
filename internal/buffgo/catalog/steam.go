package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"buff-go/internal/telemetry"
)

const (
	// DefaultSteamSearchURL is the Steam community market search/render endpoint.
	DefaultSteamSearchURL = "https://steamcommunity.com/market/search/render/"
	// DefaultPullCount preserves the legacy catalog page size.
	DefaultPullCount = 100
	// MaxPullCount is the legacy client clamp; Steam's actual cap is unverified.
	MaxPullCount = 100
)

// steamSearchResponse is the legacy candidate catalog projection.
type steamSearchResponse struct {
	Success    bool `json:"success"`
	Start      int  `json:"start"`
	TotalCount int  `json:"total_count"`
	Results    []struct {
		Name     string `json:"name"`
		HashName string `json:"hash_name"`
		Appid    int    `json:"appid"`
		Asset    struct {
			Appid          int    `json:"appid"`
			IconURL        string `json:"icon_url"`
			MarketHashName string `json:"market_hash_name"`
			ClassID        string `json:"classid"`
			Commodity      int    `json:"commodity"` // Raw integer; meaning is unverified.
		} `json:"asset_description"`
	} `json:"results"`
}

// ParseSteamMarketSearchCatalog applies the legacy catalog projection to a
// search/render body. Its identity fallbacks and field meanings are unverified.
func ParseSteamMarketSearchCatalog(body []byte, defaultAppID int64) ([]Item, int, error) {
	var data steamSearchResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, 0, fmt.Errorf("steam market json: %w", err)
	}
	if !data.Success && len(data.Results) == 0 {
		return nil, 0, fmt.Errorf("steam market search unsuccessful")
	}

	out := make([]Item, 0, len(data.Results))
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
		it := Item{
			AppID:          appid,
			MarketHashName: hash,
			Name:           strings.TrimSpace(r.Name),
			IconURL:        strings.TrimSpace(r.Asset.IconURL),
			ClassID:        strings.TrimSpace(r.Asset.ClassID),
			Commodity:      r.Asset.Commodity == 1,
		}
		if err := it.Normalize(); err != nil {
			return nil, 0, fmt.Errorf("result[%d]: %w", i, err)
		}
		out = append(out, it)
	}
	return out, data.TotalCount, nil
}

// PullOptions controls a legacy Steam market catalog pull.
type PullOptions struct {
	AppID      int64
	Start      int
	Count      int
	MaxPages   int // 0 or 1 = single page (minimal); >1 paginates
	BaseURL    string
	HTTPClient *http.Client
	UserAgent  string
}

// Puller fetches Steam market search pages into legacy catalog items.
type Puller struct {
	client    *http.Client
	baseURL   string
	userAgent string
}

// NewPuller builds a Puller with legacy compatibility defaults.
func NewPuller(opts PullOptions) *Puller {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = DefaultSteamSearchURL
	}
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = "buffgo-catalog/1.0 (+https://github.com/local/buff-go; market catalog pull)"
	}
	return &Puller{client: client, baseURL: base, userAgent: ua}
}

// PullResult is one minimal or multi-page pull outcome.
type PullResult struct {
	Items      []Item
	TotalCount int
	Pages      int
}

// Pull fetches one or more market search pages for appid.
// Minimal mode: MaxPages <= 1 pulls a single page (default count 100).
func (p *Puller) Pull(ctx context.Context, opts PullOptions) (PullResult, error) {
	var res PullResult
	if opts.AppID <= 0 {
		return res, fmt.Errorf("appid is required")
	}
	count := opts.Count
	if count <= 0 {
		count = DefaultPullCount
	}
	if count > MaxPullCount {
		count = MaxPullCount
	}
	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}

	start := opts.Start
	if start < 0 {
		start = 0
	}

	seen := make(map[string]struct{})
	for page := 0; page < maxPages; page++ {
		body, err := p.fetchPage(ctx, opts.AppID, start, count)
		if err != nil {
			return res, err
		}
		items, total, err := ParseSteamMarketSearchCatalog(body, opts.AppID)
		if err != nil {
			return res, err
		}
		res.TotalCount = total
		res.Pages++
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			key := itemKey(it.AppID, it.MarketHashName)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			res.Items = append(res.Items, it)
		}
		start += count
		if total > 0 && start >= total {
			break
		}
		// If Steam returned fewer than requested, no more pages.
		if len(items) < count {
			break
		}
	}
	return res, nil
}

func (p *Puller) fetchPage(ctx context.Context, appid int64, start, count int) ([]byte, error) {
	u, err := url.Parse(p.baseURL)
	if err != nil {
		return nil, telemetry.WrapError("steam catalog base URL parse", err)
	}
	q := u.Query()
	q.Set("query", "")
	q.Set("start", strconv.Itoa(start))
	q.Set("count", strconv.Itoa(count))
	q.Set("search_descriptions", "0")
	q.Set("sort_column", "popular")
	q.Set("sort_dir", "desc")
	q.Set("appid", strconv.FormatInt(appid, 10))
	q.Set("norender", "1")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, telemetry.WrapError("steam catalog request build", err)
	}
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://steamcommunity.com/market/search?appid="+strconv.FormatInt(appid, 10))

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, telemetry.WrapError("steam catalog request", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, telemetry.WrapError("steam catalog response read", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("steam catalog http status %d", resp.StatusCode)
	}
	return body, nil
}
