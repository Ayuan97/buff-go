package config

import (
	"path/filepath"
	"testing"
)

// TestP51_LoadMultiGameExample proves the second-appid model config loads:
// enabled_appids, per-game quotas, and jobs each carry their own appid.
func TestP51_LoadMultiGameExample(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "configs", "buffgo.multi.example.toml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load multi example: %v", err)
	}

	if !cfg.IsMultiGame() {
		t.Fatalf("expected multi-game, enabled=%v", cfg.Games.EnabledAppIDs)
	}
	if !cfg.HasAppID(DefaultRustAppID) || !cfg.HasAppID(AppIDCS2) {
		t.Fatalf("enabled_appids must include Rust+CS2: %v", cfg.Games.EnabledAppIDs)
	}
	if cfg.FirstEnabledAppID() != DefaultRustAppID {
		t.Fatalf("FirstEnabledAppID: %d", cfg.FirstEnabledAppID())
	}

	// Quotas are per-appid (soft caps), not a single global.
	if cfg.MaxProxyLeases(DefaultRustAppID) != 10 {
		t.Fatalf("rust max_proxy_leases: %d", cfg.MaxProxyLeases(DefaultRustAppID))
	}
	if cfg.MaxProxyLeases(AppIDCS2) != 5 {
		t.Fatalf("cs2 max_proxy_leases: %d", cfg.MaxProxyLeases(AppIDCS2))
	}
	m := cfg.MaxProxyLeasesMap()
	if m[DefaultRustAppID] != 10 || m[AppIDCS2] != 5 {
		t.Fatalf("MaxProxyLeasesMap: %v", m)
	}

	// Jobs: both games present; appid never implicit.
	jobs := cfg.EnabledJobs(0)
	if len(jobs) != 4 {
		t.Fatalf("expected 4 enabled jobs (2 platforms × 2 appids), got %d: %+v", len(jobs), jobKeys(jobs))
	}
	var rust, cs2 int
	for _, j := range jobs {
		if j.Job.AppID <= 0 {
			t.Fatalf("job %s missing appid", j.Key)
		}
		switch j.Job.AppID {
		case DefaultRustAppID:
			rust++
		case AppIDCS2:
			cs2++
		default:
			t.Fatalf("unexpected appid on job %s: %d", j.Key, j.Job.AppID)
		}
	}
	if rust != 2 || cs2 != 2 {
		t.Fatalf("job split rust=%d cs2=%d want 2/2", rust, cs2)
	}

	// Filter one game (worker --appid 730 model).
	onlyCS := cfg.EnabledJobs(AppIDCS2)
	if len(onlyCS) != 2 {
		t.Fatalf("only CS2 jobs: %+v", jobKeys(onlyCS))
	}
	for _, j := range onlyCS {
		if j.Job.AppID != AppIDCS2 {
			t.Fatalf("filter leak: %+v", j)
		}
	}
}

// TestP51_SingleGameExampleStillDefault keeps production default Rust-only.
func TestP51_SingleGameExampleStillDefault(t *testing.T) {
	root := findRepoRoot(t)
	cfg, err := Load(filepath.Join(root, "configs", "buffgo.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IsMultiGame() {
		t.Fatalf("example.toml must stay single-game by default: %v", cfg.Games.EnabledAppIDs)
	}
	if !cfg.HasAppID(DefaultRustAppID) {
		t.Fatalf("missing Rust: %v", cfg.Games.EnabledAppIDs)
	}
	if cfg.HasAppID(AppIDCS2) {
		t.Fatalf("CS2 must not be enabled in default example: %v", cfg.Games.EnabledAppIDs)
	}
	// Jobs for disabled appids (commented in example) are simply absent / ignored.
	for _, j := range cfg.EnabledJobs(0) {
		if j.Job.AppID != DefaultRustAppID {
			t.Fatalf("default example job appid: %s → %d", j.Key, j.Job.AppID)
		}
	}
}

func TestIsMultiGameAndEnabledJobs(t *testing.T) {
	cfg := &Config{
		Database: DatabaseConfig{DSN: "postgres://x"},
		Redis:    RedisConfig{Addr: "127.0.0.1:6379"},
		Games: GamesConfig{
			EnabledAppIDs: []int64{DefaultRustAppID, AppIDCS2},
			Quota: map[string]GameQuota{
				"252490": {MaxProxyLeases: 10},
				"730":    {MaxProxyLeases: 5},
			},
		},
		Jobs: map[string]JobConfig{
			"steam_sell_rust": {Platform: "steam", Side: "ask", AppID: DefaultRustAppID, Enabled: true, Interval: "1m"},
			"steam_sell_cs2":  {Platform: "steam", Side: "ask", AppID: AppIDCS2, Enabled: true, Interval: "1m"},
			"disabled_cs2":    {Platform: "steam", Side: "ask", AppID: AppIDCS2, Enabled: false},
			"other_game":      {Platform: "steam", Side: "ask", AppID: 570, Enabled: true, Interval: "1m"}, // not enabled
			"no_appid":        {Platform: "steam", Side: "ask", AppID: 0, Enabled: false},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.IsMultiGame() {
		t.Fatal("expected multi")
	}
	jobs := cfg.EnabledJobs(0)
	if len(jobs) != 2 {
		t.Fatalf("jobs: %+v", jobKeys(jobs))
	}
	if n := cfg.EnabledJobs(570); len(n) != 0 {
		t.Fatalf("disabled appid filter: %+v", n)
	}
}

func TestValidate_RejectsInvalidEnabledJobInterval(t *testing.T) {
	cfg := &Config{
		Database: DatabaseConfig{DSN: "postgres://x"},
		Redis:    RedisConfig{Addr: "127.0.0.1:6379"},
		Games:    GamesConfig{EnabledAppIDs: []int64{DefaultRustAppID}},
		Jobs: map[string]JobConfig{
			"steam_sell_rust": {Platform: "steam", Side: "ask", AppID: DefaultRustAppID, Enabled: true},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing interval error")
	}
	cfg.Jobs["steam_sell_rust"] = JobConfig{
		Platform: "steam", Side: "ask", AppID: DefaultRustAppID, Enabled: true, Interval: "bad",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid interval error")
	}
}

func TestValidate_RejectsMalformedEnabledJob(t *testing.T) {
	cfg := &Config{
		Database: DatabaseConfig{DSN: "postgres://x"},
		Redis:    RedisConfig{Addr: "127.0.0.1:6379"},
		Games:    GamesConfig{EnabledAppIDs: []int64{DefaultRustAppID}},
		Jobs: map[string]JobConfig{
			"bad": {Platform: "steam", Side: "ask", Enabled: true, Interval: "1m"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected enabled job appid error")
	}
	cfg.Jobs["bad"] = JobConfig{Platform: "unknown", Side: "ask", AppID: DefaultRustAppID, Enabled: true, Interval: "1m"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected enabled job platform error")
	}
	cfg.Jobs["bad"] = JobConfig{Platform: "steam", Side: "ask", AppID: DefaultRustAppID, Enabled: true, Interval: "1m", PageDelay: "bad"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected page_delay error")
	}
}

func TestValidate_RejectsZeroAppID(t *testing.T) {
	cfg := &Config{
		Database: DatabaseConfig{DSN: "postgres://x"},
		Redis:    RedisConfig{Addr: "127.0.0.1:6379"},
		Games:    GamesConfig{EnabledAppIDs: []int64{0}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid appid 0")
	}
}

func jobKeys(jobs []JobConfigWithKey) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.Key
	}
	return out
}
