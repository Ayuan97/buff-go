package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// SearchRequest describes one legacy market catalog page.
type SearchRequest struct {
	AppID              int64
	Query              string
	Start              int
	Count              int
	SearchDescriptions bool
	SortColumn         string
	SortDirection      string
	NoRender           bool
	PriceCurrency      int
	PriceMin           *int64
	PriceMax           *int64
	Filters            map[string][]string
}

// SearchResponse is one Steam catalog page.
type SearchResponse struct {
	Success    bool           `json:"success"`
	Start      int            `json:"start"`
	PageSize   int            `json:"pagesize"`
	TotalCount int            `json:"total_count"`
	Results    []SearchResult `json:"results"`
}

// SearchResult is one grouped Steam market item.
type SearchResult struct {
	HashName         string                 `json:"hash_name"`
	SellListings     int64                  `json:"sell_listings"`
	SellPrice        int64                  `json:"sell_price"`
	SellPriceText    string                 `json:"sell_price_text"`
	SalePriceText    string                 `json:"sale_price_text"`
	AssetDescription SearchAssetDescription `json:"asset_description"`
}

// SearchAssetDescription contains stable identity and display fields from search.
type SearchAssetDescription struct {
	AppID                 int64  `json:"appid"`
	MarketHashName        string `json:"market_hash_name"`
	MarketBucketGroupID   string `json:"market_bucket_group_id"`
	MarketBucketGroupName string `json:"market_bucket_group_name"`
	IconURL               string `json:"icon_url"`
	Type                  string `json:"type"`
	NameColor             string `json:"name_color"`
}

// Search reads one market/search/render page and preserves its raw payload.
func (c *Client) Search(ctx context.Context, request SearchRequest) (SearchResponse, []byte, error) {
	if request.AppID < 1 {
		return SearchResponse{}, nil, fmt.Errorf("steam search appid must be positive")
	}
	if request.Start < 0 || request.Count < 1 {
		return SearchResponse{}, nil, fmt.Errorf("steam search start/count is invalid")
	}
	query := url.Values{}
	query.Set("query", request.Query)
	query.Set("start", strconv.Itoa(request.Start))
	query.Set("count", strconv.Itoa(request.Count))
	query.Set("search_descriptions", boolFlag(request.SearchDescriptions))
	query.Set("sort_column", request.SortColumn)
	query.Set("sort_dir", request.SortDirection)
	query.Set("appid", strconv.FormatInt(request.AppID, 10))
	if request.NoRender {
		query.Set("norender", "1")
	}
	if request.PriceMin != nil || request.PriceMax != nil {
		if request.PriceCurrency < 1 {
			return SearchResponse{}, nil, fmt.Errorf("steam search price currency must be positive")
		}
		query.Set("price_currency", strconv.Itoa(request.PriceCurrency))
		if request.PriceMin != nil {
			query.Set("price_min", strconv.FormatInt(*request.PriceMin, 10))
		}
		if request.PriceMax != nil {
			query.Set("price_max", strconv.FormatInt(*request.PriceMax, 10))
		}
	}
	for key, values := range request.Filters {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	body, err := c.get(ctx, searchRenderPath, query, false)
	if err != nil {
		return SearchResponse{}, nil, err
	}
	parsed, err := parseSearch(body)
	return parsed, body, err
}

func parseSearch(body []byte) (SearchResponse, error) {
	type response struct {
		Success    bool           `json:"success"`
		Start      int            `json:"start"`
		PageSize   int            `json:"pagesize"`
		TotalCount *int           `json:"total_count"`
		Results    []SearchResult `json:"results"`
	}
	var decoded response
	if err := json.Unmarshal(body, &decoded); err != nil {
		return SearchResponse{}, fmt.Errorf("search/render json: %w", err)
	}
	if !decoded.Success {
		return SearchResponse{}, fmt.Errorf("search/render success=false")
	}
	if decoded.TotalCount == nil {
		return SearchResponse{}, fmt.Errorf("search/render missing total_count")
	}
	parsed := SearchResponse{
		Success: decoded.Success, Start: decoded.Start, PageSize: decoded.PageSize,
		TotalCount: *decoded.TotalCount, Results: decoded.Results,
	}
	if parsed.Start < 0 || parsed.PageSize < 0 || parsed.TotalCount < 0 {
		return SearchResponse{}, fmt.Errorf("search/render pagination is negative")
	}
	return parsed, nil
}

func boolFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
