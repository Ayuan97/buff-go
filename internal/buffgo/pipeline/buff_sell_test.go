package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"buff-go/internal/buffgo/catalog"
	"buff-go/internal/buffgo/config"
	"buff-go/internal/buffgo/pool"
	"buff-go/internal/buffgo/resolve"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/store"
)

func TestBuffJobsFromConfig(t *testing.T) {
	cfg := &config.Config{
		Games: config.GamesConfig{EnabledAppIDs: []int64{252490}},
		Jobs: map[string]config.JobConfig{
			"steam_sell_rust": {
				Platform: "steam", Side: "ask", AppID: 252490, Enabled: true,
			},
			"buff_sell_rust": {
				Platform: "buff", Side: "ask", AppID: 252490, Enabled: true, NeedsAccount: true,
			},
			"buff_sell_cs": {
				Platform: "buff", Side: "ask", AppID: 730, Enabled: true,
			},
		},
	}
	jobs := BuffJobsFromConfig(cfg, "buff.ask")
	if len(jobs) != 1 || jobs[0].AppID != 252490 || jobs[0].Key != "buff_sell_rust" {
		t.Fatalf("buff jobs: %+v", jobs)
	}
	jobsAll := BuffJobsFromConfig(cfg, "")
	if len(jobsAll) != 1 {
		t.Fatalf("expected only enabled appid buff sell, got %+v", jobsAll)
	}
	// steam allowlist excludes buff
	if n := BuffJobsFromConfig(cfg, "steam.ask"); len(n) != 0 {
		t.Fatalf("steam allowlist should exclude buff: %+v", n)
	}
}

// capturingBuffFetcher records Pull options (for MaxPages / sizing assertions).
type capturingBuffFetcher struct {
	last  source.BuffSellOptions
	out   []source.RawOffer
	calls int
}

type partialBuffFetcher struct{}

func (partialBuffFetcher) Pull(context.Context, source.BuffSellOptions) ([]source.RawOffer, error) {
	offers := []source.RawOffer{testPresentOffer(source.PlatformBuff, 252490, "Partial Item", 700)}
	return offers, &source.BuffPartialPullError{Offers: offers, NextPage: 2, Err: source.ErrHTTP429}
}

func TestBuffSellRunJob_PartialResultKeepsWritesAndCursor(t *testing.T) {
	resolver := resolve.NewMemoryResolver()
	resolver.SeedProduct(252490, "Partial Item")
	quotes := store.NewMemoryQuoteStore()
	runner := NewBuffSellRunnerParts(partialBuffFetcher{}, resolver, quotes)

	res, err := runner.RunJob(context.Background(), source.JobSpec{
		Key: "buff_sell_rust", AppID: 252490, Platform: source.PlatformBuff, Side: source.SideAsk,
	})
	if err == nil {
		t.Fatal("want partial error")
	}
	if res.Fetched != 1 || res.QuotesWritten != 1 || res.Completeness != CompletenessPartial || res.NextStart != 2 {
		t.Fatalf("partial result: %+v", res)
	}
	if quotes.Len() != 1 {
		t.Fatalf("written quotes=%d want 1", quotes.Len())
	}
}

func (c *capturingBuffFetcher) Pull(_ context.Context, opts source.BuffSellOptions) ([]source.RawOffer, error) {
	c.calls++
	c.last = opts
	if c.out != nil {
		return c.out, nil
	}
	offer := testPresentOffer(source.PlatformBuff, opts.AppID, "Fixture Item", 123)
	offer.NameRaw = "Fixture Item"
	return []source.RawOffer{offer}, nil
}

