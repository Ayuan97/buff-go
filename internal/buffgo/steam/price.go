package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// PriceOverview fetches the candidate priceoverview endpoint.
// Authentication and currency semantics are unverified; zero uses legacy ID 1.
func (c *Client) PriceOverview(ctx context.Context, appid int64, marketHashName string, currency int) (*PriceOverview, error) {
	if appid <= 0 {
		return nil, fmt.Errorf("steam.PriceOverview: appid is required")
	}
	marketHashName = strings.TrimSpace(marketHashName)
	if marketHashName == "" {
		return nil, fmt.Errorf("steam.PriceOverview: market_hash_name is required")
	}
	if currency <= 0 {
		currency = legacyPriceOverviewDefaultCurrencyID
	}
	q := url.Values{}
	q.Set("appid", strconv.FormatInt(appid, 10))
	q.Set("currency", strconv.Itoa(currency))
	q.Set("market_hash_name", marketHashName)
	rawURL := "https://steamcommunity.com/market/priceoverview/?" + q.Encode()
	body, _, err := c.get(ctx, rawURL, listingReferer(appid, marketHashName))
	if err != nil {
		return nil, err
	}
	return ParsePriceOverview(body, appid, marketHashName, currency)
}

// ParsePriceOverview parses priceoverview JSON.
func ParsePriceOverview(body []byte, appid int64, marketHash string, currency int) (*PriceOverview, error) {
	var raw struct {
		Success     bool   `json:"success"`
		LowestPrice string `json:"lowest_price"`
		MedianPrice string `json:"median_price"`
		Volume      string `json:"volume"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("steam.PriceOverview: decode: %w", err)
	}
	if !raw.Success {
		return nil, fmt.Errorf("steam.PriceOverview: success=false")
	}
	return &PriceOverview{
		Success:     true,
		LowestPrice: raw.LowestPrice,
		MedianPrice: raw.MedianPrice,
		Volume:      raw.Volume,
		Currency:    currency,
		AppID:       appid,
		MarketHash:  marketHash,
	}, nil
}

// PriceHistory fetches the candidate pricehistory endpoint. This legacy client
// requires a Cookie, but the endpoint's real authentication contract is unverified.
func (c *Client) PriceHistory(ctx context.Context, appid int64, marketHashName string) (*PriceHistory, error) {
	if appid <= 0 {
		return nil, fmt.Errorf("steam.PriceHistory: appid is required")
	}
	marketHashName = strings.TrimSpace(marketHashName)
	if marketHashName == "" {
		return nil, fmt.Errorf("steam.PriceHistory: market_hash_name is required")
	}
	if c.Cookie == "" {
		return nil, fmt.Errorf("steam.PriceHistory: community cookie is required")
	}
	q := url.Values{}
	q.Set("appid", strconv.FormatInt(appid, 10))
	q.Set("market_hash_name", marketHashName)
	rawURL := "https://steamcommunity.com/market/pricehistory/?" + q.Encode()
	body, _, err := c.get(ctx, rawURL, listingReferer(appid, marketHashName))
	if err != nil {
		return nil, err
	}
	return ParsePriceHistory(body, appid, marketHashName)
}

// ParsePriceHistory parses pricehistory JSON.
func ParsePriceHistory(body []byte, appid int64, marketHash string) (*PriceHistory, error) {
	// The legacy client treats [] and null as unavailable; they do not prove why.
	trim := strings.TrimSpace(string(body))
	if trim == "[]" || trim == "null" {
		return nil, fmt.Errorf("steam.PriceHistory: empty response (unavailable or item has no history)")
	}
	var raw struct {
		Success     bool            `json:"success"`
		PricePrefix string          `json:"price_prefix"`
		PriceSuffix string          `json:"price_suffix"`
		Prices      [][]interface{} `json:"prices"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("steam.PriceHistory: decode: %w", err)
	}
	// success may be bool true or number 1
	out := &PriceHistory{
		Success:     true,
		PricePrefix: raw.PricePrefix,
		PriceSuffix: raw.PriceSuffix,
		AppID:       appid,
		MarketHash:  marketHash,
		Points:      make([]PriceHistoryPoint, 0, len(raw.Prices)),
	}
	for _, row := range raw.Prices {
		if len(row) < 3 {
			continue
		}
		pt := PriceHistoryPoint{}
		if s, ok := row[0].(string); ok {
			pt.TimeRaw = s
		}
		switch v := row[1].(type) {
		case float64:
			pt.Price = v
		case json.Number:
			f, _ := v.Float64()
			pt.Price = f
		}
		switch v := row[2].(type) {
		case string:
			pt.Volume = v
		case float64:
			pt.Volume = strconv.FormatInt(int64(v), 10)
		}
		out.Points = append(out.Points, pt)
	}
	if !raw.Success && len(out.Points) == 0 {
		return nil, fmt.Errorf("steam.PriceHistory: success=false")
	}
	return out, nil
}
