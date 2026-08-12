package pipeline

import (
	"context"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"buff-go/internal/buffgo/config"
	"buff-go/internal/buffgo/pool"
	"buff-go/internal/buffgo/resolve"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/steam"
	"buff-go/internal/buffgo/store"
)

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func TestJobsFromConfig(t *testing.T) {
	cfg := &config.Config{
		Games: config.GamesConfig{EnabledAppIDs: []int64{252490}},
		Jobs: map[string]config.JobConfig{
			"steam_sell_rust": {
				Platform: "steam", Side: "ask", AppID: 252490, Enabled: true,
			},
			"buff_sell_rust": {
				Platform: "buff", Side: "ask", AppID: 252490, Enabled: true,
			},
			"steam_sell_cs": {
				Platform: "steam", Side: "ask", AppID: 730, Enabled: true,
			},
		},
	}
	jobs := JobsFromConfig(cfg, "steam.ask")
	if len(jobs) != 1 || jobs[0].AppID != 252490 {
		t.Fatalf("jobs: %+v", jobs)
	}
	// empty allowlist still filters by enabled appids
	jobsAll := JobsFromConfig(cfg, "")
	if len(jobsAll) != 1 {
		t.Fatalf("expected only enabled appid steam sell, got %+v", jobsAll)
	}
}

// TestJobsFromConfig_PageDelayMaxPages ensures job page_delay / max_pages from
// config flow into JobSpec for steam.ask Fetch (daemon multi-page crawl).
func TestJobsFromConfig_PageDelayMaxPages(t *testing.T) {
	cfg := &config.Config{
		Games: config.GamesConfig{EnabledAppIDs: []int64{252490}},
		Jobs: map[string]config.JobConfig{
			"steam_sell_rust": {
				Platform:  "steam",
				Side:      "ask",
				AppID:     252490,
				Enabled:   true,
				MaxPages:  55,
				Count:     100,
				PageDelay: "2s",
			},
		},
	}
	jobs := JobsFromConfig(cfg, "steam.ask")
	if len(jobs) != 1 {
		t.Fatalf("jobs: %+v", jobs)
	}
	j := jobs[0]
	if j.MaxPages != 55 {
		t.Fatalf("MaxPages: got %d want 55", j.MaxPages)
	}
	if j.Count != 100 {
		t.Fatalf("Count: got %d want 100", j.Count)
	}
	if j.PageDelay != 2*time.Second {
		t.Fatalf("PageDelay: got %v want 2s", j.PageDelay)
	}
}

