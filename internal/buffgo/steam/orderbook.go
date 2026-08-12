package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ItemBookParams selects which order-book API to call for one item.
type ItemBookParams struct {
	AppID          int64
	MarketHashName string
	// ItemNameID is the legacy histogram parameter. This client resolves it when empty;
	// the endpoint's real requirement and identity role remain unverified.
	ItemNameID string
	Currency   int    // Raw currency parameter; zero sends legacy ID 1.
	Country    string // default client.Country
	Language   string // default client.Language
	// PreferOrderBook forces beta /market/orderbook instead of histogram.
	PreferOrderBook bool
}

// ItemBook returns the legacy interpretation of one candidate depth response.
// Goal 0B has not verified its bid/ask, unit, or quantity semantics.
func (c *Client) ItemBook(ctx context.Context, p ItemBookParams) (*OrderBook, error) {
	if p.AppID <= 0 {
		return nil, fmt.Errorf("steam.ItemBook: appid is required")
	}
	p.MarketHashName = strings.TrimSpace(p.MarketHashName)
	if p.MarketHashName == "" {
		return nil, fmt.Errorf("steam.ItemBook: market_hash_name is required")
	}
	if p.PreferOrderBook {
		return c.OrderBook(ctx, p.AppID, p.MarketHashName)
	}
	nameid := strings.TrimSpace(p.ItemNameID)
	if nameid == "" {
		id, err := c.ResolveItemNameID(ctx, p.AppID, p.MarketHashName)
		if err != nil {
			// fallback to beta orderbook when nameid unavailable
			ob, err2 := c.OrderBook(ctx, p.AppID, p.MarketHashName)
			if err2 != nil {
				return nil, fmt.Errorf("steam.ItemBook: nameid: %v; orderbook: %w", err, err2)
			}
			return ob, nil
		}
		nameid = id
	}
	return c.ItemOrdersHistogram(ctx, ItemBookParams{
		AppID:          p.AppID,
		MarketHashName: p.MarketHashName,
		ItemNameID:     nameid,
		Currency:       p.Currency,
		Country:        p.Country,
		Language:       p.Language,
	})
}

// ItemOrdersHistogram calls the candidate market/itemordershistogram endpoint.
func (c *Client) ItemOrdersHistogram(ctx context.Context, p ItemBookParams) (*OrderBook, error) {
	nameid := strings.TrimSpace(p.ItemNameID)
	if nameid == "" {
		return nil, fmt.Errorf("steam.ItemOrdersHistogram: item_nameid is required")
	}
	currency := p.Currency
	if currency <= 0 {
		currency = legacyPriceOverviewDefaultCurrencyID
	}
	country := strings.TrimSpace(p.Country)
	if country == "" {
		country = c.Country
	}
	lang := strings.TrimSpace(p.Language)
	if lang == "" {
		lang = c.Language
	}
	q := url.Values{}
	q.Set("country", country)
	q.Set("language", lang)
	q.Set("currency", strconv.Itoa(currency))
	q.Set("item_nameid", nameid)
	q.Set("two_factor", "0")
	rawURL := "https://steamcommunity.com/market/itemordershistogram?" + q.Encode()
	ref := listingReferer(p.AppID, p.MarketHashName)
	if p.AppID <= 0 || p.MarketHashName == "" {
		ref = "https://steamcommunity.com/market/"
	}
	body, _, err := c.get(ctx, rawURL, ref)
	if err != nil {
		return nil, err
	}
	return ParseItemOrdersHistogram(body, p)
}

type histogramJSON struct {
	Success          any     `json:"success"`
	HighestBuyOrder  string  `json:"highest_buy_order"`
	LowestSellOrder  string  `json:"lowest_sell_order"`
	BuyOrderGraph    [][]any `json:"buy_order_graph"`
	SellOrderGraph   [][]any `json:"sell_order_graph"`
	PricePrefix      string  `json:"price_prefix"`
	PriceSuffix      string  `json:"price_suffix"`
	SellOrderSummary string  `json:"sell_order_summary"`
	BuyOrderSummary  string  `json:"buy_order_summary"`
}

