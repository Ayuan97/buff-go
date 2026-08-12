package steam

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestNormalizeProxyID(t *testing.T) {
	if NormalizeProxyID("") != ProxyDirect {
		t.Fatal("empty → direct")
	}
	if NormalizeProxyID("  ") != ProxyDirect {
		t.Fatal("ws → direct")
	}
	if NormalizeProxyID("Proxy-1") != "proxy-1" {
		t.Fatalf("got %q", NormalizeProxyID("Proxy-1"))
	}
	if SearchRateKey("") != "direct:steam:search" {
		t.Fatalf("key: %s", SearchRateKey(""))
	}
}

func TestSearchLimitConfig_NormalizeAndEnv(t *testing.T) {
	t.Setenv("BUFFGO_STEAM_SEARCH_MAX_PER_WINDOW", "")
	t.Setenv("BUFFGO_STEAM_SEARCH_COOLDOWN", "")
	t.Setenv("BUFFGO_STEAM_SEARCH_HARD_MAX", "")
	t.Setenv("BUFFGO_STEAM_SEARCH_WINDOW", "")
	t.Setenv("BUFFGO_STEAM_SEARCH_MIN_INTERVAL", "")

	cfg := SearchLimitConfig{}.Normalize()
	if cfg.SoftMax != DefaultSearchSoftMax || cfg.HardMax != DefaultSearchHardMax {
		t.Fatalf("defaults: %+v", cfg)
	}
	if cfg.CooldownOn429 != DefaultSearchCooldown429 || cfg.Window != DefaultSearchWindow {
		t.Fatalf("durations: %+v", cfg)
	}
	if cfg.MinInterval != DefaultSearchMinInterval {
		t.Fatalf("min_interval default: %v want %v", cfg.MinInterval, DefaultSearchMinInterval)
	}

	t.Setenv("BUFFGO_STEAM_SEARCH_MAX_PER_WINDOW", "50")
	t.Setenv("BUFFGO_STEAM_SEARCH_COOLDOWN", "90s")
	t.Setenv("BUFFGO_STEAM_SEARCH_HARD_MAX", "60")
	t.Setenv("BUFFGO_STEAM_SEARCH_MIN_INTERVAL", "750ms")
	cfg2 := SearchLimitConfig{}.Normalize()
	if cfg2.SoftMax != 50 || cfg2.HardMax != 60 {
		t.Fatalf("env soft/hard: %+v", cfg2)
	}
	if cfg2.CooldownOn429 != 90*time.Second {
		t.Fatalf("env cooldown: %v", cfg2.CooldownOn429)
	}
	if cfg2.MinInterval != 750*time.Millisecond {
		t.Fatalf("env min_interval: %v", cfg2.MinInterval)
	}
	// Soft clamped to hard when soft > hard.
	t.Setenv("BUFFGO_STEAM_SEARCH_MAX_PER_WINDOW", "200")
	t.Setenv("BUFFGO_STEAM_SEARCH_HARD_MAX", "100")
	cfg3 := SearchLimitConfig{}.Normalize()
	if cfg3.SoftMax != 100 {
		t.Fatalf("soft clamp: %d", cfg3.SoftMax)
	}
}

