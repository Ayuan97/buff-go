package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// PriceOverviewRequest selects the localized summary for one exact item.
type PriceOverviewRequest struct {
	AppID          int64
	MarketHashName string
	Country        string
	Currency       int
}

// PriceOverview is Steam's localized lowest, median, and recent volume summary.
type PriceOverview struct {
	Success     bool   `json:"success"`
	LowestPrice string `json:"lowest_price"`
	MedianPrice string `json:"median_price"`
	Volume      string `json:"volume"`
}

// PricePoint is one median price and purchase-volume bucket.
type PricePoint struct {
	Time        int64   `json:"time"`
	MedianPrice float64 `json:"price_median"`
	Purchases   int64   `json:"purchases"`
}

// PriceHistory contains the complete grouped history returned by the new market UI.
type PriceHistory struct {
	Currency int          `json:"ecurrency"`
	Prices   []PricePoint `json:"prices"`
}

// PriceOverview reads the legacy lightweight summary endpoint.
func (c *Client) PriceOverview(ctx context.Context, request PriceOverviewRequest) (PriceOverview, error) {
	if err := validateItemKey(request.AppID, request.MarketHashName); err != nil {
		return PriceOverview{}, err
	}
	if request.Currency < 1 {
		return PriceOverview{}, fmt.Errorf("steam price overview currency must be positive")
	}
	query := url.Values{}
	query.Set("appid", strconv.FormatInt(request.AppID, 10))
	query.Set("market_hash_name", request.MarketHashName)
	query.Set("currency", strconv.Itoa(request.Currency))
	if request.Country != "" {
		query.Set("country", request.Country)
	}
	body, err := c.get(ctx, priceOverviewPath, query, false)
	if err != nil {
		return PriceOverview{}, err
	}
	var result *PriceOverview
	if err := json.Unmarshal(body, &result); err != nil {
		return PriceOverview{}, fmt.Errorf("priceoverview json: %w", err)
	}
	if result == nil {
		return PriceOverview{}, fmt.Errorf("priceoverview returned no data")
	}
	return *result, nil
}

// PriceHistory reads the anonymous QueryPriceHistory action used by the new UI.
func (c *Client) PriceHistory(ctx context.Context, appID int64, marketHashName string) (PriceHistory, error) {
	if err := validateItemKey(appID, marketHashName); err != nil {
		return PriceHistory{}, err
	}
	body, err := c.queryAction(ctx, priceHistoryAction, appID, marketHashName)
	if err != nil {
		return PriceHistory{}, err
	}
	return decodeQueryAction[PriceHistory](body, priceHistoryAction)
}
