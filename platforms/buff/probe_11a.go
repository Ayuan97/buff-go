//go:build ignore

// One-shot BUFF Goal 11A probe. Does not implement collection.PageFetcher.
// Cookie values are never printed. Full bodies stay in gitignored scratch.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type chromeCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

type probeResult struct {
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	Status   int      `json:"status"`
	Location string   `json:"location,omitempty"`
	CT       string   `json:"content_type"`
	BodyLen  int      `json:"body_len"`
	BodyHead string   `json:"body_head"`
	TopKeys  []string `json:"top_keys,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

func main() {
	root := findRepoRoot()
	cookiePath := filepath.Join(root, "platforms", "private", "buff", "cookie.md")
	outDir := filepath.Join(root, "platforms", "private", "buff", "scratch")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		fatal(err)
	}
	header, names, err := loadCookies(cookiePath)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("cookie names=%v\n", names)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	var results []probeResult
	record := func(name, rawURL string) {
		r := doGET(client, header, name, rawURL, outDir)
		results = append(results, r)
		fmt.Printf("%s status=%d len=%d ct=%s loc=%s keys=%v notes=%v\n", r.Name, r.Status, r.BodyLen, r.CT, r.Location, r.TopKeys, r.Notes)
	}
	record("goods_p1", "https://buff.163.com/api/market/goods?game=csgo&page_num=1&page_size=20")
	record("goods_p2", "https://buff.163.com/api/market/goods?game=csgo&page_num=2&page_size=20")
	record("goods_empty", "https://buff.163.com/api/market/goods?game=csgo&search=zzzznonesuchitemzzzz&page_num=1&page_size=20")
	record("goods_last", "https://buff.163.com/api/market/goods?game=csgo&page_num=1793&page_size=20")
	record("goods_past", "https://buff.163.com/api/market/goods?game=csgo&page_num=1794&page_size=20")
	record("goods_rust", "https://buff.163.com/api/market/goods?game=rust&page_num=1&page_size=5")
	record("sell_order", "https://buff.163.com/api/market/goods/sell_order?game=csgo&goods_id=968144&page_num=1&page_size=10")
	record("buy_order", "https://buff.163.com/api/market/goods/buy_order?game=csgo&goods_id=968144&page_num=1&page_size=10")
	anon := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	r := doGET(anon, "", "goods_anon", "https://buff.163.com/api/market/goods?game=csgo&page_num=1&page_size=5", outDir)
	results = append(results, r)
	fmt.Printf("%s status=%d len=%d keys=%v notes=%v\n", r.Name, r.Status, r.BodyLen, r.TopKeys, r.Notes)

	summary, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "11a_summary.json"), summary, 0o600); err != nil {
		fatal(err)
	}
}

func doGET(client *http.Client, cookie, name, rawURL, outDir string) probeResult {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		fatal(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; buffgo-probe/1.0)")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://buff.163.com/market/csgo")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		return probeResult{Name: name, URL: rawURL, Notes: []string{err.Error()}}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = os.WriteFile(filepath.Join(outDir, name+".body"), body, 0o600)
	result := probeResult{
		Name:     name,
		URL:      rawURL,
		Status:   resp.StatusCode,
		Location: resp.Header.Get("Location"),
		CT:       resp.Header.Get("Content-Type"),
		BodyLen:  len(body),
		BodyHead: clip(string(body), 180),
	}
	if loc := strings.ToLower(result.Location); strings.Contains(loc, "login") {
		result.Notes = append(result.Notes, "login_redirect")
	}
	var decoded any
	if json.Unmarshal(body, &decoded) == nil {
		switch value := decoded.(type) {
		case map[string]any:
			for key := range value {
				result.TopKeys = append(result.TopKeys, key)
			}
			sort.Strings(result.TopKeys)
		}
	}
	return result
}

func loadCookies(path string) (string, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var cookies []chromeCookie
	if err := json.Unmarshal(raw, &cookies); err != nil {
		return "", nil, err
	}
	parts := make([]string, 0, len(cookies))
	names := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" || cookie.Value == "" {
			continue
		}
		domain := strings.ToLower(cookie.Domain)
		if domain != "" && !strings.Contains(domain, "163.com") && !strings.Contains(domain, "buff") {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
		names = append(names, cookie.Name)
	}
	if len(parts) == 0 {
		return "", names, fmt.Errorf("no buff cookies")
	}
	return strings.Join(parts, "; "), names, nil
}

func findRepoRoot() string {
	dir, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	fatal(fmt.Errorf("go.mod not found"))
	return ""
}

func clip(value string, n int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= n {
		return value
	}
	return value[:n]
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "probe: %v\n", err)
	os.Exit(1)
}