// TestJobsFromConfig_ExampleTOML_SteamSell loads shipped example and checks
// page_delay=2s reaches JobSpec (always-on steam.ask daemon baseline).
func TestJobsFromConfig_ExampleTOML_SteamSell(t *testing.T) {
	root := findRepoRoot(t)
	cfg, err := config.Load(filepath.Join(root, "configs", "buffgo.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	jobs := JobsFromConfig(cfg, "steam.ask")
	if len(jobs) != 1 {
		t.Fatalf("steam.ask jobs: %+v", jobs)
	}
	if jobs[0].PageDelay != 2*time.Second {
		t.Fatalf("example page_delay → JobSpec: got %v want 2s", jobs[0].PageDelay)
	}
	if jobs[0].AppID != 252490 {
		t.Fatalf("appid: %d", jobs[0].AppID)
	}
}

// --- B-daemon: proxy HTTP client + StaticProvider direct mode + limiter ---

func TestProxyURLFromEndpoint_FullURLWithAuth(t *testing.T) {
	u, err := proxyURLFromEndpoint("http://1.2.3.4:8080", "user:pass")
	if err != nil {
		t.Fatal(err)
	}
	if u == nil {
		t.Fatal("expected non-nil URL")
	}
	if u.Host != "1.2.3.4:8080" {
		t.Fatalf("host: %q", u.Host)
	}
	if u.User == nil || u.User.Username() != "user" {
		t.Fatalf("user: %v", u.User)
	}
	pass, _ := u.User.Password()
	if pass != "pass" {
		t.Fatalf("pass: %q", pass)
	}
}

func TestProxyURLFromEndpoint_HostPortWithAuth(t *testing.T) {
	u, err := proxyURLFromEndpoint("10.0.0.1:3128", "alice:s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "http" || u.Host != "10.0.0.1:3128" {
		t.Fatalf("url: %s", u.String())
	}
	if u.User.Username() != "alice" {
		t.Fatalf("user: %s", u.User.Username())
	}
	pass, _ := u.User.Password()
	if pass != "s3cret" {
		t.Fatalf("pass: %q", pass)
	}
}

func TestProxyURLFromEndpoint_Direct(t *testing.T) {
	for _, ep := range []string{"", "direct", "DIRECT", "  "} {
		u, err := proxyURLFromEndpoint(ep, "u:p")
		if err != nil {
			t.Fatalf("ep=%q: %v", ep, err)
		}
		if u != nil {
			t.Fatalf("ep=%q: want nil URL, got %v", ep, u)
		}
	}
}

func TestHTTPClientForProxy_DirectNoProxyTransport(t *testing.T) {
	c, err := HTTPClientForProxy("", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Timeout != 30*time.Second {
		t.Fatalf("timeout: %v", c.Timeout)
	}
	// Default client has nil Transport (uses http.DefaultTransport) — no Proxy set.
	if c.Transport != nil {
		t.Fatalf("direct client should use nil Transport, got %#v", c.Transport)
	}

	c2, err := HTTPClientForProxy(pool.ProxyIDDirect, "")
	if err != nil {
		t.Fatal(err)
	}
	if c2.Transport != nil {
		t.Fatal("direct id should not set proxy transport")
	}
}

func TestHTTPClientForProxy_WithAuthURL(t *testing.T) {
	c, err := HTTPClientForProxy("http://proxy.example:8080", "u:p")
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatalf("expected Transport.Proxy, got %#v", c.Transport)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://steamcommunity.com/", nil)
	pu, err := tr.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if pu == nil || pu.Host != "proxy.example:8080" {
		t.Fatalf("proxy url: %v", pu)
	}
	if pu.User == nil || pu.User.Username() != "u" {
		t.Fatalf("proxy user: %v", pu.User)
	}
}

func TestProxyURLFromEndpoint_ParseErrorDoesNotExposeCredentials(t *testing.T) {
	const (
		endpoint = "http://proxy-user:proxy-secret@proxy.example/%zz"
		auth     = "auth-user:auth-secret"
	)
	_, err := proxyURLFromEndpoint(endpoint, auth)
	if err == nil {
		t.Fatal("expected proxy URL parse error")
	}
	for _, secret := range []string{endpoint, auth, "proxy-secret", "auth-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("proxy parse error leaked %q: %v", secret, err)
		}
	}
	if !strings.Contains(err.Error(), "error_ref=detail_v1_") {
		t.Fatalf("proxy parse error missing safe reference: %v", err)
	}
}

func TestHTTPClientForEndpoint_Direct(t *testing.T) {
	c, err := HTTPClientForEndpoint(pool.DirectEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	if c.Transport != nil {
		t.Fatal("direct endpoint should not set proxy")
	}
}

func TestStaticProviderFromConfig_EmptyIsDirect(t *testing.T) {
	prov := staticProviderFromConfig(&config.Config{})
	if !prov.IsDirectMode() {
		t.Fatal("empty config should be direct mode")
	}
	list, err := prov.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ProxyID() != pool.ProxyIDDirect {
		t.Fatalf("list: %+v", list)
	}
	ep, err := resolveProxyForAppID(context.Background(), prov, 252490)
	if err != nil {
		t.Fatal(err)
	}
	if ep.ProxyID() != pool.ProxyIDDirect || !ep.IsDirect() {
		t.Fatalf("resolve: %+v", ep)
	}
}

func TestStaticProviderFromConfig_SelectsProxyForAppID(t *testing.T) {
	cfg := &config.Config{
		Proxies: []config.ProxyConfig{
			{Endpoint: "http://shared.example:8080", LineType: "oversea", Auth: "a:b"},
			{Endpoint: "http://rust-only.example:8080", LineType: "oversea", OnlyAppIDs: []int64{252490}},
		},
	}
	// enabled defaults true when Enabled is nil
	prov := staticProviderFromConfig(cfg)
	if prov.IsDirectMode() {
		t.Fatal("expected non-direct")
	}
	ep, err := resolveProxyForAppID(context.Background(), prov, 252490)
	if err != nil {
		t.Fatal(err)
	}
	// dedicated only_appids ranks first
	if ep.ProxyID() != "http://rust-only.example:8080" {
		t.Fatalf("want dedicated proxy, got %q", ep.ProxyID())
	}
}

func TestSearchLimiterFromConfig_MemoryWhenNoRedis(t *testing.T) {
	cfg := &config.Config{
		Steam: config.SteamConfig{
			Search: config.SteamSearchConfig{
				MaxPerWindow:     40,
				HardMaxPerWindow: 50,
				CooldownOn429:    "2m",
				Window:           "5m",
			},
		},
	}
	lim := searchLimiterFromConfig(cfg, nil)
	if lim == nil {
		t.Fatal("nil limiter")
	}
	mem, ok := lim.(*steam.MemorySearchLimiter)
	if !ok {
		t.Fatalf("want *MemorySearchLimiter, got %T", lim)
	}
	c := mem.Config()
	if c.SoftMax != 40 || c.HardMax != 50 {
		t.Fatalf("cfg: %+v", c)
	}
	// Allow once under budget
	if err := lim.AllowSearch(context.Background(), steam.ProxyDirect); err != nil {
		t.Fatal(err)
	}
}

type capturingSteamFetcher struct {
	last source.SteamSellOptions
}

func (f *capturingSteamFetcher) Pull(_ context.Context, opts source.SteamSellOptions) ([]source.RawOffer, error) {
	f.last = opts
	return []source.RawOffer{testPresentOffer(source.PlatformSteam, opts.AppID, "X", 100)}, nil
}

func TestRunJobPassesProxyIDToPull(t *testing.T) {
	src := &capturingSteamFetcher{}
	resolver := resolve.NewMemoryResolver()
	resolver.SeedProduct(252490, "X")
	quotes := store.NewMemoryQuoteStore()
	runner := NewSteamSellRunnerParts(src, resolver, quotes)
	runner.ProxyID = "p-job"

	ctx := context.Background()
	job := source.JobSpec{Key: "t", Platform: source.PlatformSteam, Side: source.SideAsk, AppID: 252490, Enabled: true}
	if _, err := runner.RunJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if src.last.ProxyID != "p-job" {
		t.Fatalf("Pull ProxyID = %q, want p-job", src.last.ProxyID)
	}
}

type partialSteamFetcher struct{}

func (partialSteamFetcher) Pull(context.Context, source.SteamSellOptions) ([]source.RawOffer, error) {
	offers := []source.RawOffer{testPresentOffer(source.PlatformSteam, 252490, "Partial Item", 100)}
	return offers, &source.PartialPullError{
		Offers: offers, Page: 1, Start: 100, Err: context.DeadlineExceeded,
	}
}

func TestSteamSellRunJob_PartialResultKeepsWritesAndCursor(t *testing.T) {
	resolver := resolve.NewMemoryResolver()
	resolver.SeedProduct(252490, "Partial Item")
	quotes := store.NewMemoryQuoteStore()
	runner := NewSteamSellRunnerParts(partialSteamFetcher{}, resolver, quotes)

	res, err := runner.RunJob(context.Background(), source.JobSpec{
		Key: "steam_sell_rust", AppID: 252490, Platform: source.PlatformSteam, Side: source.SideAsk,
	})
	if err == nil {
		t.Fatal("want partial error")
	}
	if res.Fetched != 1 || res.QuotesWritten != 1 || res.Completeness != CompletenessPartial || res.NextStart != 100 {
		t.Fatalf("partial result: %+v", res)
	}
	if quotes.Len() != 1 {
		t.Fatalf("written quotes=%d want 1", quotes.Len())
	}
}

func TestRunSteamSellFromConfig_DisabledJobsDoNotFallback(t *testing.T) {
	cfg := &config.Config{
		Games: config.GamesConfig{EnabledAppIDs: []int64{config.DefaultRustAppID}},
		Jobs: map[string]config.JobConfig{
			"steam_sell_rust": {Platform: "steam", Side: "ask", AppID: config.DefaultRustAppID, Enabled: false},
		},
	}
	results, err := RunSteamSellFromConfig(context.Background(), cfg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("disabled jobs must not run: %+v", results)
	}
}
