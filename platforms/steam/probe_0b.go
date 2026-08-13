//go:build ignore

// One-shot Goal 0B probe. Does not implement collection.PageFetcher.
// Cookie values are never printed. Full bodies stay in gitignored scratch.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	Name       string            `json:"name"`
	URL        string            `json:"url"`
	Status     int               `json:"status"`
	Location   string            `json:"location,omitempty"`
	RetryAfter string            `json:"retry_after,omitempty"`
	CT         string            `json:"content_type"`
	BodyLen    int               `json:"body_len"`
	BodyHead   string            `json:"body_head"`
	JSONKind   string            `json:"json_kind,omitempty"`
	TopKeys    []string          `json:"top_keys,omitempty"`
	Notes      []string          `json:"notes,omitempty"`
	Extra      map[string]any    `json:"extra,omitempty"`
}

func main() {
	root := findRepoRoot()
	cookiePath := filepath.Join(root, "platforms", "private", "steam", "cookie.md")
	outDir := filepath.Join(root, "platforms", "private", "steam", "scratch")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		fatal(err)
	}
	jar, names, err := loadCookies(cookiePath)
	if err != nil {
		fatal(err)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	var results []probeResult
	record := func(r probeResult) {
		results = append(results, r)
		fmt.Printf("%s status=%d len=%d ct=%s loc=%s keys=%v notes=%v\n", r.Name, r.Status, r.BodyLen, r.CT, r.Location, r.TopKeys, r.Notes)
	}

	hasLogin := false
	for _, n := range names {
		if n == "steamLoginSecure" {
			hasLogin = true
		}
	}
	if !hasLogin {
		fatal(fmt.Errorf("cookie file missing steamLoginSecure (names=%v)", names))
	}

	searchURL := func(query string, start, count int, extra url.Values) string {
		q := url.Values{}
		q.Set("query", query)
		q.Set("start", fmt.Sprintf("%d", start))
		q.Set("count", fmt.Sprintf("%d", count))
		q.Set("search_descriptions", "0")
		q.Set("sort_column", "price")
		q.Set("sort_dir", "asc")
		q.Set("appid", "730")
		q.Set("norender", "1")
		for k, vs := range extra {
			for _, v := range vs {
				q.Set(k, v)
			}
		}
		return "https://steamcommunity.com/market/search/render/?" + q.Encode()
	}
	orderbookURL := func(appid int, name string) string {
		qp, _ := json.Marshal([]any{appid, name})
		q := url.Values{}
		q.Set("q", "Load")
		q.Set("qp", string(qp))
		return "https://steamcommunity.com/market/orderbook?" + q.Encode()
	}

	get := func(name, rawURL, cookieHeader string) probeResult {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			fatal(err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; buffgo-0b-probe/1.0)")
		req.Header.Set("Accept", "application/json,text/javascript,*/*;q=0.8")
		if cookieHeader != "" {
			req.Header.Set("Cookie", cookieHeader)
		} else {
			req.Header.Set("Cookie", jar)
		}
		resp, err := client.Do(req)
		if err != nil {
			return probeResult{Name: name, URL: stripQuerySecrets(rawURL), Notes: []string{err.Error()}}
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		r := probeResult{
			Name:       name,
			URL:        stripQuerySecrets(rawURL),
			Status:     resp.StatusCode,
			Location:   resp.Header.Get("Location"),
			RetryAfter: resp.Header.Get("Retry-After"),
			CT:         resp.Header.Get("Content-Type"),
			BodyLen:    len(body),
			BodyHead:   headASCII(body, 240),
			Extra:      map[string]any{},
		}
		analyzeJSON(&r, body)
		_ = os.WriteFile(filepath.Join(outDir, "0b_"+name+".body"), body, 0o600)
		return r
	}

	page1 := get("search_page0", searchURL("", 0, 10, nil), "")
	record(page1)

	time.Sleep(2 * time.Second)
	page2 := get("search_page1", searchURL("", 10, 10, nil), "")
	record(page2)

	time.Sleep(2 * time.Second)
	empty := get("search_empty", searchURL("zzzznonexistentitemxyz_buffgo_0b", 0, 10, nil), "")
	record(empty)

	namesFromPage := stringSlice(page1.Extra["hash_names"])
	if len(namesFromPage) >= 2 {
		time.Sleep(1 * time.Second)
		ob1 := get("orderbook_item1", orderbookURL(730, namesFromPage[0]), "")
		record(ob1)
		time.Sleep(1 * time.Second)
		ob2 := get("orderbook_item2", orderbookURL(730, namesFromPage[1]), "")
		record(ob2)
	} else {
		time.Sleep(1 * time.Second)
		ob := get("orderbook_redline", orderbookURL(730, "AK-47 | Redline (Field-Tested)"), "")
		record(ob)
	}

	time.Sleep(2 * time.Second)
	page1b := get("search_page0_later", searchURL("", 0, 10, nil), "")
	record(page1b)

	time.Sleep(1 * time.Second)
	bad := get("search_bad_cookie", searchURL("", 0, 1, nil), "steamLoginSecure=invalid")
	record(bad)

	time.Sleep(1 * time.Second)
	anon := get("search_anon", searchURL("", 0, 1, nil), " ")
	// empty cookie: pass a single space so we don't use jar
	_ = anon
	anon = get("search_anon2", searchURL("", 0, 1, nil), "sessionid=x")
	record(anon)

	enc, _ := json.MarshalIndent(map[string]any{
		"at":      time.Now().UTC().Format(time.RFC3339),
		"cookies": names,
		"results": results,
	}, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "0b_analysis.json"), enc, 0o600)
	fmt.Println("wrote", filepath.Join(outDir, "0b_analysis.json"))
}

