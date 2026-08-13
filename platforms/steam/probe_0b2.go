//go:build ignore

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
	"strings"
	"time"
)

type chromeCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

func main() {
	root := findRepoRoot()
	raw, err := os.ReadFile(filepath.Join(root, "platforms", "private", "steam", "cookie.md"))
	if err != nil {
		fatal(err)
	}
	var cookies []chromeCookie
	if err := json.Unmarshal(bytes.TrimSpace(raw), &cookies); err != nil {
		fatal(err)
	}
	var jar, noLogin []string
	for _, c := range cookies {
		if c.Name == "" || c.Value == "" {
			continue
		}
		if !strings.Contains(c.Domain, "steamcommunity.com") && c.Domain != "" {
			continue
		}
		part := c.Name + "=" + c.Value
		jar = append(jar, part)
		if c.Name != "steamLoginSecure" {
			noLogin = append(noLogin, part)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	get := func(name, rawURL, cookie string) {
		req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; buffgo-0b-probe/1.0)")
		req.Header.Set("Accept", "application/json,text/javascript,*/*;q=0.8")
		req.Header.Set("Cookie", cookie)
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("%s err=%v\n", name, err)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		head := string(body)
		if len(head) > 280 {
			head = head[:280]
		}
		head = strings.ReplaceAll(head, "\n", " ")
		fmt.Printf("%s status=%d loc=%q ra=%q len=%d head=%s\n", name, resp.StatusCode, resp.Header.Get("Location"), resp.Header.Get("Retry-After"), len(body), head)
		_ = os.WriteFile(filepath.Join(root, "platforms", "private", "steam", "scratch", "0b2_"+name+".body"), body, 0o600)
	}

	ob := func(name string) string {
		qp, _ := json.Marshal([]any{730, name})
		q := url.Values{}
		q.Set("q", "Load")
		q.Set("qp", string(qp))
		return "https://steamcommunity.com/market/orderbook?" + q.Encode()
	}
	search := func(start, count int) string {
		q := url.Values{}
		q.Set("query", "")
		q.Set("start", fmt.Sprintf("%d", start))
		q.Set("count", fmt.Sprintf("%d", count))
		q.Set("search_descriptions", "0")
		q.Set("sort_column", "price")
		q.Set("sort_dir", "asc")
		q.Set("appid", "730")
		q.Set("norender", "1")
		return "https://steamcommunity.com/market/search/render/?" + q.Encode()
	}

	login := strings.Join(jar, "; ")
	get("orderbook_redline", ob("AK-47 | Redline (Field-Tested)"), login)
	time.Sleep(time.Second)
	get("orderbook_fake", ob("Not A Real Item XYZ"), login)
	time.Sleep(time.Second)
	get("search_last", search(35230, 10), login)
	time.Sleep(time.Second)
	get("search_after_end", search(35234, 10), login)
	time.Sleep(time.Second)
	get("mylistings_ok", "https://steamcommunity.com/market/mylistings", login)
	time.Sleep(time.Second)
	get("mylistings_bad", "https://steamcommunity.com/market/mylistings", "steamLoginSecure=invalid")
	time.Sleep(time.Second)
	get("orderbook_bad", ob("AK-47 | Redline (Field-Tested)"), "steamLoginSecure=invalid")
	time.Sleep(time.Second)
	get("orderbook_nologin", ob("AK-47 | Redline (Field-Tested)"), strings.Join(noLogin, "; "))
	time.Sleep(time.Second)
	get("search_nologin", search(0, 1), strings.Join(noLogin, "; "))
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
