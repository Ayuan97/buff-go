package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the shared process configuration for buffgo roles.
type Config struct {
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Games    GamesConfig    `mapstructure:"games"`
	Pool     PoolConfig     `mapstructure:"pool"`
	// Steam holds legacy Steam crawl rate-limit compatibility knobs.
	Steam SteamConfig          `mapstructure:"steam"`
	Jobs  map[string]JobConfig `mapstructure:"jobs"`
	// Proxies is an optional static proxy list for pool acquire (SLICE C).
	// Empty or all-disabled → pool StaticProvider direct mode (proxy_id=direct).
	// TOML: [[proxies]] endpoint=... line_type=... enabled=true only_appids=[...]
	// No proxy CRUD CLI — future source is PG / HTTP control plane (ProxyProvider).
	Proxies []ProxyConfig `mapstructure:"proxies"`
}

// SteamConfig is optional production tuning for Steam market HTTP.
type SteamConfig struct {
	Search SteamSearchConfig `mapstructure:"search"`
}

// SteamSearchConfig preserves legacy search/render compatibility settings.
// Its defaults are not verified Steam policy evidence.
type SteamSearchConfig struct {
	MinInterval      string `mapstructure:"min_interval"`        // e.g. "2s" (page delay hint)
	MaxPerWindow     int    `mapstructure:"max_per_window"`      // soft budget, default 80
	HardMaxPerWindow int    `mapstructure:"hard_max_per_window"` // absolute, default 100
	CooldownOn429    string `mapstructure:"cooldown_on_429"`     // e.g. "3m"
	Window           string `mapstructure:"window"`              // counter window, e.g. "5m"
}

// ProxyConfig is one static proxy row (ARCHITECTURE §3.6 / PG proxies columns).
// Used by pool.NewStaticProviderFromInput; not a runtime lease manager.
type ProxyConfig struct {
	Endpoint      string  `mapstructure:"endpoint"`
	Auth          string  `mapstructure:"auth"`
	LineType      string  `mapstructure:"line_type"`
	Enabled       *bool   `mapstructure:"enabled"` // nil = true
	OnlyAppIDs    []int64 `mapstructure:"only_appids"`
	PreferAppIDs  []int64 `mapstructure:"prefer_appids"`
	MaxConcurrent int     `mapstructure:"max_concurrent"`
}

