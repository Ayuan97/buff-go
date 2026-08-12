package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const defaultSearchCount = 100
const maxSearchCount = 100

// Search calls market/search/render and preserves its legacy wire fields.
// Goal 0B has not verified their ask, amount, or count semantics.
//
// When Client.SearchLimiter is set, each call reserves its configured legacy
// per-proxy budget. HTTP 429 applies the configured cooldown and returns
// ErrSearchCooling; these settings are not verified Steam policy evidence.
func (c *Client) Search(ctx context.Context, p SearchParams) (*SearchPage, error) {
	if p.AppID <= 0 {
		return nil, fmt.Errorf("steam.Search: appid is required")
	}
	if err := c.allowSearch(ctx); err != nil {
		return nil, err
	}
	count := p.Count
	if count <= 0 {
		count = defaultSearchCount
	}
	if count > maxSearchCount {
		count = maxSearchCount
	}
	start := p.Start
	if start < 0 {
		start = 0
	}
	sortCol := strings.TrimSpace(p.SortColumn)
	if sortCol == "" {
		sortCol = "popular"
	}
	sortDir := strings.TrimSpace(p.SortDir)
	if sortDir == "" {
		sortDir = "desc"
	}
	desc := 0
	if p.SearchDescriptions {
		desc = 1
	}

	q := url.Values{}
	q.Set("query", p.Query)
	q.Set("start", strconv.Itoa(start))
	q.Set("count", strconv.Itoa(count))
	q.Set("search_descriptions", strconv.Itoa(desc))
	q.Set("sort_column", sortCol)
	q.Set("sort_dir", sortDir)
	q.Set("appid", strconv.FormatInt(p.AppID, 10))
	q.Set("norender", "1")
	for facet, tags := range p.Categories {
		facet = strings.TrimSpace(facet)
		if facet == "" {
			continue
		}
		key := "category_" + facet + "[]"
		for _, tag := range tags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if !strings.HasPrefix(tag, "tag_") {
				tag = "tag_" + tag
			}
			q.Add(key, tag)
		}
	}

	rawURL := "https://steamcommunity.com/market/search/render/?" + q.Encode()
	referer := "https://steamcommunity.com/market/search?appid=" + strconv.FormatInt(p.AppID, 10)
	body, status, err := c.get(ctx, rawURL, referer)
	if status == http.StatusTooManyRequests {
		c.markSearch429(ctx)
		return nil, fmt.Errorf("steam.Search: http 429: %w", ErrSearchCooling)
	}
	if err != nil {
		return nil, err
	}
	return ParseSearchPage(body, p.AppID)
}

// SearchAll pages through search/render until MaxPages or end of results.
// maxPages <= 0 means 1 page. pageDelay is applied by the caller if needed.
func (c *Client) SearchAll(ctx context.Context, p SearchParams, maxPages int) ([]SearchResult, int, error) {
	if maxPages <= 0 {
		maxPages = 1
	}
	count := p.Count
	if count <= 0 {
		count = defaultSearchCount
	}
	if count > maxSearchCount {
		count = maxSearchCount
	}
	var out []SearchResult
	total := 0
	start := p.Start
	if start < 0 {
		start = 0
	}
	for page := 0; page < maxPages; page++ {
		pp := p
		pp.Start = start
		pp.Count = count
		res, err := c.Search(ctx, pp)
		if err != nil {
			return out, total, err
		}
		total = res.TotalCount
		out = append(out, res.Results...)
		if len(res.Results) == 0 {
			break
		}
		start += count
		if start >= total {
			break
		}
	}
	return out, total, nil
}