// ParseItemOrdersHistogram parses histogram JSON into the legacy OrderBook DTO.
func ParseItemOrdersHistogram(body []byte, p ItemBookParams) (*OrderBook, error) {
	var raw histogramJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("steam.histogram: decode: %w", err)
	}
	if !truthy(raw.Success) {
		return nil, fmt.Errorf("steam.histogram: success=false")
	}
	currency := p.Currency
	if currency <= 0 {
		currency = legacyPriceOverviewDefaultCurrencyID
	}
	ob := &OrderBook{
		AppID:          p.AppID,
		MarketHashName: p.MarketHashName,
		ItemNameID:     p.ItemNameID,
		Currency:       currency,
		Country:        p.Country,
		Language:       p.Language,
		LowestSell:     parseInt64String(raw.LowestSellOrder),
		HighestBuy:     parseInt64String(raw.HighestBuyOrder),
		PricePrefix:    raw.PricePrefix,
		PriceSuffix:    raw.PriceSuffix,
		SellGraph:      parseGraph(raw.SellOrderGraph),
		BuyGraph:       parseGraph(raw.BuyOrderGraph),
		Source:         "histogram",
	}
	// Legacy assumption: use the last cumulative graph value as a total.
	if n := len(ob.SellGraph); n > 0 {
		ob.SellOrders = ob.SellGraph[n-1].Cumulative
	}
	if n := len(ob.BuyGraph); n > 0 {
		ob.BuyOrders = ob.BuyGraph[n-1].Cumulative
	}
	return ob, nil
}

// OrderBook calls the candidate beta market/orderbook endpoint.
func (c *Client) OrderBook(ctx context.Context, appid int64, marketHashName string) (*OrderBook, error) {
	if appid <= 0 {
		return nil, fmt.Errorf("steam.OrderBook: appid is required")
	}
	marketHashName = strings.TrimSpace(marketHashName)
	if marketHashName == "" {
		return nil, fmt.Errorf("steam.OrderBook: market_hash_name is required")
	}
	payload, _ := json.Marshal([]any{appid, marketHashName})
	q := url.Values{}
	q.Set("q", "Load")
	q.Set("qp", string(payload))
	rawURL := "https://steamcommunity.com/market/orderbook?" + q.Encode()
	body, _, err := c.getHeader(ctx, rawURL, listingReferer(appid, marketHashName), map[string]string{
		"x-valve-request-type": "queryAction",
		"Accept":               "application/json",
	})
	if err != nil {
		return nil, err
	}
	return ParseOrderBookJSON(body, appid, marketHashName)
}

type orderbookJSON struct {
	Success bool `json:"success"`
	Data    struct {
		AmtMaxBuyOrder      int64   `json:"amtMaxBuyOrder"`
		AmtMinSellOrder     int64   `json:"amtMinSellOrder"`
		ECurrency           int     `json:"eCurrency"`
		CBuyOrders          int     `json:"cBuyOrders"`
		CSellOrders         int     `json:"cSellOrders"`
		RgCompactBuyOrders  []int64 `json:"rgCompactBuyOrders"`
		RgCompactSellOrders []int64 `json:"rgCompactSellOrders"`
	} `json:"data"`
}

// ParseOrderBookJSON parses beta JSON into the legacy OrderBook DTO.
func ParseOrderBookJSON(body []byte, appid int64, marketHash string) (*OrderBook, error) {
	var raw orderbookJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("steam.OrderBook: decode: %w", err)
	}
	if !raw.Success {
		return nil, fmt.Errorf("steam.OrderBook: success=false")
	}
	ob := &OrderBook{
		AppID:          appid,
		MarketHashName: marketHash,
		Currency:       raw.Data.ECurrency,
		LowestSell:     raw.Data.AmtMinSellOrder,
		HighestBuy:     raw.Data.AmtMaxBuyOrder,
		SellOrders:     raw.Data.CSellOrders,
		BuyOrders:      raw.Data.CBuyOrders,
		SellCompact:    parseCompact(raw.Data.RgCompactSellOrders),
		BuyCompact:     parseCompact(raw.Data.RgCompactBuyOrders),
		Source:         "orderbook",
	}
	return ob, nil
}