// TestBuffSellRunJob_HonorsJobMaxPages ensures a positive safety cap overrides
// the full-crawl default.
func TestBuffSellRunJob_HonorsJobMaxPages(t *testing.T) {
	src := &capturingBuffFetcher{}
	resolver := resolve.NewMemoryResolver()
	resolver.SeedProduct(252490, "Fixture Item")
	quotes := store.NewMemoryQuoteStore()
	runner := NewBuffSellRunnerParts(src, resolver, quotes)
	if runner.MaxPages != 0 {
		t.Fatalf("runner default MaxPages: got %d want 0", runner.MaxPages)
	}

	ctx := context.Background()
	_, err := runner.RunJob(ctx, source.JobSpec{
		Key:      "buff_sell_rust",
		Platform: source.PlatformBuff,
		Side:     source.SideAsk,
		AppID:    252490,
		Enabled:  true,
		MaxPages: 7,
	})
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if src.last.MaxPages != 7 {
		t.Fatalf("Pull MaxPages: got %d want 7 (job.MaxPages should win)", src.last.MaxPages)
	}

	// job.MaxPages=0 keeps the full-crawl default.
	_, err = runner.RunJob(ctx, source.JobSpec{
		Key: "buff_sell_rust", Platform: source.PlatformBuff, Side: source.SideAsk,
		AppID: 252490, Enabled: true, MaxPages: 0,
	})
	if err != nil {
		t.Fatalf("RunJob zero: %v", err)
	}
	if src.last.MaxPages != 0 {
		t.Fatalf("Pull MaxPages with job 0: got %d want full crawl", src.last.MaxPages)
	}
}

// TestBuffJobsFromConfig_MaxPages copies job max_pages into JobSpec (config → runner path).
func TestBuffJobsFromConfig_MaxPages(t *testing.T) {
	cfg := &config.Config{
		Games: config.GamesConfig{EnabledAppIDs: []int64{252490}},
		Jobs: map[string]config.JobConfig{
			"buff_sell_rust": {
				Platform: "buff", Side: "ask", AppID: 252490, Enabled: true,
				MaxPages: 12,
			},
		},
	}
	jobs := BuffJobsFromConfig(cfg, "buff.ask")
	if len(jobs) != 1 || jobs[0].MaxPages != 12 {
		t.Fatalf("buff MaxPages: %+v", jobs)
	}
}