func loadCookies(path string) (header string, names []string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	raw = bytes.TrimSpace(raw)
	var cookies []chromeCookie
	if err := json.Unmarshal(raw, &cookies); err != nil {
		return "", nil, fmt.Errorf("cookie.md must be chrome JSON array: %w", err)
	}
	var parts []string
	for _, c := range cookies {
		if c.Name == "" || c.Value == "" {
			continue
		}
		if !strings.Contains(c.Domain, "steamcommunity.com") && c.Domain != "" {
			continue
		}
		names = append(names, c.Name)
		parts = append(parts, c.Name+"="+c.Value)
	}
	sort.Strings(names)
	return strings.Join(parts, "; "), names, nil
}

func analyzeJSON(r *probeResult, body []byte) {
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 || (trim[0] != '{' && trim[0] != '[') {
		r.JSONKind = "nonjson"
		return
	}
	var top map[string]any
	if err := json.Unmarshal(trim, &top); err != nil {
		r.JSONKind = "json_error"
		r.Notes = append(r.Notes, err.Error())
		return
	}
	r.JSONKind = "object"
	r.TopKeys = keysOf(top)
	if success, ok := top["success"]; ok {
		r.Extra["success"] = success
	}
	if v, ok := top["total_count"]; ok {
		r.Extra["total_count"] = v
		r.Notes = append(r.Notes, fmt.Sprintf("total_count=%v", v))
	}
	if v, ok := top["start"]; ok {
		r.Extra["start"] = v
	}
	if v, ok := top["pagesize"]; ok {
		r.Extra["pagesize"] = v
	}
	if v, ok := top["results"]; ok {
		if arr, ok := v.([]any); ok {
			r.Extra["result_count"] = len(arr)
			r.Notes = append(r.Notes, fmt.Sprintf("results=%d", len(arr)))
			var hashes []string
			var sample []map[string]any
			for i, item := range arr {
				obj, _ := item.(map[string]any)
				if obj == nil {
					continue
				}
				info := map[string]any{"keys": keysOf(obj)}
				for _, k := range []string{"hash_name", "name", "sell_price", "sell_price_text", "sale_price_text", "sell_listings", "asset_description"} {
					if val, ok := obj[k]; ok {
						info[k] = summarizeValue(val)
					}
				}
				if ad, ok := obj["asset_description"].(map[string]any); ok {
					info["asset_keys"] = keysOf(ad)
					for _, k := range []string{"appid", "market_hash_name", "name", "classid", "instanceid", "market_name", "type"} {
						if val, ok := ad[k]; ok {
							info["ad_"+k] = val
						}
					}
					if mh, ok := ad["market_hash_name"].(string); ok && mh != "" {
						hashes = append(hashes, mh)
					} else if hn, ok := obj["hash_name"].(string); ok {
						hashes = append(hashes, hn)
					}
				} else if hn, ok := obj["hash_name"].(string); ok {
					hashes = append(hashes, hn)
				}
				if i < 3 {
					sample = append(sample, info)
				}
			}
			r.Extra["hash_names"] = hashes
			r.Extra["result_samples"] = sample
		}
	}
	if data, ok := top["data"].(map[string]any); ok {
		r.Extra["data_keys"] = keysOf(data)
		for _, k := range []string{"amtMaxBuyOrder", "amtMinSellOrder", "eCurrency", "cBuyOrders", "cSellOrders"} {
			if val, ok := data[k]; ok {
				r.Extra[k] = val
			}
		}
		if buys, ok := data["rgCompactBuyOrders"].([]any); ok {
			r.Extra["buy_levels"] = len(buys)
			if len(buys) > 0 {
				if m, ok := buys[0].(map[string]any); ok {
					r.Extra["buy_level0_keys"] = keysOf(m)
					r.Extra["buy_level0"] = m
				}
			}
		}
		if sells, ok := data["rgCompactSellOrders"].([]any); ok {
			r.Extra["sell_levels"] = len(sells)
			if len(sells) > 0 {
				if m, ok := sells[0].(map[string]any); ok {
					r.Extra["sell_level0_keys"] = keysOf(m)
					r.Extra["sell_level0"] = m
				}
			}
		}
	}
}

func summarizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return map[string]any{"keys": keysOf(t)}
	case []any:
		return map[string]any{"len": len(t)}
	default:
		return v
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func stringSlice(v any) []string {
	arr, ok := v.([]string)
	if ok {
		return arr
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func headASCII(body []byte, n int) string {
	if len(body) > n {
		body = body[:n]
	}
	s := string(body)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func stripQuerySecrets(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Scheme + "://" + u.Host + u.Path + "?" + u.Query().Encode()
}

func findRepoRoot() string {
	wd, _ := os.Getwd()
	dir := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return wd
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