// ResolveItemNameID loads listing HTML and applies legacy candidate patterns.
func (c *Client) ResolveItemNameID(ctx context.Context, appid int64, marketHashName string) (string, error) {
	if appid <= 0 || strings.TrimSpace(marketHashName) == "" {
		return "", fmt.Errorf("steam.ResolveItemNameID: appid and market_hash_name required")
	}
	rawURL := listingReferer(appid, marketHashName)
	reqURL := fmt.Sprintf("https://steamcommunity.com/market/listings/%d/%s", appid, url.PathEscape(marketHashName))
	body, _, err := c.getHeader(ctx, reqURL, rawURL, map[string]string{
		"Accept": "text/html",
	})
	if err != nil {
		return "", err
	}
	id, err := ParseItemNameID(body)
	if err != nil {
		return "", err
	}
	return id, nil
}

var (
	reLoadOrderSpread = regexp.MustCompile(`Market_LoadOrderSpread\(\s*(\d+)\s*\)`)
	reItemNameID      = regexp.MustCompile(`ItemActivityTicker\.Start\(\s*(\d+)\s*\)`)
	reNameIDJSON      = regexp.MustCompile(`"item_nameid"\s*:\s*"?(\d+)"?`)
)

// ParseItemNameID extracts a candidate item_nameid using legacy HTML patterns.
func ParseItemNameID(html []byte) (string, error) {
	s := string(html)
	if m := reLoadOrderSpread.FindStringSubmatch(s); len(m) == 2 {
		return m[1], nil
	}
	if m := reItemNameID.FindStringSubmatch(s); len(m) == 2 {
		return m[1], nil
	}
	if m := reNameIDJSON.FindStringSubmatch(s); len(m) == 2 {
		return m[1], nil
	}
	return "", fmt.Errorf("steam: item_nameid not found in listing HTML")
}

func parseGraph(rows [][]any) []DepthLevel {
	out := make([]DepthLevel, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		dl := DepthLevel{}
		switch v := row[0].(type) {
		case float64:
			dl.Price = v
		case json.Number:
			f, _ := v.Float64()
			dl.Price = f
		}
		switch v := row[1].(type) {
		case float64:
			dl.Cumulative = int(v)
		case json.Number:
			n, _ := v.Int64()
			dl.Cumulative = int(n)
		}
		if len(row) >= 3 {
			if s, ok := row[2].(string); ok {
				dl.Text = s
			}
		}
		out = append(out, dl)
	}
	return out
}

func parseCompact(arr []int64) []CompactLevel {
	out := make([]CompactLevel, 0, len(arr)/2)
	for i := 0; i+1 < len(arr); i += 2 {
		out = append(out, CompactLevel{PriceMinor: arr[i], Quantity: int(arr[i+1])})
	}
	return out
}

func parseInt64String(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x == "1" || strings.EqualFold(x, "true")
	case json.Number:
		n, _ := x.Int64()
		return n != 0
	default:
		return v != nil
	}
}

// BestPrices is a legacy convenience view; its direction and unit names are
// historical interpretations, not verified market-domain evidence.
type BestPrices struct {
	AppID          int64
	MarketHashName string
	// From the legacy list/search or overview interpretation.
	ListLowestSellMajor float64 // major units if known
	// From the legacy orderbook interpretation.
	LowestSellMinor int64
	HighestBuyMinor int64
	LowestSellMajor float64
	HighestBuyMajor float64
	SellOrders      int
	BuyOrders       int
	Currency        int
	Source          string
	SpreadMinor     int64 // Legacy arithmetic difference when both values are positive.
}

// BestPricesFromBook maps an OrderBook to BestPrices.
func BestPricesFromBook(ob *OrderBook) BestPrices {
	if ob == nil {
		return BestPrices{}
	}
	bp := BestPrices{
		AppID:           ob.AppID,
		MarketHashName:  ob.MarketHashName,
		LowestSellMinor: ob.LowestSell,
		HighestBuyMinor: ob.HighestBuy,
		LowestSellMajor: MinorToMajor(ob.LowestSell),
		HighestBuyMajor: MinorToMajor(ob.HighestBuy),
		SellOrders:      ob.SellOrders,
		BuyOrders:       ob.BuyOrders,
		Currency:        ob.Currency,
		Source:          ob.Source,
	}
	if ob.LowestSell > 0 && ob.HighestBuy > 0 {
		bp.SpreadMinor = ob.LowestSell - ob.HighestBuy
	}
	return bp
}