type searchJSON struct {
	Success    bool `json:"success"`
	Start      int  `json:"start"`
	PageSize   int  `json:"pagesize"`
	TotalCount int  `json:"total_count"`
	Results    []struct {
		Name          string `json:"name"`
		HashName      string `json:"hash_name"`
		SellListings  int    `json:"sell_listings"`
		SellPrice     int64  `json:"sell_price"`
		SellPriceText string `json:"sell_price_text"`
		SalePriceText string `json:"sale_price_text"`
		AppName       string `json:"app_name"`
		Asset         struct {
			AppID          int64  `json:"appid"`
			ClassID        string `json:"classid"`
			IconURL        string `json:"icon_url"`
			Name           string `json:"name"`
			Type           string `json:"type"`
			MarketName     string `json:"market_name"`
			MarketHashName string `json:"market_hash_name"`
			Commodity      int    `json:"commodity"`
		} `json:"asset_description"`
	} `json:"results"`
}

// ParseSearchPage parses search/render JSON (norender=1).
func ParseSearchPage(body []byte, defaultAppID int64) (*SearchPage, error) {
	var raw searchJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("steam.Search: decode: %w", err)
	}
	if !raw.Success {
		return nil, fmt.Errorf("steam.Search: success=false")
	}
	page := &SearchPage{
		Success:    true,
		Start:      raw.Start,
		PageSize:   raw.PageSize,
		TotalCount: raw.TotalCount,
		Results:    make([]SearchResult, 0, len(raw.Results)),
	}
	for _, r := range raw.Results {
		appid := r.Asset.AppID
		if appid <= 0 {
			appid = defaultAppID
		}
		hash := strings.TrimSpace(r.Asset.MarketHashName)
		if hash == "" {
			hash = strings.TrimSpace(r.HashName)
		}
		page.Results = append(page.Results, SearchResult{
			Name:           strings.TrimSpace(r.Name),
			HashName:       strings.TrimSpace(r.HashName),
			SellListings:   r.SellListings,
			SellPriceCents: r.SellPrice,
			SellPriceText:  r.SellPriceText,
			SalePriceText:  r.SalePriceText,
			AppID:          appid,
			AppName:        r.AppName,
			ClassID:        r.Asset.ClassID,
			Commodity:      r.Asset.Commodity == 1,
			IconURL:        r.Asset.IconURL,
			Type:           r.Asset.Type,
			MarketHashName: hash,
		})
	}
	return page, nil
}

// AppFilters loads category / item-type facets for an appid.
func (c *Client) AppFilters(ctx context.Context, appid int64) (*AppFilters, error) {
	if appid <= 0 {
		return nil, fmt.Errorf("steam.AppFilters: appid is required")
	}
	rawURL := fmt.Sprintf("https://steamcommunity.com/market/appfilters/%d", appid)
	body, _, err := c.get(ctx, rawURL, "https://steamcommunity.com/market/search?appid="+strconv.FormatInt(appid, 10))
	if err != nil {
		return nil, err
	}
	return ParseAppFilters(body, appid)
}

type appFiltersJSON struct {
	Success bool `json:"success"`
	Facets  map[string]struct {
		AppID         int64  `json:"appid"`
		Name          string `json:"name"`
		LocalizedName string `json:"localized_name"`
		Tags          map[string]struct {
			LocalizedName string `json:"localized_name"`
			Matches       any    `json:"matches"`
		} `json:"tags"`
	} `json:"facets"`
}

// ParseAppFilters parses appfilters JSON.
func ParseAppFilters(body []byte, appid int64) (*AppFilters, error) {
	var raw appFiltersJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("steam.AppFilters: decode: %w", err)
	}
	out := &AppFilters{Success: raw.Success, AppID: appid}
	for id, f := range raw.Facets {
		facet := Facet{
			ID:            id,
			Name:          f.Name,
			LocalizedName: f.LocalizedName,
		}
		for tag, t := range f.Tags {
			facet.Tags = append(facet.Tags, FacetTag{
				Tag:           tag,
				LocalizedName: t.LocalizedName,
				Matches:       parseMatches(t.Matches),
			})
		}
		out.Facets = append(out.Facets, facet)
	}
	return out, nil
}

func parseMatches(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		s := strings.ReplaceAll(x, ",", "")
		n, _ := strconv.Atoi(s)
		return n
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	default:
		return 0
	}
}
