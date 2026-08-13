package steam_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 校验本 Goal 落盘的 Steam 接口资料完整性（不访问外网、不读 Cookie）。
func TestSteamMarketDocsInventory(t *testing.T) {
	dir := findSteamDocsDir(t)
	required := []string{
		"README.md",
		"search-render.md",
		"orderbook.md",
		"appfilters.md",
		"appfacets.md",
		"priceoverview.md",
		"pricehistory.md",
		"rate-limit.md",
	}
	for _, name := range required {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
		if len(data) < 80 {
			t.Fatalf("%s too short", name)
		}
		text := string(data)
		if strings.Contains(text, "steamLoginSecure=") || strings.Contains(text, "sessionid=") {
			t.Fatalf("%s must not embed session cookies", name)
		}
	}

	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	r := string(readme)
	for _, needle := range []string{
		"market/search/render",
		"market/orderbook",
		"market/appfilters",
		"market/appfacets",
		"market/priceoverview",
		"market/pricehistory",
		"itemordershistogram",
		"已实请求",
		"不共享",
		"Retry-After",
	} {
		if !strings.Contains(r, needle) {
			t.Fatalf("README.md missing required marker %q", needle)
		}
	}

	orderbook, err := os.ReadFile(filepath.Join(dir, "orderbook.md"))
	if err != nil {
		t.Fatal(err)
	}
	o := string(orderbook)
	for _, needle := range []string{"q=Load", "80", "200", "匿名", "amtMaxBuyOrder", "eCurrency"} {
		if !strings.Contains(o, needle) {
			t.Fatalf("orderbook.md missing %q", needle)
		}
	}

	search, err := os.ReadFile(filepath.Join(dir, "search-render.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(search)
	for _, needle := range []string{"429", "匿名", "登录", "2×200", "market_hash_name", "sell_price_text"} {
		if !strings.Contains(s, needle) {
			t.Fatalf("search-render.md missing %q", needle)
		}
	}
	// 禁止把无法核对的历史加压包络写进正式结论（成功计数表）
	if strings.Contains(s, "全 200") && (strings.Contains(s, "gap_") || strings.Contains(s, "parallel")) {
		t.Fatal("search-render.md must not claim unverifiable ladder success table")
	}

	rate, err := os.ReadFile(filepath.Join(dir, "rate-limit.md"))
	if err != nil {
		t.Fatal(err)
	}
	rt := string(rate)
	if !strings.Contains(rt, "orderbook") || !strings.Contains(rt, "search/render") {
		t.Fatal("rate-limit.md must cover orderbook and search/render")
	}
	if !strings.Contains(rt, "2×200") {
		t.Fatal("rate-limit.md must state verifiable search/render 2x200")
	}

	if !strings.Contains(r, "getbuyorderstatus") {
		t.Fatal("README.md must register market/getbuyorderstatus")
	}
	if !strings.Contains(r, "Goal 0B") {
		t.Fatal("README.md must record Goal 0B field acceptance")
	}
}

func findSteamDocsDir(t *testing.T) string {
	t.Helper()
	// test file lives in platforms/steam/
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{
		wd,
		filepath.Join(wd, "platforms", "steam"),
	}
	// walk up looking for platforms/steam/README.md
	dir := wd
	for i := 0; i < 6; i++ {
		candidates = append(candidates, filepath.Join(dir, "platforms", "steam"))
		dir = filepath.Dir(dir)
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "README.md")); err == nil {
			if _, err := os.Stat(filepath.Join(c, "orderbook.md")); err == nil {
				return c
			}
		}
	}
	t.Fatal("platforms/steam docs dir not found")
	return ""
}
