package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// OrderBookResponse is the Steam orderbook result envelope.
type OrderBookResponse struct {
	Success bool           `json:"success"`
	Data    *OrderBookData `json:"data"`
}

// OrderBookData contains best prices, totals, and compact price/quantity pairs.
type OrderBookData struct {
	MaxBuyOrder       *int64  `json:"amtMaxBuyOrder"`
	MinSellOrder      *int64  `json:"amtMinSellOrder"`
	Currency          int     `json:"eCurrency"`
	BuyOrders         int64   `json:"cBuyOrders"`
	SellOrders        int64   `json:"cSellOrders"`
	CompactBuyOrders  []int64 `json:"rgCompactBuyOrders"`
	CompactSellOrders []int64 `json:"rgCompactSellOrders"`
}

// OrderBook reads the grouped bid and ask book for one exact market hash name.
func (c *Client) OrderBook(ctx context.Context, appID int64, marketHashName string) (OrderBookResponse, error) {
	if err := validateItemKey(appID, marketHashName); err != nil {
		return OrderBookResponse{}, err
	}
	encoded, err := encodeQueryActionParams(appID, marketHashName)
	if err != nil {
		return OrderBookResponse{}, err
	}
	query := url.Values{}
	query.Set("q", orderBookAction)
	query.Set("qp", encoded)
	body, err := c.get(ctx, orderbookPath, query, true)
	if err != nil {
		return OrderBookResponse{}, err
	}
	return parseOrderBook(body)
}

func parseOrderBook(body []byte) (OrderBookResponse, error) {
	payload, err := unwrapOrderBookEnvelope(body)
	if err != nil {
		return OrderBookResponse{}, err
	}
	var parsed OrderBookResponse
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return OrderBookResponse{}, fmt.Errorf("orderbook json: %w", err)
	}
	return parsed, nil
}

// Steam Query Action wraps the legacy response as {"data":{success,data}}.
func unwrapOrderBookEnvelope(body []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, fmt.Errorf("orderbook json: %w", err)
	}
	if _, hasSuccess := top["success"]; hasSuccess {
		return body, nil
	}
	inner, ok := top["data"]
	if !ok || len(inner) == 0 || string(inner) == "null" {
		return body, nil
	}
	return inner, nil
}
