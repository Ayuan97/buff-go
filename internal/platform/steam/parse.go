package steam

import (
	"encoding/json"
	"fmt"
	"strings"

	"buff-go/internal/market"
)

// evidencedCNYCurrency is the logged-in wallet eCurrency that matched ¥ fen
// on 2026-08-13. It is not a guessed global Steam enum.
const evidencedCNYCurrency = 23

type searchRenderResponse struct {
	Success    bool           `json:"success"`
	Start      int            `json:"start"`
	PageSize   int            `json:"pagesize"`
	TotalCount int            `json:"total_count"`
	Results    []searchResult `json:"results"`
}

type searchResult struct {
	HashName         string `json:"hash_name"`
	SellListings     int64  `json:"sell_listings"`
	SellPrice        int64  `json:"sell_price"`
	SellPriceText    string `json:"sell_price_text"`
	SalePriceText    string `json:"sale_price_text"`
	AssetDescription struct {
		AppID               int64  `json:"appid"`
		MarketHashName      string `json:"market_hash_name"`
		MarketBucketGroupID string `json:"market_bucket_group_id"`
	} `json:"asset_description"`
}

type orderbookResponse struct {
	Success bool           `json:"success"`
	Data    *orderbookData `json:"data"`
}

type orderbookData struct {
	AmtMaxBuyOrder      *int64  `json:"amtMaxBuyOrder"`
	AmtMinSellOrder     *int64  `json:"amtMinSellOrder"`
	ECurrency           int     `json:"eCurrency"`
	CBuyOrders          int64   `json:"cBuyOrders"`
	CSellOrders         int64   `json:"cSellOrders"`
	RgCompactBuyOrders  []int64 `json:"rgCompactBuyOrders"`
	RgCompactSellOrders []int64 `json:"rgCompactSellOrders"`
}

func parseSearchRender(body []byte) (searchRenderResponse, error) {
	var parsed searchRenderResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return searchRenderResponse{}, fmt.Errorf("search/render json: %w", err)
	}
	if !parsed.Success {
		return searchRenderResponse{}, fmt.Errorf("search/render success=false")
	}
	if parsed.Start < 0 || parsed.PageSize < 0 || parsed.TotalCount < 0 {
		return searchRenderResponse{}, fmt.Errorf("search/render pagination is negative")
	}
	return parsed, nil
}

func hashNameOf(item searchResult) (string, error) {
	hash := strings.TrimSpace(item.AssetDescription.MarketHashName)
	if hash == "" {
		hash = strings.TrimSpace(item.HashName)
	}
	if hash == "" {
		return "", fmt.Errorf("missing market_hash_name")
	}
	if item.HashName != "" && item.AssetDescription.MarketHashName != "" && item.HashName != item.AssetDescription.MarketHashName {
		return "", fmt.Errorf("hash_name mismatch")
	}
	return hash, nil
}

func parseYuanAsk(item searchResult) (market.CNYCents, error) {
	textCents, err := parseYuanCents(item.SellPriceText)
	if err != nil {
		return 0, err
	}
	if item.SellPrice < 0 {
		return 0, fmt.Errorf("sell_price is negative")
	}
	if market.CNYCents(item.SellPrice) != textCents {
		return 0, fmt.Errorf("sell_price does not match ¥ text")
	}
	return textCents, nil
}

func parseYuanCents(text string) (market.CNYCents, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "¥") {
		return 0, fmt.Errorf("price text is not yuan")
	}
	numeric := strings.TrimSpace(strings.TrimPrefix(text, "¥"))
	return market.ParseCNYCents(numeric)
}

func parseOrderbook(body []byte) (orderbookResponse, error) {
	var parsed orderbookResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return orderbookResponse{}, fmt.Errorf("orderbook json: %w", err)
	}
	return parsed, nil
}

func compactFirstPrice(levels []int64) (int64, bool) {
	if len(levels) < 2 || len(levels)%2 != 0 {
		return 0, false
	}
	return levels[0], true
}

func orderbookBest(side market.Side, data orderbookData) (cents market.CNYCents, empty bool, err error) {
	if data.ECurrency != evidencedCNYCurrency {
		return 0, false, fmt.Errorf("orderbook eCurrency %d is not evidenced CNY", data.ECurrency)
	}
	switch side {
	case market.SideBid:
		if data.AmtMaxBuyOrder == nil && data.CBuyOrders == 0 && len(data.RgCompactBuyOrders) == 0 {
			return 0, true, nil
		}
		if data.AmtMaxBuyOrder == nil {
			return 0, false, fmt.Errorf("bid present without amtMaxBuyOrder")
		}
		first, ok := compactFirstPrice(data.RgCompactBuyOrders)
		if !ok || first != *data.AmtMaxBuyOrder {
			return 0, false, fmt.Errorf("amtMaxBuyOrder does not match first compact buy")
		}
		if *data.AmtMaxBuyOrder < 0 {
			return 0, false, fmt.Errorf("amtMaxBuyOrder is negative")
		}
		return market.CNYCents(*data.AmtMaxBuyOrder), false, nil
	case market.SideAsk:
		if data.AmtMinSellOrder == nil && data.CSellOrders == 0 && len(data.RgCompactSellOrders) == 0 {
			return 0, true, nil
		}
		if data.AmtMinSellOrder == nil {
			return 0, false, fmt.Errorf("ask present without amtMinSellOrder")
		}
		first, ok := compactFirstPrice(data.RgCompactSellOrders)
		if !ok || first != *data.AmtMinSellOrder {
			return 0, false, fmt.Errorf("amtMinSellOrder does not match first compact sell")
		}
		if *data.AmtMinSellOrder < 0 {
			return 0, false, fmt.Errorf("amtMinSellOrder is negative")
		}
		return market.CNYCents(*data.AmtMinSellOrder), false, nil
	default:
		return 0, false, fmt.Errorf("invalid side")
	}
}