// IsEnabled reports whether this proxy row is active (default true when unset).
func (p ProxyConfig) IsEnabled() bool {
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

type DatabaseConfig struct {
	Driver string `mapstructure:"driver"`
	DSN    string `mapstructure:"dsn"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// GamesConfig holds enabled appids and optional per-game soft quotas.
// TOML: [games.quota.252490] max_proxy_leases = 10
type GamesConfig struct {
	EnabledAppIDs []int64              `mapstructure:"enabled_appids"`
	Quota         map[string]GameQuota `mapstructure:"quota"`
}

// GameQuota is soft scheduling/pool limits for one appid (ARCHITECTURE §4.2).
// MaxProxyLeases caps concurrent pool leases for that game (0 = unlimited).
type GameQuota struct {
	Weight         int `mapstructure:"weight"`
	MaxInflight    int `mapstructure:"max_inflight"`
	MaxProxyLeases int `mapstructure:"max_proxy_leases"`
}

type PoolConfig struct {
	LeaseTTL                string `mapstructure:"lease_ttl"`
	DefaultPlatformCooldown string `mapstructure:"default_platform_cooldown"`
	// PlatformLines: platform → allowed proxy line_types (cn/hk/oversea/dual).
	// Defaults when unset: buff→cn/dual/hk, steam→oversea/dual/hk.
	PlatformLines map[string][]string `mapstructure:"platform_lines"`
}

type JobConfig struct {
	Platform     string `mapstructure:"platform"`
	Side         string `mapstructure:"side"`
	AppID        int64  `mapstructure:"appid"`
	Enabled      bool   `mapstructure:"enabled"`
	Interval     string `mapstructure:"interval"`
	NeedsAccount bool   `mapstructure:"needs_account"`
	// Market crawl sizing (MaxPages 0 = fetch through the upstream total).
	MaxPages  int    `mapstructure:"max_pages"`
	Count     int    `mapstructure:"count"`
	PageDelay string `mapstructure:"page_delay"` // e.g. "1.5s" between market pages
}

// IntervalDuration parses the fixed delay between completed job runs.
func (j JobConfig) IntervalDuration() (time.Duration, error) {
	value := strings.TrimSpace(j.Interval)
	if value == "" {
		return 0, fmt.Errorf("interval is required")
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("interval must be a positive duration")
	}
	return d, nil
}

// PageDelayDuration returns job page delay or 0.
func (j JobConfig) PageDelayDuration() time.Duration {
	return parseDurationDefault(j.PageDelay, 0)
}

// LeaseTTLDuration parses pool.lease_ttl (default 30s).
func (c *Config) LeaseTTLDuration() time.Duration {
	return parseDurationDefault(c.Pool.LeaseTTL, 30*time.Second)
}

// PlatformCooldownDuration parses pool.default_platform_cooldown (default 2m).
func (c *Config) PlatformCooldownDuration() time.Duration {
	return parseDurationDefault(c.Pool.DefaultPlatformCooldown, 2*time.Minute)
}

// SteamSearchMaxPerWindow returns steam.search.max_per_window (0 = unset / use code default).
func (c *Config) SteamSearchMaxPerWindow() int {
	if c == nil {
		return 0
	}
	return c.Steam.Search.MaxPerWindow
}

// SteamSearchHardMaxPerWindow returns steam.search.hard_max_per_window (0 = unset).
func (c *Config) SteamSearchHardMaxPerWindow() int {
	if c == nil {
		return 0
	}
	return c.Steam.Search.HardMaxPerWindow
}

// SteamSearchCooldownOn429 parses steam.search.cooldown_on_429 (0 if unset).
func (c *Config) SteamSearchCooldownOn429() time.Duration {
	if c == nil {
		return 0
	}
	return parseDurationDefault(c.Steam.Search.CooldownOn429, 0)
}

// SteamSearchWindow parses steam.search.window (0 if unset).
func (c *Config) SteamSearchWindow() time.Duration {
	if c == nil {
		return 0
	}
	return parseDurationDefault(c.Steam.Search.Window, 0)
}

// SteamSearchMinInterval parses steam.search.min_interval (0 if unset; page_delay often used instead).
func (c *Config) SteamSearchMinInterval() time.Duration {
	if c == nil {
		return 0
	}
	return parseDurationDefault(c.Steam.Search.MinInterval, 0)
}

// SteamSearchMaxPerWindowOrDefault returns configured soft max or STEAM_RATE_LIMIT default (80).
func (c *Config) SteamSearchMaxPerWindowOrDefault() int {
	n := c.SteamSearchMaxPerWindow()
	if n <= 0 {
		return 80
	}
	return n
}

// SteamSearchCooldownOn429OrDefault returns configured cooldown or 3m.
func (c *Config) SteamSearchCooldownOn429OrDefault() time.Duration {
	d := c.SteamSearchCooldownOn429()
	if d <= 0 {
		return 3 * time.Minute
	}
	return d
}

// Default platform prefer line_types (ARCHITECTURE §4.3 / pool.platform_lines).
var (
	DefaultBuffLines  = []string{"cn", "dual", "hk"}
	DefaultSteamLines = []string{"oversea", "dual", "hk"}
)

// PlatformPreferLines returns allowed proxy line_types for platform.
// Uses pool.platform_lines when configured for that platform; otherwise
// product defaults (buff→cn/dual/hk, steam→oversea/dual/hk). Unknown
// platforms without an explicit config entry return nil.
func (c *Config) PlatformPreferLines(platform string) []string {
	p := strings.ToLower(strings.TrimSpace(platform))
	if p == "" {
		return nil
	}
	if c != nil && c.Pool.PlatformLines != nil {
		if lines, ok := c.Pool.PlatformLines[p]; ok && len(lines) > 0 {
			return normalizeLineTypes(lines)
		}
		for k, lines := range c.Pool.PlatformLines {
			if strings.ToLower(strings.TrimSpace(k)) == p && len(lines) > 0 {
				return normalizeLineTypes(lines)
			}
		}
	}
	switch p {
	case "buff":
		return append([]string(nil), DefaultBuffLines...)
	case "steam":
		return append([]string(nil), DefaultSteamLines...)
	default:
		return nil
	}
}

// EffectivePlatformLines returns the full prefer map: defaults overlaid by config.
func (c *Config) EffectivePlatformLines() map[string][]string {
	out := map[string][]string{
		"buff":  append([]string(nil), DefaultBuffLines...),
		"steam": append([]string(nil), DefaultSteamLines...),
	}
	if c == nil || c.Pool.PlatformLines == nil {
		return out
	}
	for k, lines := range c.Pool.PlatformLines {
		pk := strings.ToLower(strings.TrimSpace(k))
		norm := normalizeLineTypes(lines)
		if pk == "" || len(norm) == 0 {
			continue
		}
		out[pk] = norm
	}
	return out
}

func normalizeLineTypes(lines []string) []string {
	seen := make(map[string]struct{}, len(lines))
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		n := strings.ToLower(strings.TrimSpace(l))
		switch n {
		case "cn", "hk", "oversea", "dual":
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	return out
}

// DefaultRustAppID is the baseline game for early development (PRODUCT default).
// Not a hard single-game lock: multi-game uses games.enabled_appids + per-job appid.
const DefaultRustAppID int64 = 252490

// AppIDCS2 is the second-game model id (Counter-Strike 2). Used for multi-game
// validation (P5.1); keep disabled in production configs unless monitoring CS.
const AppIDCS2 int64 = 730

// Load reads config from path (toml/yaml/json via viper).
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, fmt.Errorf("config path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("config file: %w", err)
	}

	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// defaults
	v.SetDefault("database.driver", "postgres")
	v.SetDefault("redis.db", 0)
	v.SetDefault("games.enabled_appids", []int64{DefaultRustAppID})
	v.SetDefault("pool.lease_ttl", "30s")
	v.SetDefault("pool.default_platform_cooldown", "2m")

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks required fields for any buffgo role.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	if c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}
	if c.Redis.Addr == "" {
		return fmt.Errorf("redis.addr is required")
	}
	if len(c.Games.EnabledAppIDs) == 0 {
		return fmt.Errorf("games.enabled_appids must contain at least one appid")
	}
	for _, id := range c.Games.EnabledAppIDs {
		if id <= 0 {
			return fmt.Errorf("games.enabled_appids: invalid appid %d", id)
		}
	}
	for key, job := range c.Jobs {
		if !job.Enabled {
			continue
		}
		if job.AppID <= 0 {
			return fmt.Errorf("jobs.%s.appid must be positive", key)
		}
		platform := strings.ToLower(strings.TrimSpace(job.Platform))
		if platform != "steam" && platform != "buff" {
			return fmt.Errorf("jobs.%s.platform must be steam or buff", key)
		}
		if strings.ToLower(strings.TrimSpace(job.Side)) != "ask" {
			return fmt.Errorf("jobs.%s.side must be ask", key)
		}
		if _, err := job.IntervalDuration(); err != nil {
			return fmt.Errorf("jobs.%s: %w", key, err)
		}
		if job.MaxPages < 0 {
			return fmt.Errorf("jobs.%s.max_pages cannot be negative", key)
		}
		if job.Count < 0 {
			return fmt.Errorf("jobs.%s.count cannot be negative", key)
		}
		if value := strings.TrimSpace(job.PageDelay); value != "" {
			d, err := time.ParseDuration(value)
			if err != nil || d < 0 {
				return fmt.Errorf("jobs.%s.page_delay must be a non-negative duration", key)
			}
		}
	}
	return nil
}

// HasAppID reports whether appid is in the enabled list.
func (c *Config) HasAppID(appid int64) bool {
	for _, id := range c.Games.EnabledAppIDs {
		if id == appid {
			return true
		}
	}
	return false
}

// IsMultiGame reports whether two or more appids are enabled (ARCHITECTURE §4.1).
// Multi-game mode enables quota + dedicated/shared proxy split semantics.
func (c *Config) IsMultiGame() bool {
	return c != nil && len(c.Games.EnabledAppIDs) >= 2
}

// FirstEnabledAppID returns the first enabled appid, or 0 if none.
// Used as CLI default when --appid is omitted (not a hard single-game lock).
func (c *Config) FirstEnabledAppID() int64 {
	if c == nil || len(c.Games.EnabledAppIDs) == 0 {
		return 0
	}
	return c.Games.EnabledAppIDs[0]
}

// EnabledJobs returns enabled job entries whose appid is in games.enabled_appids.
// Optional onlyAppID > 0 further restricts to that single game (worker/processor filter).
// Appid is never assumed: jobs without a positive appid are skipped.
func (c *Config) EnabledJobs(onlyAppID int64) []JobConfigWithKey {
	if c == nil || c.Jobs == nil {
		return nil
	}
	var out []JobConfigWithKey
	for key, j := range c.Jobs {
		if !j.Enabled || j.AppID <= 0 {
			continue
		}
		if !c.HasAppID(j.AppID) {
			continue
		}
		if onlyAppID > 0 && j.AppID != onlyAppID {
			continue
		}
		out = append(out, JobConfigWithKey{Key: key, Job: j})
	}
	// Stable key order for logs / tests.
	for i := 0; i < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].Key < out[i].Key {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out
}

// JobConfigWithKey pairs a map key with its JobConfig (for enumeration).
type JobConfigWithKey struct {
	Key string
	Job JobConfig
}

// GameQuotaFor returns the configured quota for appid, or zero-value if unset.
func (c *Config) GameQuotaFor(appid int64) GameQuota {
	if c == nil || c.Games.Quota == nil {
		return GameQuota{}
	}
	key := strconv.FormatInt(appid, 10)
	if q, ok := c.Games.Quota[key]; ok {
		return q
	}
	// tolerate accidental int-like keys after alternate unmarshallers
	for k, q := range c.Games.Quota {
		if strings.TrimSpace(k) == key {
			return q
		}
	}
	return GameQuota{}
}

// MaxProxyLeases returns games.quota.<appid>.max_proxy_leases (0 = unlimited).
func (c *Config) MaxProxyLeases(appid int64) int {
	n := c.GameQuotaFor(appid).MaxProxyLeases
	if n < 0 {
		return 0
	}
	return n
}

// MaxProxyLeasesMap builds appid → max_proxy_leases for pool.Manager Options.
// Only entries with max_proxy_leases > 0 are included.
func (c *Config) MaxProxyLeasesMap() map[int64]int {
	if c == nil || len(c.Games.Quota) == 0 {
		return nil
	}
	out := make(map[int64]int, len(c.Games.Quota))
	for k, q := range c.Games.Quota {
		if q.MaxProxyLeases <= 0 {
			continue
		}
		appid, err := strconv.ParseInt(strings.TrimSpace(k), 10, 64)
		if err != nil || appid <= 0 {
			continue
		}
		out[appid] = q.MaxProxyLeases
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// StaticProxyInputs converts config proxies into pool.StaticProxyInput-shaped
// rows (endpoint, auth, line_type, only_appids, enabled, …).
// Callers build pool.NewStaticProviderFromInput(cfg.StaticProxyInputs()).
// Empty slice → StaticProvider direct mode (no Redis required for the provider).
func (c *Config) StaticProxyInputs() []StaticProxyInput {
	if c == nil || len(c.Proxies) == 0 {
		return nil
	}
	out := make([]StaticProxyInput, 0, len(c.Proxies))
	for _, p := range c.Proxies {
		out = append(out, StaticProxyInput{
			Endpoint:      strings.TrimSpace(p.Endpoint),
			Auth:          strings.TrimSpace(p.Auth),
			LineType:      strings.ToLower(strings.TrimSpace(p.LineType)),
			OnlyAppIDs:    append([]int64(nil), p.OnlyAppIDs...),
			PreferAppIDs:  append([]int64(nil), p.PreferAppIDs...),
			Enabled:       p.IsEnabled(),
			MaxConcurrent: p.MaxConcurrent,
		})
	}
	return out
}

// StaticProxyInput is the config→pool bridge shape (no import of pool package).
// Identical field semantics to pool.StaticProxyInput / ProxyEndpoint.
type StaticProxyInput struct {
	Endpoint      string
	Auth          string
	LineType      string
	OnlyAppIDs    []int64
	PreferAppIDs  []int64
	Enabled       bool
	MaxConcurrent int
}

// PoolManagerOptions returns lease TTL, cooldown, platform lines, and soft
// quotas for constructing pool.Manager from this config.
// Proxy listing is separate: use StaticProxyInputs + pool.NewStaticProviderFromInput.
func (c *Config) PoolManagerOptions() PoolManagerOptions {
	if c == nil {
		return PoolManagerOptions{
			LeaseTTL:        30 * time.Second,
			DefaultCooldown: 2 * time.Minute,
			PlatformLines:   nil,
			MaxProxyLeases:  nil,
		}
	}
	return PoolManagerOptions{
		LeaseTTL:        c.LeaseTTLDuration(),
		DefaultCooldown: c.PlatformCooldownDuration(),
		PlatformLines:   c.Pool.PlatformLines,
		MaxProxyLeases:  c.MaxProxyLeasesMap(),
	}
}

// PoolManagerOptions is config-derived input for pool.NewManager (no pool import).
type PoolManagerOptions struct {
	LeaseTTL        time.Duration
	DefaultCooldown time.Duration
	PlatformLines   map[string][]string
	MaxProxyLeases  map[int64]int
}

func parseDurationDefault(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}