func TestBuffSell_ResolveAndUpsertPath_RustFixture(t *testing.T) {
	root := findRepoRoot(t)

	catData, err := os.ReadFile(filepath.Join(root, "testdata", "rust_catalog_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	items, appid, err := catalog.ParseImportJSON(catData, 0)
	if err != nil {
		t.Fatal(err)
	}
	if appid != 252490 {
		t.Fatalf("catalog appid: %d", appid)
	}
	resolver := resolve.NewMemoryResolver()
	itemIDs := make(map[string]int64, len(items))
	for _, it := range items {
		id := resolver.SeedProduct(it.AppID, it.Name)
		itemIDs[it.MarketHashName] = int64(id)
	}

	payload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_buff_sell_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	src := &capturingBuffFetcher{out: []source.RawOffer{
		testPresentOffer(source.PlatformBuff, 252490, "Metal Facemask", 8850),
		testPresentOffer(source.PlatformBuff, 252490, "Road Sign Kilt", 5600),
		testPresentOffer(source.PlatformBuff, 252490, "AK47", 2475),
	}}
	quotes := store.NewMemoryQuoteStore()
	runner := NewBuffSellRunnerParts(src, resolver, quotes)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := runner.RunJob(ctx, source.JobSpec{
		Key:      "buff_sell_rust",
		Platform: source.PlatformBuff,
		Side:     source.SideAsk,
		AppID:    252490,
		Enabled:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Fetched != 3 || res.Resolved != 3 || res.QuotesWritten != 3 || res.Unresolved != 0 {
		t.Fatalf("pipeline: %+v skipped=%v", res, res.Skipped)
	}

	n, err := quotes.CountByAppIDPlatformSide(ctx, 252490, source.PlatformBuff, string(source.SideAsk))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("buff sell quotes: %d", n)
	}

	maskID, ok := itemIDs["Metal Facemask"]
	if !ok {
		t.Fatal("Metal Facemask not in catalog fixture")
	}
	q, ok := quotes.Get(maskID, source.PlatformBuff, source.SideAsk)
	if !ok || q.PriceCents != 8850 || q.AppID != 252490 {
		t.Fatalf("Metal Facemask buff sell: %+v ok=%v", q, ok)
	}

	// Lowest in fixture is AK47 @ 24.75
	list, err := quotes.ListByAppIDPlatformSide(ctx, 252490, "buff", "ask", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 || list[0].PriceCents != 2475 {
		t.Fatalf("lowest buff sell sample: %+v", list)
	}

	// Idempotent second pass
	res2, err := runner.RunJob(ctx, source.JobSpec{
		Key: "buff_sell_rust", Platform: source.PlatformBuff, Side: source.SideAsk, AppID: 252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.QuotesWritten != 3 {
		t.Fatalf("second run: %+v", res2)
	}
	if quotes.Len() != 3 {
		t.Fatalf("unique quote rows after re-run: %d", quotes.Len())
	}
}

// TestBuffAndSteamSell_SameItemFixture: P2 phase acceptance path (offline):
// same catalog item has both buff and steam sell quotes.
func TestBuffAndSteamSell_SameItemFixture(t *testing.T) {
	root := findRepoRoot(t)
	catData, err := os.ReadFile(filepath.Join(root, "testdata", "rust_catalog_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := catalog.ParseImportJSON(catData, 252490)
	if err != nil {
		t.Fatal(err)
	}
	resolver := resolve.NewMemoryResolver()
	for _, it := range items {
		resolver.SeedProduct(it.AppID, it.Name)
	}
	quotes := store.NewMemoryQuoteStore()

	steamPayload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_steam_search_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	buffPayload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_buff_sell_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	steamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(steamPayload)
	}))
	defer steamSrv.Close()
	buffSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buffPayload)
	}))
	defer buffSrv.Close()

	ctx := context.Background()
	steamRunner := NewSteamSellRunnerParts(staticSteamFetcher{offers: []source.RawOffer{
		testPresentOffer(source.PlatformSteam, 252490, "Metal Facemask", 1250),
	}}, resolver, quotes)
	buffRunner := NewBuffSellRunnerParts(&capturingBuffFetcher{out: []source.RawOffer{
		testPresentOffer(source.PlatformBuff, 252490, "Metal Facemask", 8850),
	}}, resolver, quotes)

	if _, err := steamRunner.RunJob(ctx, source.JobSpec{
		Key: "steam_sell_rust", Platform: source.PlatformSteam, Side: source.SideAsk, AppID: 252490,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := buffRunner.RunJob(ctx, source.JobSpec{
		Key: "buff_sell_rust", Platform: source.PlatformBuff, Side: source.SideAsk, AppID: 252490,
	}); err != nil {
		t.Fatal(err)
	}

	// Resolve Metal Facemask and require both platforms.
	res, err := resolver.Resolve(ctx, source.RawOffer{
		Platform: source.PlatformSteam, AppID: 252490, ExactName: "Metal Facemask",
	})
	if err != nil {
		t.Fatal(err)
	}
	sq, ok := quotes.Get(int64(res.ProductID), source.PlatformSteam, source.SideAsk)
	if !ok || sq.PriceCents != 1250 {
		t.Fatalf("steam quote: %+v ok=%v", sq, ok)
	}
	bq, ok := quotes.Get(int64(res.ProductID), source.PlatformBuff, source.SideAsk)
	if !ok || bq.PriceCents != 8850 {
		t.Fatalf("buff quote: %+v ok=%v", bq, ok)
	}
	if quotes.Len() != 2 {
		t.Fatalf("total unique quotes: %d want 2", quotes.Len())
	}
}

func TestReleaseOptsForBuffFetchErr(t *testing.T) {
	opts := releaseOptsForBuffFetchErr(nil)
	if opts.SetCooldown {
		t.Fatal("nil err should not cooldown")
	}
	opts = releaseOptsForBuffFetchErr(fmt.Errorf("network reset"))
	if opts.SetCooldown {
		t.Fatal("generic err should not cooldown")
	}
	opts = releaseOptsForBuffFetchErr(source.ErrHTTP429)
	if !opts.SetCooldown || opts.Cooldown != 0 {
		// Cooldown 0 → Manager DefaultCooldown applied at Release time
		t.Fatalf("429: %+v", opts)
	}
	wrapped := fmt.Errorf("fetch: %w", source.ErrHTTP429)
	opts = releaseOptsForBuffFetchErr(wrapped)
	if !opts.SetCooldown {
		t.Fatalf("wrapped 429: %+v", opts)
	}
	opts = releaseOptsForBuffFetchErr(fmt.Errorf("buff.ask http 429: limited"))
	if !opts.SetCooldown {
		t.Fatalf("string 429: %+v", opts)
	}
}

func TestTryAcquireCandidates_PlatformBuff(t *testing.T) {
	// Ensures shared acquire loop is used with platform=buff (line fields passed through).
	cands := []pool.ProxyCandidate{
		{ID: "oversea-a", LineType: pool.LineOversea},
		{ID: "cn-a", LineType: pool.LineCN, OnlyAppIDs: []int64{252490}},
	}
	var gotPlatform, gotLine, gotProxy string
	n := 0
	acq := func(ctx context.Context, req pool.AcquireRequest) (*pool.Lease, error) {
		n++
		gotPlatform = req.Platform
		if req.LineType == pool.LineOversea {
			return nil, pool.ErrLineMismatch
		}
		gotLine = req.LineType
		gotProxy = req.Proxy
		return &pool.Lease{ID: "L1", Proxy: req.Proxy, Platform: req.Platform, LineType: req.LineType, AppID: req.AppID, WorkerID: req.WorkerID}, nil
	}
	lease, idx, err := TryAcquireCandidates(context.Background(), acq, "w1", source.PlatformBuff, 252490, cands)
	if err != nil {
		t.Fatal(err)
	}
	if idx != 1 || lease.Proxy != "cn-a" || gotProxy != "cn-a" {
		t.Fatalf("lease=%+v idx=%d gotProxy=%s", lease, idx, gotProxy)
	}
	if gotPlatform != source.PlatformBuff || gotLine != pool.LineCN {
		t.Fatalf("platform=%q line=%q", gotPlatform, gotLine)
	}
	if n != 2 {
		t.Fatalf("calls: %d", n)
	}
}

func TestBuffSell_UnresolvedWhenNoCatalog(t *testing.T) {
	root := findRepoRoot(t)
	payload, err := os.ReadFile(filepath.Join(root, "testdata", "rust_buff_sell_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	resolver := resolve.NewMemoryResolver()
	quotes := store.NewMemoryQuoteStore()
	src := source.NewBuffSellSource(source.BuffSellOptions{
		BaseURL: srv.URL, HTTPClient: srv.Client(),
	})
	runner := NewBuffSellRunnerParts(src, resolver, quotes)

	_, err = runner.RunJob(context.Background(), source.JobSpec{
		Key: "buff_sell_rust", Platform: source.PlatformBuff, Side: source.SideAsk, AppID: 252490,
	})
	if err == nil {
		t.Fatal("expected error when 0 offers resolved")
	}
	if quotes.Len() != 0 {
		t.Fatalf("no quotes expected, got %d", quotes.Len())
	}
}

func TestRunBuffSellFromConfig_DisabledJobsDoNotFallback(t *testing.T) {
	cfg := &config.Config{
		Games: config.GamesConfig{EnabledAppIDs: []int64{config.DefaultRustAppID}},
		Jobs: map[string]config.JobConfig{
			"buff_sell_rust": {Platform: "buff", Side: "ask", AppID: config.DefaultRustAppID, Enabled: false},
		},
	}
	results, err := RunBuffSellFromConfig(context.Background(), cfg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("disabled jobs must not run: %+v", results)
	}
}
