package steam

import (
	"testing"

	"buff-go/internal/market"
)

func TestParseYuanAskMatchesSellPrice(t *testing.T) {
	item := searchResult{SellPrice: 21, SellPriceText: "¥ 0.21", SalePriceText: "¥ 0.14", HashName: "Sealed Graffiti | Tilt (Desert Amber)"}
	item.AssetDescription.MarketHashName = item.HashName
	got, err := parseYuanAsk(item)
	if err != nil || got != 21 {
		t.Fatalf("got=%d err=%v", got, err)
	}
}

func TestParseYuanAskRejectsSalePriceText(t *testing.T) {
	item := searchResult{SellPrice: 14, SellPriceText: "¥ 0.14"}
	if _, err := parseYuanAsk(item); err != nil {
		t.Fatal(err)
	}
	item.SellPrice = 21
	if _, err := parseYuanAsk(item); err == nil {
		t.Fatal("mismatched sell_price must fail")
	}
}

func TestParseYuanCentsRejectsUSD(t *testing.T) {
	if _, err := parseYuanCents("$0.21"); err == nil {
		t.Fatal("expected usd rejection")
	}
}

// 上千的价格平台带千位分隔符。按价格降序采集时第一页就全是这种，
// 解析不了会让整页商品变成 price_unverified。
func TestParseYuanAskAcceptsThousandsSeparator(t *testing.T) {
	for _, test := range []struct {
		text  string
		cents int64
	}{
		{text: "¥ 9,341.19", cents: 934119},
		{text: "¥ 1,000.00", cents: 100000},
		{text: "¥ 1,234,567.89", cents: 123456789},
	} {
		item := searchResult{SellPrice: test.cents, SellPriceText: test.text}
		got, err := parseYuanAsk(item)
		if err != nil || int64(got) != test.cents {
			t.Fatalf("%q got=%d err=%v", test.text, got, err)
		}
	}
}

// 分隔符位置错乱时剥离后的数值会和平台给的整数分对不上，必须仍然被拒。
func TestParseYuanAskRejectsMalformedSeparator(t *testing.T) {
	for _, text := range []string{"¥ 9,34.19", "¥ 9,,341.19", "¥ ,341.19"} {
		item := searchResult{SellPrice: 934119, SellPriceText: text}
		if _, err := parseYuanAsk(item); err == nil {
			t.Fatalf("%q must be rejected", text)
		}
	}
}

func TestHashNameMismatch(t *testing.T) {
	item := searchResult{HashName: "A"}
	item.AssetDescription.MarketHashName = "B"
	if _, err := hashNameOf(item); err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestOrderbookBestBidAndEmpty(t *testing.T) {
	max := int64(27567)
	data := orderbookData{
		AmtMaxBuyOrder:     &max,
		ECurrency:          evidencedCNYCurrency,
		CBuyOrders:         10,
		RgCompactBuyOrders: []int64{27567, 4, 27293, 1},
	}
	cents, empty, err := orderbookBest(market.SideBid, data)
	if err != nil || empty || cents != 27567 {
		t.Fatalf("cents=%d empty=%v err=%v", cents, empty, err)
	}
	emptyBid := orderbookData{ECurrency: evidencedCNYCurrency, CBuyOrders: 0}
	_, empty, err = orderbookBest(market.SideBid, emptyBid)
	if err != nil || !empty {
		t.Fatalf("empty bid err=%v empty=%v", err, empty)
	}
}

func TestOrderbookRejectsUSDWallet(t *testing.T) {
	min := int64(4114)
	data := orderbookData{AmtMinSellOrder: &min, ECurrency: 1, CSellOrders: 1, RgCompactSellOrders: []int64{4114, 1}}
	if _, _, err := orderbookBest(market.SideAsk, data); err == nil {
		t.Fatal("usd eCurrency must be rejected")
	}
}

func TestOrderbookFakeItem(t *testing.T) {
	parsed, err := parseOrderbook([]byte(`{"success":false}`))
	if err != nil || parsed.Success || parsed.Data != nil {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
}

func TestParseOrderbookUnwrapsQueryActionEnvelope(t *testing.T) {
	// 2026-08-15 实打 AK Royale / Redline：顶层只有 data，success 在里面。
	body := []byte(`{"data":{"success":true,"data":{"amtMaxBuyOrder":5617,"amtMinSellOrder":6502,"eCurrency":23,"cBuyOrders":188,"cSellOrders":12,"rgCompactBuyOrders":[5617,2],"rgCompactSellOrders":[6502,1]}}}`)
	parsed, err := parseOrderbook(body)
	if err != nil || !parsed.Success || parsed.Data == nil || parsed.Data.ECurrency != 23 {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
	if parsed.Data.AmtMaxBuyOrder == nil || *parsed.Data.AmtMaxBuyOrder != 5617 {
		t.Fatalf("max buy=%v", parsed.Data.AmtMaxBuyOrder)
	}
	cents, empty, err := orderbookBest(market.SideBid, *parsed.Data)
	if err != nil || empty || cents != 5617 {
		t.Fatalf("cents=%d empty=%v err=%v", cents, empty, err)
	}
}

func TestParseOrderbookWrappedFakeItem(t *testing.T) {
	parsed, err := parseOrderbook([]byte(`{"data":{"success":false}}`))
	if err != nil || parsed.Success || parsed.Data != nil {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
}

func TestSearchEmptyPage(t *testing.T) {
	parsed, err := parseSearchRender([]byte(`{"success":true,"start":0,"pagesize":10,"total_count":0,"results":[]}`))
	if err != nil || parsed.TotalCount != 0 || len(parsed.Results) != 0 {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
}

func TestSearchPageRequiresTotalCount(t *testing.T) {
	for _, body := range []string{
		`{"success":true,"start":0,"pagesize":10,"results":[]}`,
		`{"success":true,"start":0,"pagesize":10,"total_count":null,"results":[]}`,
	} {
		if _, err := parseSearchRender([]byte(body)); err == nil {
			t.Fatalf("body=%s: expected error", body)
		}
	}
}

func TestParseCNYCentsPointTwoOne(t *testing.T) {
	got, err := parseYuanCents("¥ 0.21")
	if err != nil || got != 21 {
		t.Fatalf("got=%d err=%v", got, err)
	}
}
