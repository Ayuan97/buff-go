package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_SampleConfig(t *testing.T) {
	// Drive real Load() against the shipped example config (or fixture copy).
	root := findRepoRoot(t)
	path := filepath.Join(root, "configs", "buffgo.example.toml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	if cfg.Database.Driver != "postgres" {
		t.Fatalf("driver: got %q", cfg.Database.Driver)
	}
	if cfg.Database.DSN == "" {
		t.Fatal("database.dsn empty")
	}
	if cfg.Redis.Addr == "" {
		t.Fatal("redis.addr empty")
	}
	if !cfg.HasAppID(DefaultRustAppID) {
		t.Fatalf("enabled_appids missing Rust %d: %v", DefaultRustAppID, cfg.Games.EnabledAppIDs)
	}
	if d := cfg.LeaseTTLDuration(); d != 30*time.Second {
		t.Fatalf("lease_ttl: got %v", d)
	}
	// The example preserves its legacy compatibility cooldown value.
	if d := cfg.PlatformCooldownDuration(); d != 3*time.Minute {
		t.Fatalf("default_platform_cooldown: got %v want 3m", d)
	}
	// The example preserves its legacy page delay; it is not platform evidence.
	steamJob, ok := cfg.Jobs["steam_sell_rust"]
	if !ok {
		t.Fatal("expected jobs.steam_sell_rust in example.toml")
	}
	if d := steamJob.PageDelayDuration(); d != 2*time.Second {
		t.Fatalf("steam_sell_rust page_delay: got %v want 2s", d)
	}
	// The legacy steam.search compatibility block remains parseable.
	if cfg.SteamSearchMaxPerWindow() != 80 {
		t.Fatalf("steam.search.max_per_window: got %d want 80", cfg.SteamSearchMaxPerWindow())
	}
	if cfg.SteamSearchHardMaxPerWindow() != 100 {
		t.Fatalf("steam.search.hard_max: got %d", cfg.SteamSearchHardMaxPerWindow())
	}
	if d := cfg.SteamSearchCooldownOn429(); d != 3*time.Minute {
		t.Fatalf("steam.search.cooldown_on_429: got %v want 3m", d)
	}
	if d := cfg.SteamSearchWindow(); d != 5*time.Minute {
		t.Fatalf("steam.search.window: got %v want 5m", d)
	}
	if d := cfg.SteamSearchMinInterval(); d != 2*time.Second {
		t.Fatalf("steam.search.min_interval: got %v want 2s", d)
	}
	if steamJob.MaxPages != 0 {
		// 0 follows upstream total_count to the last page.
		t.Fatalf("steam_sell_rust max_pages default: got %d want 0 (full crawl)", steamJob.MaxPages)
	}
	// pool.platform_lines from example.toml
	buffLines := cfg.PlatformPreferLines("buff")
	if !equalStrings(buffLines, DefaultBuffLines) {
		t.Fatalf("buff prefer: got %v want %v", buffLines, DefaultBuffLines)
	}
	steamLines := cfg.PlatformPreferLines("steam")
	if !equalStrings(steamLines, DefaultSteamLines) {
		t.Fatalf("steam prefer: got %v want %v", steamLines, DefaultSteamLines)
	}
	if len(cfg.Pool.PlatformLines) == 0 {
		t.Fatal("expected platform_lines loaded from example.toml")
	}
	// games.quota.252490 from example.toml
	q := cfg.GameQuotaFor(DefaultRustAppID)
	if q.MaxProxyLeases != 10 {
		t.Fatalf("max_proxy_leases rust: got %d want 10", q.MaxProxyLeases)
	}
	if q.Weight != 70 || q.MaxInflight != 20 {
		t.Fatalf("quota fields: %+v", q)
	}
	if cfg.MaxProxyLeases(DefaultRustAppID) != 10 {
		t.Fatalf("MaxProxyLeases helper: %d", cfg.MaxProxyLeases(DefaultRustAppID))
	}
	m := cfg.MaxProxyLeasesMap()
	if m[DefaultRustAppID] != 10 {
		t.Fatalf("MaxProxyLeasesMap: %v", m)
	}
	if cfg.MaxProxyLeases(730) != 0 {
		t.Fatalf("unset appid should be unlimited (0), got %d", cfg.MaxProxyLeases(730))
	}
	// example.toml has no [[proxies]] → empty static list (pool direct mode).
	if len(cfg.Proxies) != 0 {
		t.Fatalf("example proxies should be empty (direct mode), got %d", len(cfg.Proxies))
	}
	if cfg.StaticProxyInputs() != nil {
		t.Fatalf("StaticProxyInputs empty: %v", cfg.StaticProxyInputs())
	}
	mo := cfg.PoolManagerOptions()
	if mo.LeaseTTL != 30*time.Second || mo.DefaultCooldown != 3*time.Minute {
		t.Fatalf("PoolManagerOptions: %+v", mo)
	}
	if mo.MaxProxyLeases[DefaultRustAppID] != 10 {
		t.Fatalf("PoolManagerOptions quotas: %v", mo.MaxProxyLeases)
	}
}