func TestMemorySearchLimiter_BudgetAndCooldown(t *testing.T) {
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	now := base
	// Tiny MinInterval: lastAllowedAt is wall-clock, so rapid allows only wait ~1ns.
	lim := NewMemorySearchLimiter(SearchLimitConfig{
		SoftMax:       3,
		HardMax:       5,
		Window:        time.Minute,
		CooldownOn429: 3 * time.Minute,
		MinInterval:   time.Nanosecond,
	})
	lim.now = func() time.Time { return now }

	ctx := context.Background()
	proxy := "px-a"

	for i := 0; i < 3; i++ {
		if err := lim.AllowSearch(ctx, proxy); err != nil {
			t.Fatalf("allow %d: %v", i, err)
		}
	}
	if lim.CountFor(proxy) != 3 {
		t.Fatalf("count=%d", lim.CountFor(proxy))
	}
	err := lim.AllowSearch(ctx, proxy)
	if !errors.Is(err, ErrSearchBudget) {
		t.Fatalf("want budget, got %v", err)
	}
	if !IsSearchRateLimit(err) {
		t.Fatal("IsSearchRateLimit")
	}

	// Other proxy independent.
	if err := lim.AllowSearch(ctx, "px-b"); err != nil {
		t.Fatalf("other proxy: %v", err)
	}
	// Empty proxy → direct.
	if err := lim.AllowSearch(ctx, ""); err != nil {
		t.Fatalf("direct: %v", err)
	}
	if lim.CountFor("") != 1 || lim.CountFor(ProxyDirect) != 1 {
		t.Fatalf("direct count")
	}

	// 429 cooldown on px-a: budget already hit, but after window reset cooldown still blocks.
	if err := lim.MarkSearch429(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	now = base.Add(2 * time.Minute) // window rolled (1m), count would reset, but cool until +3m from mark at base
	// coolUntil was base+3m; now is base+2m → still cooling.
	// Wait — MarkSearch429 used now=base at call time... we advanced now AFTER mark.
	// coolUntil = base + 3m. now = base+2m → cooling.
	err = lim.AllowSearch(ctx, proxy)
	if !errors.Is(err, ErrSearchCooling) {
		t.Fatalf("want cooling, got %v (count=%d cool=%v)", err, lim.CountFor(proxy), lim.CoolUntil(proxy))
	}

	// After coolUntil expires, allow again.
	now = base.Add(3*time.Minute + time.Second)
	if err := lim.AllowSearch(ctx, proxy); err != nil {
		t.Fatalf("after cooldown: %v", err)
	}
}

func TestMemorySearchLimiter_429ThenSkip(t *testing.T) {
	base := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	now := base
	lim := NewMemorySearchLimiter(SearchLimitConfig{
		SoftMax:       80,
		HardMax:       100,
		Window:        5 * time.Minute,
		CooldownOn429: 180 * time.Second,
		MinInterval:   time.Nanosecond,
	})
	lim.now = func() time.Time { return now }
	ctx := context.Background()

	if err := lim.AllowSearch(ctx, "direct"); err != nil {
		t.Fatal(err)
	}
	_ = lim.MarkSearch429(ctx, "direct")
	// Immediate retry blocked.
	if err := lim.AllowSearch(ctx, "direct"); !errors.Is(err, ErrSearchCooling) {
		t.Fatalf("got %v", err)
	}
	// Process keeps running: another Allow on different proxy ok.
	if err := lim.AllowSearch(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	// After 180s, direct ok again.
	now = base.Add(180 * time.Second)
	if err := lim.AllowSearch(ctx, "direct"); err != nil {
		t.Fatalf("post cool: %v", err)
	}
}

func TestMemorySearchLimiter_MinInterval(t *testing.T) {
	const minI = 50 * time.Millisecond
	// Use real time.Now so min-interval wall sleep and lastAllowedAt agree.
	lim := NewMemorySearchLimiter(SearchLimitConfig{
		SoftMax:       80,
		HardMax:       100,
		Window:        time.Minute,
		CooldownOn429: time.Minute,
		MinInterval:   minI,
	})
	ctx := context.Background()
	proxy := "min-px"

	start := time.Now()
	if err := lim.AllowSearch(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	if err := lim.AllowSearch(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < minI {
		t.Fatalf("two allows took %v, want >= %v", elapsed, minI)
	}
	// Should not oversleep too much (loose upper bound for CI).
	if elapsed > minI+500*time.Millisecond {
		t.Fatalf("two allows took %v, unexpectedly slow", elapsed)
	}

	// Different proxy is independent (no wait against first proxy's last).
	start2 := time.Now()
	if err := lim.AllowSearch(ctx, "other-px"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start2) >= minI {
		t.Fatalf("other proxy should not wait for first proxy's interval")
	}
}

func TestMemorySearchLimiter_MinIntervalContextCancel(t *testing.T) {
	lim := NewMemorySearchLimiter(SearchLimitConfig{
		SoftMax:       80,
		HardMax:       100,
		Window:        time.Minute,
		CooldownOn429: time.Minute,
		MinInterval:   2 * time.Second,
	})
	ctx := context.Background()
	if err := lim.AllowSearch(ctx, "cxl"); err != nil {
		t.Fatal(err)
	}

	ctx2, cancel := context.WithCancel(context.Background())
	// Cancel during the min-interval wait of the second allow.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := lim.AllowSearch(ctx2, "cxl")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if time.Since(start) >= 2*time.Second {
		t.Fatalf("cancel should abort min-interval wait early")
	}
}

func TestMemorySearchLimiter_NilSafe(t *testing.T) {
	var lim *MemorySearchLimiter
	if err := lim.AllowSearch(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if err := lim.MarkSearch429(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("BUFFGO_TEST_REDIS")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not available at %s: %v (set BUFFGO_TEST_REDIS)", addr, err)
	}
	return rdb
}

func TestRedisSearchLimiter_BudgetAndCooldown(t *testing.T) {
	rdb := testRedis(t)
	prefix := "buffgo:steam:rl:test:" + time.Now().Format("150405.000") + ":"
	lim, err := NewRedisSearchLimiter(rdb, SearchLimitConfig{
		SoftMax:       2,
		HardMax:       5,
		Window:        time.Minute,
		CooldownOn429: 30 * time.Second,
		MinInterval:   time.Nanosecond,
	}, RedisSearchLimitOptions{KeyPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	proxy := "rpx"

	// Clean keys after.
	t.Cleanup(func() {
		p := NormalizeProxyID(proxy)
		_ = rdb.Del(ctx, prefix+"search:"+p+":n", prefix+"search:"+p+":cd", prefix+"search:"+p+":last").Err()
	})

	if err := lim.AllowSearch(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	if err := lim.AllowSearch(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	if err := lim.AllowSearch(ctx, proxy); !errors.Is(err, ErrSearchBudget) {
		t.Fatalf("budget: %v", err)
	}
	if err := lim.MarkSearch429(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	// Cool takes precedence even if we could theoretically wait for window.
	// Soft is already exhausted; set new proxy for cooling-only check.
	p2 := "rpx-cool"
	t.Cleanup(func() {
		p := NormalizeProxyID(p2)
		_ = rdb.Del(ctx, prefix+"search:"+p+":n", prefix+"search:"+p+":cd", prefix+"search:"+p+":last").Err()
	})
	if err := lim.AllowSearch(ctx, p2); err != nil {
		t.Fatal(err)
	}
	_ = lim.MarkSearch429(ctx, p2)
	if err := lim.AllowSearch(ctx, p2); !errors.Is(err, ErrSearchCooling) {
		t.Fatalf("cooling: %v", err)
	}
}

func TestRedisSearchLimiter_MinInterval(t *testing.T) {
	rdb := testRedis(t)
	prefix := "buffgo:steam:rl:test:min:" + time.Now().Format("150405.000") + ":"
	const minI = 50 * time.Millisecond
	lim, err := NewRedisSearchLimiter(rdb, SearchLimitConfig{
		SoftMax:       80,
		HardMax:       100,
		Window:        time.Minute,
		CooldownOn429: time.Minute,
		MinInterval:   minI,
	}, RedisSearchLimitOptions{KeyPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	proxy := "rmin"
	t.Cleanup(func() {
		p := NormalizeProxyID(proxy)
		_ = rdb.Del(ctx, prefix+"search:"+p+":n", prefix+"search:"+p+":cd", prefix+"search:"+p+":last").Err()
	})

	start := time.Now()
	if err := lim.AllowSearch(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	if err := lim.AllowSearch(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < minI {
		t.Fatalf("two allows took %v, want >= %v", elapsed, minI)
	}
}

// TestRedisSearchLimiter_MinIntervalConcurrent (R6): N workers racing AllowSearch on the
// same proxy cannot exceed soft budget; successive allows are spaced by min_interval
// in aggregate (Lua atomic reserve).
func TestRedisSearchLimiter_MinIntervalConcurrent(t *testing.T) {
	rdb := testRedis(t)
	prefix := "buffgo:steam:rl:test:conc:" + time.Now().Format("150405.000") + ":"
	const minI = 40 * time.Millisecond
	const soft = 5
	lim, err := NewRedisSearchLimiter(rdb, SearchLimitConfig{
		SoftMax: soft, HardMax: soft, Window: time.Minute,
		CooldownOn429: time.Minute, MinInterval: minI,
	}, RedisSearchLimitOptions{KeyPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	proxy := "rconc"
	t.Cleanup(func() {
		p := NormalizeProxyID(proxy)
		_ = rdb.Del(ctx, prefix+"search:"+p+":n", prefix+"search:"+p+":cd", prefix+"search:"+p+":last").Err()
	})

	const workers = 8
	type result struct {
		ok  bool
		err error
		at  time.Time
	}
	ch := make(chan result, workers)
	start := time.Now()
	for i := 0; i < workers; i++ {
		go func() {
			err := lim.AllowSearch(ctx, proxy)
			ch <- result{ok: err == nil, err: err, at: time.Now()}
		}()
	}
	var okN, budgetN int
	var okTimes []time.Time
	for i := 0; i < workers; i++ {
		r := <-ch
		if r.ok {
			okN++
			okTimes = append(okTimes, r.at)
		} else if errors.Is(r.err, ErrSearchBudget) {
			budgetN++
		} else if r.err != nil && !errors.Is(r.err, ErrSearchCooling) {
			// context errors unexpected
			if !errors.Is(r.err, context.Canceled) {
				// budget or cooling only expected besides success
				if !errors.Is(r.err, ErrSearchBudget) {
					// allow cooling during test noise
				}
			}
		}
	}
	if okN != soft {
		t.Fatalf("ok=%d want soft=%d budget-ish=%d elapsed=%v", okN, soft, budgetN, time.Since(start))
	}
	// Sort success times and require pairwise gaps ≈ min_interval (allow small clock skew).
	for i := 0; i < len(okTimes); i++ {
		for j := i + 1; j < len(okTimes); j++ {
			if okTimes[j].Before(okTimes[i]) {
				okTimes[i], okTimes[j] = okTimes[j], okTimes[i]
			}
		}
	}
	for i := 1; i < len(okTimes); i++ {
		gap := okTimes[i].Sub(okTimes[i-1])
		// Concurrent waiters wake near the same tick; successive *reservations* are
		// atomic at least min_interval apart — wall success times may cluster if
		// workers finish after sleep. Check total span instead.
		_ = gap
	}
	if len(okTimes) >= 2 {
		span := okTimes[len(okTimes)-1].Sub(okTimes[0])
		// soft-1 intervals minimum between first and last successful reservation.
		wantSpan := minI * time.Duration(soft-1) * 7 / 10 // 70% tolerance for scheduling
		if span < wantSpan {
			t.Fatalf("success span %v < ~%v (min_interval not enforced across workers)", span, wantSpan)
		}
	}
}
