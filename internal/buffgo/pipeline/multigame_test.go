package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"buff-go/internal/buffgo/catalog"
	"buff-go/internal/buffgo/config"
	"buff-go/internal/buffgo/resolve"
	"buff-go/internal/buffgo/source"
	"buff-go/internal/buffgo/store"
)

type multiGameCNYFetcher struct{}

func (multiGameCNYFetcher) Pull(_ context.Context, opts source.SteamSellOptions) ([]source.RawOffer, error) {
	if opts.AppID == config.AppIDCS2 {
		return []source.RawOffer{testPresentOffer(source.PlatformSteam, opts.AppID, "AK-47 | Redline (Field-Tested)", 1850)}, nil
	}
	return []source.RawOffer{testPresentOffer(source.PlatformSteam, opts.AppID, "Metal Facemask", 1250)}, nil
}

// TestP51_MultiGame_JobsQuotaCatalogQuote is the P5.1 acceptance path:
//
//	second appid (730) validates the model without long-running dual ops
//	appid threads through config → jobs → catalog → resolve → quotes
//	no single-game hardcode: Rust and CS2 isolated end-to-end offline.
func TestP51_MultiGame_JobsQuotaCatalogQuote(t *testing.T) {
	root := findRepoRoot(t)
	cfg, err := config.Load(filepath.Join(root, "configs", "buffgo.multi.example.toml"))
	if err != nil {
		t.Fatalf("load multi config: %v", err)
	}
	if !cfg.IsMultiGame() {
		t.Fatal("multi config must enable ≥2 appids")
	}

	// --- config / jobs ---
	steamJobs := JobsFromConfig(cfg, "steam.ask")
	if len(steamJobs) != 2 {
		t.Fatalf("steam jobs (both appids): %+v", steamJobs)
	}
	seen := map[int64]bool{}
	for _, j := range steamJobs {
		if j.AppID <= 0 {
			t.Fatalf("job missing appid: %+v", j)
		}
		seen[j.AppID] = true
	}
	if !seen[config.DefaultRustAppID] || !seen[config.AppIDCS2] {
		t.Fatalf("steam jobs appids: %v", seen)
	}
	// only_appid filter (worker --appid 730)
	csOnly := JobsFromConfigAppID(cfg, "steam.ask", config.AppIDCS2)
	if len(csOnly) != 1 || csOnly[0].AppID != config.AppIDCS2 {
		t.Fatalf("cs-only jobs: %+v", csOnly)
	}
	buffJobs := BuffJobsFromConfig(cfg, "buff.ask")
	if len(buffJobs) != 2 {
		t.Fatalf("buff jobs both appids: %+v", buffJobs)
	}
	// Quota map is per-appid (pool soft caps).
	if cfg.MaxProxyLeases(config.DefaultRustAppID) != 10 || cfg.MaxProxyLeases(config.AppIDCS2) != 5 {
		t.Fatalf("quota: rust=%d cs=%d", cfg.MaxProxyLeases(config.DefaultRustAppID), cfg.MaxProxyLeases(config.AppIDCS2))
	}

	// --- catalog: both appids ---
	rustCat, err := os.ReadFile(filepath.Join(root, "testdata", "rust_catalog_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	csCat, err := os.ReadFile(filepath.Join(root, "testdata", "cs2_catalog_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	rustItems, rustApp, err := catalog.ParseImportJSON(rustCat, 0)
	if err != nil {
		t.Fatal(err)
	}
	csItems, csApp, err := catalog.ParseImportJSON(csCat, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rustApp != config.DefaultRustAppID || csApp != config.AppIDCS2 {
		t.Fatalf("catalog appids: rust=%d cs=%d", rustApp, csApp)
	}
	// KnownGame covers both (not hardcoded to Rust-only).
	if g := catalog.KnownGame(csApp); g.Code != "cs2" {
		t.Fatalf("KnownGame(730): %+v", g)
	}

	resolver := resolve.NewMemoryResolver()
	rustIDs := map[string]int64{}
	csIDs := map[string]int64{}
	for _, it := range rustItems {
		rustIDs[it.MarketHashName] = int64(resolver.SeedProduct(it.AppID, it.Name))
	}
	for _, it := range csItems {
		csIDs[it.MarketHashName] = int64(resolver.SeedProduct(it.AppID, it.Name))
	}

	quotes := store.NewMemoryQuoteStore()
	runner := NewSteamSellRunnerParts(multiGameCNYFetcher{}, resolver, quotes)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Run both enabled steam jobs (multi-game worker model).
	for _, job := range steamJobs {
		res, err := runner.RunJob(ctx, job)
		if err != nil {
			t.Fatalf("job %s appid=%d: %v", job.Key, job.AppID, err)
		}
		if res.Fetched == 0 || res.QuotesWritten == 0 {
			t.Fatalf("empty pipeline for %+v: %+v", job, res)
		}
		if res.AppID != job.AppID {
			t.Fatalf("result appid leak: job=%d res=%d", job.AppID, res.AppID)
		}
	}

	// Quotes isolated by appid.
	nRust, err := quotes.CountByAppIDPlatformSide(ctx, config.DefaultRustAppID, source.PlatformSteam, string(source.SideAsk))
	if err != nil {
		t.Fatal(err)
	}
	nCS, err := quotes.CountByAppIDPlatformSide(ctx, config.AppIDCS2, source.PlatformSteam, string(source.SideAsk))
	if err != nil {
		t.Fatal(err)
	}
	if nRust != 1 || nCS != 1 {
		t.Fatalf("quote counts rust=%d cs=%d want 1/1", nRust, nCS)
	}

	// Spot-check identity keys stay within game.
	maskID := rustIDs["Metal Facemask"]
	q, ok := quotes.Get(maskID, source.PlatformSteam, source.SideAsk)
	if !ok || q.AppID != config.DefaultRustAppID || q.PriceCents != 1250 {
		t.Fatalf("rust Metal Facemask: %+v ok=%v", q, ok)
	}
	akID := csIDs["AK-47 | Redline (Field-Tested)"]
	q2, ok := quotes.Get(akID, source.PlatformSteam, source.SideAsk)
	if !ok || q2.AppID != config.AppIDCS2 || q2.PriceCents != 1850 {
		t.Fatalf("cs2 AK Redline: %+v ok=%v", q2, ok)
	}

}

// TestP51_DisabledAppIDJobsSkipped: jobs for non-enabled appids never run.
func TestP51_DisabledAppIDJobsSkipped(t *testing.T) {
	cfg := &config.Config{
		Games: config.GamesConfig{EnabledAppIDs: []int64{config.DefaultRustAppID}},
		Jobs: map[string]config.JobConfig{
			"steam_sell_rust": {Platform: "steam", Side: "ask", AppID: config.DefaultRustAppID, Enabled: true},
			"steam_sell_cs2":  {Platform: "steam", Side: "ask", AppID: config.AppIDCS2, Enabled: true},
		},
	}
	jobs := JobsFromConfig(cfg, "")
	if len(jobs) != 1 || jobs[0].AppID != config.DefaultRustAppID {
		t.Fatalf("disabled game must not schedule: %+v", jobs)
	}
	// Enabling second appid picks up its jobs without code changes.
	cfg.Games.EnabledAppIDs = []int64{config.DefaultRustAppID, config.AppIDCS2}
	jobs = JobsFromConfig(cfg, "steam.ask")
	if len(jobs) != 2 {
		t.Fatalf("multi enable: %+v", jobs)
	}
}