func TestStaticProxyInputs_EnabledDefaultAndFields(t *testing.T) {
	enFalse := false
	cfg := &Config{
		Proxies: []ProxyConfig{
			{
				Endpoint:   " http://a:1 ",
				Auth:       " u:p ",
				LineType:   "Oversea",
				OnlyAppIDs: []int64{252490},
				// Enabled nil → true
			},
			{
				Endpoint: "http://b:2",
				LineType: "cn",
				Enabled:  &enFalse,
			},
		},
	}
	in := cfg.StaticProxyInputs()
	if len(in) != 2 {
		t.Fatalf("len=%d", len(in))
	}
	if in[0].Endpoint != "http://a:1" || in[0].Auth != "u:p" || in[0].LineType != "oversea" {
		t.Fatalf("row0: %+v", in[0])
	}
	if !in[0].Enabled || len(in[0].OnlyAppIDs) != 1 || in[0].OnlyAppIDs[0] != 252490 {
		t.Fatalf("row0 enabled/only: %+v", in[0])
	}
	if in[1].Enabled {
		t.Fatal("row1 should be disabled")
	}
}

func TestLoad_ProxiesTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.toml")
	body := `
[database]
driver = "postgres"
dsn = "postgres://x"

[redis]
addr = "127.0.0.1:6379"

[games]
enabled_appids = [252490]

[[proxies]]
endpoint = "http://1.2.3.4:8080"
auth = "user:pass"
line_type = "oversea"
enabled = true
only_appids = [252490]
max_concurrent = 2

[[proxies]]
endpoint = "http://5.6.7.8:8080"
line_type = "cn"
enabled = false
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Proxies) != 2 {
		t.Fatalf("proxies len=%d", len(cfg.Proxies))
	}
	if cfg.Proxies[0].Endpoint != "http://1.2.3.4:8080" {
		t.Fatalf("endpoint: %q", cfg.Proxies[0].Endpoint)
	}
	if !cfg.Proxies[0].IsEnabled() {
		t.Fatal("first should be enabled")
	}
	if cfg.Proxies[0].MaxConcurrent != 2 {
		t.Fatalf("max_concurrent: %d", cfg.Proxies[0].MaxConcurrent)
	}
	if len(cfg.Proxies[0].OnlyAppIDs) != 1 || cfg.Proxies[0].OnlyAppIDs[0] != 252490 {
		t.Fatalf("only_appids: %v", cfg.Proxies[0].OnlyAppIDs)
	}
	if cfg.Proxies[1].IsEnabled() {
		t.Fatal("second should be disabled")
	}
	in := cfg.StaticProxyInputs()
	if len(in) != 2 || !in[0].Enabled || in[1].Enabled {
		t.Fatalf("StaticProxyInputs: %+v", in)
	}
}

func TestPlatformPreferLines_DefaultsAndOverride(t *testing.T) {
	// no platform_lines → product defaults
	cfg := &Config{
		Database: DatabaseConfig{DSN: "postgres://x"},
		Redis:    RedisConfig{Addr: "127.0.0.1:6379"},
		Games:    GamesConfig{EnabledAppIDs: []int64{DefaultRustAppID}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !equalStrings(cfg.PlatformPreferLines("buff"), DefaultBuffLines) {
		t.Fatalf("buff default: %v", cfg.PlatformPreferLines("buff"))
	}
	if !equalStrings(cfg.PlatformPreferLines("steam"), DefaultSteamLines) {
		t.Fatalf("steam default: %v", cfg.PlatformPreferLines("steam"))
	}
	if cfg.PlatformPreferLines("other") != nil {
		t.Fatalf("unknown platform: %v", cfg.PlatformPreferLines("other"))
	}

	cfg.Pool.PlatformLines = map[string][]string{
		"buff":  {"cn"},
		"other": {"dual", "hk"},
	}
	if !equalStrings(cfg.PlatformPreferLines("buff"), []string{"cn"}) {
		t.Fatalf("buff override: %v", cfg.PlatformPreferLines("buff"))
	}
	// steam still default when not overridden in PreferLinesFor sense
	if !equalStrings(cfg.PlatformPreferLines("steam"), DefaultSteamLines) {
		t.Fatalf("steam still default: %v", cfg.PlatformPreferLines("steam"))
	}
	if !equalStrings(cfg.PlatformPreferLines("other"), []string{"dual", "hk"}) {
		t.Fatalf("other: %v", cfg.PlatformPreferLines("other"))
	}

	eff := cfg.EffectivePlatformLines()
	if !equalStrings(eff["buff"], []string{"cn"}) {
		t.Fatalf("effective buff: %v", eff["buff"])
	}
	if !equalStrings(eff["steam"], DefaultSteamLines) {
		t.Fatalf("effective steam: %v", eff["steam"])
	}
	if !equalStrings(eff["other"], []string{"dual", "hk"}) {
		t.Fatalf("effective other: %v", eff["other"])
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidate_RequiresFields(t *testing.T) {
	cfg := &Config{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validate error")
	}
	cfg.Database.DSN = "postgres://x"
	cfg.Redis.Addr = "127.0.0.1:6379"
	cfg.Games.EnabledAppIDs = []int64{DefaultRustAppID}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("go.mod not found from test cwd")
	return ""
}
