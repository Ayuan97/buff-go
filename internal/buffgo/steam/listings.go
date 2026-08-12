package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ListingsRender fetches the candidate individual-listings endpoint.
func (c *Client) ListingsRender(ctx context.Context, appid int64, marketHashName string, start, count, currency int) (*ListingsPage, error) {
	if appid <= 0 {
		return nil, fmt.Errorf("steam.ListingsRender: appid is required")
	}
	marketHashName = strings.TrimSpace(marketHashName)
	if marketHashName == "" {
		return nil, fmt.Errorf("steam.ListingsRender: market_hash_name is required")
	}
	if count <= 0 {
		count = 10
	}
	if currency <= 0 {
		currency = legacyPriceOverviewDefaultCurrencyID
	}
	q := url.Values{}
	q.Set("query", "")
	q.Set("start", strconv.Itoa(start))
	q.Set("count", strconv.Itoa(count))
	q.Set("country", c.Country)
	q.Set("language", c.Language)
	q.Set("currency", strconv.Itoa(currency))
	path := fmt.Sprintf("https://steamcommunity.com/market/listings/%d/%s/render/", appid, url.PathEscape(marketHashName))
	rawURL := path + "?" + q.Encode()
	body, _, err := c.get(ctx, rawURL, listingReferer(appid, marketHashName))
	if err != nil {
		return nil, err
	}
	// beta may return HTML; require JSON
	trim := strings.TrimSpace(string(body))
	if strings.HasPrefix(trim, "<!") {
		return nil, fmt.Errorf("steam.ListingsRender: got HTML (market beta layout); use ItemBook/OrderBook for commodity depth")
	}
	return ParseListingsRender(body)
}

// ParseListingsRender parses listings render JSON.
func ParseListingsRender(body []byte) (*ListingsPage, error) {
	var raw struct {
		Success     bool `json:"success"`
		Start       int  `json:"start"`
		PageSize    int  `json:"pagesize"`
		TotalCount  int  `json:"total_count"`
		ListingInfo map[string]struct {
			ListingID      string `json:"listingid"`
			Price          int64  `json:"price"`
			Fee            int64  `json:"fee"`
			CurrencyID     int    `json:"currencyid"`
			SteamFee       int64  `json:"steam_fee"`
			PublisherFee   int64  `json:"publisher_fee"`
			ConvertedPrice int64  `json:"converted_price"`
			ConvertedFee   int64  `json:"converted_fee"`
			Asset          struct {
				ID     string `json:"id"`
				Amount string `json:"amount"`
			} `json:"asset"`
		} `json:"listinginfo"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("steam.ListingsRender: decode: %w", err)
	}
	if !raw.Success {
		return nil, fmt.Errorf("steam.ListingsRender: success=false")
	}
	page := &ListingsPage{
		Success:    true,
		Start:      raw.Start,
		PageSize:   raw.PageSize,
		TotalCount: raw.TotalCount,
	}
	for _, li := range raw.ListingInfo {
		if li.ListingID == "" {
			continue
		}
		page.Listings = append(page.Listings, Listing{
			ListingID:      li.ListingID,
			PriceMinor:     li.Price,
			FeeMinor:       li.Fee,
			ConvertedPrice: li.ConvertedPrice,
			ConvertedFee:   li.ConvertedFee,
			CurrencyID:     li.CurrencyID,
			SteamFee:       li.SteamFee,
			PublisherFee:   li.PublisherFee,
			AssetID:        li.Asset.ID,
			Amount:         li.Asset.Amount,
		})
	}
	return page, nil
}

// RecentCompleted fetches the global recent completions feed.
func (c *Client) RecentCompleted(ctx context.Context) (*RecentCompleted, error) {
	body, _, err := c.get(ctx, "https://steamcommunity.com/market/recentcompleted", "https://steamcommunity.com/market/")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Success      bool           `json:"success"`
		More         bool           `json:"more"`
		LastTime     int64          `json:"last_time"`
		LastListing  string         `json:"last_listing"`
		ListingInfo  map[string]any `json:"listinginfo"`
		PurchaseInfo map[string]any `json:"purchaseinfo"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("steam.RecentCompleted: decode: %w", err)
	}
	out := &RecentCompleted{
		Success:     raw.Success,
		More:        raw.More,
		LastTime:    raw.LastTime,
		LastListing: raw.LastListing,
	}
	for id := range raw.ListingInfo {
		out.ListingIDs = append(out.ListingIDs, id)
	}
	for id := range raw.PurchaseInfo {
		out.PurchaseIDs = append(out.PurchaseIDs, id)
	}
	return out, nil
}
