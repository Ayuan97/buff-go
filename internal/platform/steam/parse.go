package steam

import (
	"errors"
	"fmt"
	"strings"

	"buff-go/internal/market"
)

// evidencedCNYCurrency is the logged-in wallet eCurrency that matched ¥ fen
// on 2026-08-13. It is not a guessed global Steam enum.
const evidencedCNYCurrency = 23

func hashNameOf(item SearchResult) (string, error) {
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

func parseYuanAsk(item SearchResult) (market.CNYCents, error) {
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
	// 上千的价格平台会带千位分隔符（¥ 9,341.19）。分隔符是展示格式，在这里剥掉，
	// 领域层的金额解析继续只接受纯数字。
	numeric, err := stripThousandSeparators(numeric)
	if err != nil {
		return 0, err
	}
	return market.ParseCNYCents(numeric)
}

// stripThousandSeparators 去掉千位分隔符，并要求它们正好每三位一个。
// 不能无脑替换：9,,341.19 之类剥完数值恰好正确，会让异常响应被当成正常价格。
func stripThousandSeparators(numeric string) (string, error) {
	if !strings.Contains(numeric, ",") {
		return numeric, nil
	}
	whole, fraction, hasFraction := strings.Cut(numeric, ".")
	if strings.Contains(fraction, ",") {
		return "", fmt.Errorf("price fraction must not group digits")
	}
	groups := strings.Split(whole, ",")
	for index, group := range groups {
		if index == 0 {
			if len(group) < 1 || len(group) > 3 {
				return "", fmt.Errorf("price has a misplaced thousand separator")
			}
			continue
		}
		if len(group) != 3 {
			return "", fmt.Errorf("price has a misplaced thousand separator")
		}
	}
	joined := strings.Join(groups, "")
	if !hasFraction {
		return joined, nil
	}
	return joined + "." + fraction, nil
}

func compactFirstPrice(levels []int64) (int64, bool) {
	if len(levels) < 2 || len(levels)%2 != 0 {
		return 0, false
	}
	return levels[0], true
}

var errForeignCurrency = errors.New("steam wallet is not CNY")

func isDollarPrice(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "$")
}

func orderbookBest(side market.Side, data OrderBookData) (cents market.CNYCents, empty bool, err error) {
	if data.Currency != evidencedCNYCurrency {
		return 0, false, errForeignCurrency
	}
	switch side {
	case market.SideBid:
		if data.MaxBuyOrder == nil && data.BuyOrders == 0 && len(data.CompactBuyOrders) == 0 {
			return 0, true, nil
		}
		if data.MaxBuyOrder == nil {
			return 0, false, fmt.Errorf("bid present without amtMaxBuyOrder")
		}
		first, ok := compactFirstPrice(data.CompactBuyOrders)
		if !ok || first != *data.MaxBuyOrder {
			return 0, false, fmt.Errorf("amtMaxBuyOrder does not match first compact buy")
		}
		if *data.MaxBuyOrder < 0 {
			return 0, false, fmt.Errorf("amtMaxBuyOrder is negative")
		}
		return market.CNYCents(*data.MaxBuyOrder), false, nil
	case market.SideAsk:
		if data.MinSellOrder == nil && data.SellOrders == 0 && len(data.CompactSellOrders) == 0 {
			return 0, true, nil
		}
		if data.MinSellOrder == nil {
			return 0, false, fmt.Errorf("ask present without amtMinSellOrder")
		}
		first, ok := compactFirstPrice(data.CompactSellOrders)
		if !ok || first != *data.MinSellOrder {
			return 0, false, fmt.Errorf("amtMinSellOrder does not match first compact sell")
		}
		if *data.MinSellOrder < 0 {
			return 0, false, fmt.Errorf("amtMinSellOrder is negative")
		}
		return market.CNYCents(*data.MinSellOrder), false, nil
	default:
		return 0, false, fmt.Errorf("invalid side")
	}
}
