package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// P3.4 multi-worker behaviour verification.
//
// Acceptance (ROADMAP / todo):
//   - multi worker 不双租
//   - Buff 冷却后同 IP 可给 Steam
//   - 线路与游戏划分生效（line_type prefer + only_appids / max_proxy_leases）
//
// Evidence: two Manager instances share one Redis keyspace (two worker processes).

// newDualWorkers builds two Managers with the same prefix/options (worker×2).
func newDualWorkers(t *testing.T, rdb *redis.Client, opt Options) (ma, mb *Manager, prefix string) {
	t.Helper()
	prefix = fmt.Sprintf("buffgo:pool:mw:%d:", time.Now().UnixNano())
	opt.KeyPrefix = prefix
	if opt.LeaseTTL <= 0 {
		opt.LeaseTTL = 8 * time.Second
	}
	if opt.DefaultCooldown <= 0 {
		opt.DefaultCooldown = 3 * time.Second
	}
	var err error
	ma, err = NewManager(rdb, opt)
	if err != nil {
		t.Fatal(err)
	}
	mb, err = NewManager(rdb, opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		iter := rdb.Scan(ctx, 0, prefix+"*", 200).Iterator()
		var keys []string
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if len(keys) > 0 {
			_ = rdb.Del(ctx, keys...).Err()
		}
	})
	return ma, mb, prefix
}

// TestP34_MultiWorker_NoDoubleLease: two worker Managers cannot both hold the
// same (proxy, platform); concurrent cross-manager Acquire has exactly one winner.
func TestP34_MultiWorker_NoDoubleLease(t *testing.T) {
	rdb := testRedis(t)
	ma, mb, _ := newDualWorkers(t, rdb, Options{})
	ctx := context.Background()

	// Sequential: worker-a holds → worker-b busy
	la, err := ma.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-a",
		Proxy:    "mw-proxy-1",
		Account:  "acc-shared",
		Platform: "buff",
		LineType: LineDual,
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("worker-a acquire: %v", err)
	}
	_, err = mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    "mw-proxy-1",
		Account:  "acc-other",
		Platform: "buff",
		LineType: LineDual,
		AppID:    252490,
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("worker-b same proxy+platform: want ErrBusy, got %v", err)
	}
	// same account on different proxy also exclusive
	_, err = mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    "mw-proxy-2",
		Account:  "acc-shared",
		Platform: "buff",
		LineType: LineDual,
		AppID:    252490,
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("worker-b same account: want ErrBusy, got %v", err)
	}
	if err := ma.Release(ctx, la.ID, "worker-a", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}

	// Concurrent race across two managers (same Redis): exactly one winner
	const n = 16
	var okCount, busyCount atomic.Int64
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners []*Lease

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := ma
			if i%2 == 1 {
				m = mb
			}
			lease, err := m.Acquire(ctx, AcquireRequest{
				WorkerID: fmt.Sprintf("race-w-%d", i),
				Proxy:    "mw-race-proxy",
				Platform: "steam",
				LineType: LineOversea,
				AppID:    252490,
			})
			if err == nil {
				okCount.Add(1)
				mu.Lock()
				winners = append(winners, lease)
				mu.Unlock()
				return
			}
			if errors.Is(err, ErrBusy) {
				busyCount.Add(1)
				return
			}
			t.Errorf("unexpected acquire err: %v", err)
		}(i)
	}
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("expected exactly 1 winner, got %d busy=%d", okCount.Load(), busyCount.Load())
	}
	if busyCount.Load() != n-1 {
		t.Fatalf("expected %d busy, got %d", n-1, busyCount.Load())
	}
	for _, w := range winners {
		_ = ma.Release(ctx, w.ID, w.WorkerID, ReleaseOptions{})
		_ = mb.Release(ctx, w.ID, w.WorkerID, ReleaseOptions{})
	}
}

// TestP34_BuffCooldown_ThenSteamSameIP: worker-a releases Buff with cooldown;
// worker-b cannot re-lease Buff on that IP but can lease Steam on the same IP.
func TestP34_BuffCooldown_ThenSteamSameIP(t *testing.T) {
	rdb := testRedis(t)
	ma, mb, _ := newDualWorkers(t, rdb, Options{
		DefaultCooldown: 15 * time.Second,
	})
	ctx := context.Background()
	const ip = "mw-cool-ip:8080"

	lb, err := ma.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-a",
		Proxy:    ip,
		Account:  "buff-cookie",
		Platform: "buff",
		LineType: LineCN,
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("buff acquire: %v", err)
	}
	if err := ma.Release(ctx, lb.ID, "worker-a", ReleaseOptions{
		SetCooldown: true,
		Cooldown:    15 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}

	cooling, err := mb.IsCooling(ctx, ip, "buff")
	if err != nil || !cooling {
		t.Fatalf("buff should cool (cross-manager): cooling=%v err=%v", cooling, err)
	}
	_, err = mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    ip,
		Platform: "buff",
		LineType: LineDual,
		AppID:    252490,
	})
	if !errors.Is(err, ErrCooling) {
		t.Fatalf("buff re-acquire during cooldown: want ErrCooling, got %v", err)
	}

	// Same IP → Steam still free (platform-independent cooldown)
	ls, err := mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    ip,
		Platform: "steam",
		LineType: LineOversea,
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("steam on same IP during buff cooldown: %v", err)
	}
	if ls.Proxy != ip || ls.Platform != "steam" {
		t.Fatalf("steam lease fields: %+v", ls)
	}
	// worker-a can still see steam not cooling
	steamCool, err := ma.IsCooling(ctx, ip, "steam")
	if err != nil || steamCool {
		t.Fatalf("steam must not cool: %v %v", steamCool, err)
	}
	if err := mb.Release(ctx, ls.ID, "worker-b", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
}

// TestP34_LinePrefer_MultiWorker: line_type prefer rejects mismatch; matching
// lines acquire under two managers; concurrent dual-proxy contention stays exclusive.
func TestP34_LinePrefer_MultiWorker(t *testing.T) {
	rdb := testRedis(t)
	ma, mb, _ := newDualWorkers(t, rdb, Options{
		// product defaults via nil PlatformLines; also pin explicit for clarity
		PlatformLines: map[string][]string{
			"buff":  {LineCN, LineDual, LineHK},
			"steam": {LineOversea, LineDual, LineHK},
		},
	})
	ctx := context.Background()

	// mismatch must not rent (no lock left behind)
	_, err := ma.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-a",
		Proxy:    "line-bad-buff",
		Platform: "buff",
		LineType: LineOversea, // buff prefer excludes oversea
		AppID:    252490,
	})
	if !errors.Is(err, ErrLineMismatch) {
		t.Fatalf("buff+oversea: want ErrLineMismatch, got %v", err)
	}
	n, err := rdb.Exists(ctx, ma.kProxyPlatform("line-bad-buff", "buff")).Result()
	if err != nil || n != 0 {
		t.Fatalf("mismatch must not create lock: n=%d err=%v", n, err)
	}

	_, err = mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    "line-bad-steam",
		Platform: "steam",
		LineType: LineCN,
		AppID:    252490,
	})
	if !errors.Is(err, ErrLineMismatch) {
		t.Fatalf("steam+cn: want ErrLineMismatch, got %v", err)
	}

	// happy path: buff cn on worker-a, steam oversea on worker-b (different proxies)
	la, err := ma.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-a",
		Proxy:    "cn-ip-1",
		Platform: "buff",
		LineType: LineCN,
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("buff cn: %v", err)
	}
	lb, err := mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    "oversea-ip-1",
		Platform: "steam",
		LineType: LineOversea,
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("steam oversea: %v", err)
	}
	// dual proxy can serve either; exclusivity still per (proxy, platform)
	ld, err := ma.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-a2",
		Proxy:    "dual-ip-1",
		Platform: "buff",
		LineType: LineDual,
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("buff dual: %v", err)
	}
	// second worker cannot take same dual for buff
	_, err = mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b2",
		Proxy:    "dual-ip-1",
		Platform: "buff",
		LineType: LineDual,
		AppID:    252490,
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("dual buff second worker: want ErrBusy, got %v", err)
	}
	// but steam on that dual is free for worker-b
	ls, err := mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b2",
		Proxy:    "dual-ip-1",
		Platform: "steam",
		LineType: LineDual,
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("steam dual concurrent with buff dual: %v", err)
	}

	_ = ma.Release(ctx, la.ID, "worker-a", ReleaseOptions{})
	_ = mb.Release(ctx, lb.ID, "worker-b", ReleaseOptions{})
	_ = ma.Release(ctx, ld.ID, "worker-a2", ReleaseOptions{})
	_ = mb.Release(ctx, ls.ID, "worker-b2", ReleaseOptions{})
}

// TestP34_OnlyAppIDs_And_MaxProxyLeases_MultiWorker: hard-split only_appids +
// soft max_proxy_leases across two worker Managers.
func TestP34_OnlyAppIDs_And_MaxProxyLeases_MultiWorker(t *testing.T) {
	rdb := testRedis(t)
	const (
		rust = int64(252490)
		cs   = int64(730)
	)
	ma, mb, _ := newDualWorkers(t, rdb, Options{
		MaxProxyLeases: map[int64]int{
			rust: 2, // soft cap: 2 concurrent rust leases total (both workers)
			cs:   1,
		},
	})
	ctx := context.Background()

	// Pool layout: 2 rust-only, 1 cs-only, 1 shared dual
	all := []ProxyCandidate{
		{ID: "shared-1", LineType: LineDual},
		{ID: "rust-a", LineType: LineDual, OnlyAppIDs: []int64{rust}},
		{ID: "cs-a", LineType: LineDual, OnlyAppIDs: []int64{cs}},
		{ID: "rust-b", LineType: LineDual, OnlyAppIDs: []int64{rust}},
	}

	// Worker-a takes first dedicated rust; worker-b takes second — order dedicated first
	ordered := OrderProxiesForAppID(rust, all)
	if len(ordered) < 2 || ordered[0].ID != "rust-a" || ordered[1].ID != "rust-b" {
		t.Fatalf("rust order dedicated first: %v", idsOf(ordered))
	}

	lRustA, err := ma.Acquire(ctx, AcquireRequest{
		WorkerID:   "worker-a",
		Proxy:      ordered[0].ID,
		Platform:   "buff",
		LineType:   ordered[0].LineType,
		AppID:      rust,
		OnlyAppIDs: ordered[0].OnlyAppIDs,
	})
	if err != nil {
		t.Fatalf("worker-a rust dedicated: %v", err)
	}
	lRustB, err := mb.Acquire(ctx, AcquireRequest{
		WorkerID:   "worker-b",
		Proxy:      ordered[1].ID,
		Platform:   "buff",
		LineType:   ordered[1].LineType,
		AppID:      rust,
		OnlyAppIDs: ordered[1].OnlyAppIDs,
	})
	if err != nil {
		t.Fatalf("worker-b rust dedicated: %v", err)
	}

	// Soft quota max_proxy_leases=2: third concurrent rust lease rejected
	// even on free shared proxy
	_, err = ma.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-a-extra",
		Proxy:    "shared-1",
		Platform: "steam",
		LineType: LineDual,
		AppID:    rust,
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("rust soft quota: want ErrQuotaExceeded, got %v", err)
	}
	n, err := mb.ActiveLeaseCount(ctx, rust)
	if err != nil || n != 2 {
		t.Fatalf("active rust leases: n=%d err=%v", n, err)
	}

	// CS cannot rent rust-only proxy (only_appids hard split)
	_, err = mb.Acquire(ctx, AcquireRequest{
		WorkerID:   "worker-b-cs",
		Proxy:      "rust-a",
		Platform:   "steam",
		LineType:   LineDual,
		AppID:      cs,
		OnlyAppIDs: []int64{rust},
	})
	if !errors.Is(err, ErrAppIDNotAllowed) {
		t.Fatalf("cs on rust-only: want ErrAppIDNotAllowed, got %v", err)
	}

	// CS dedicated works for worker-b while rust leases held
	csOrdered := OrderProxiesForAppID(cs, all)
	if csOrdered[0].ID != "cs-a" {
		t.Fatalf("cs dedicated first: %v", idsOf(csOrdered))
	}
	for _, p := range csOrdered {
		if p.ID == "rust-a" || p.ID == "rust-b" {
			t.Fatal("rust dedicated must not appear in cs order")
		}
	}
	lCS, err := mb.Acquire(ctx, AcquireRequest{
		WorkerID:   "worker-b-cs",
		Proxy:      "cs-a",
		Platform:   "steam",
		LineType:   LineDual,
		AppID:      cs,
		OnlyAppIDs: []int64{cs},
	})
	if err != nil {
		t.Fatalf("cs dedicated: %v", err)
	}
	// cs max_proxy_leases=1 → second CS blocked
	_, err = ma.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-a-cs",
		Proxy:    "shared-1",
		Platform: "steam",
		LineType: LineDual,
		AppID:    cs,
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("cs soft quota: want ErrQuotaExceeded, got %v", err)
	}

	// Release one rust → room for shared rust lease from the other worker
	if err := ma.Release(ctx, lRustA.ID, "worker-a", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
	lShared, err := mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b-shared",
		Proxy:    "shared-1",
		Platform: "steam",
		LineType: LineDual,
		AppID:    rust,
	})
	if err != nil {
		t.Fatalf("after release, shared rust: %v", err)
	}

	// Concurrent multi-worker race on last CS slot after release
	if err := mb.Release(ctx, lCS.ID, "worker-b-cs", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
	var ok, busy atomic.Int64
	var wg sync.WaitGroup
	var winMu sync.Mutex
	var win *Lease
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := ma
			if i%2 == 1 {
				m = mb
			}
			lease, err := m.Acquire(ctx, AcquireRequest{
				WorkerID: fmt.Sprintf("cs-race-%d", i),
				Proxy:    fmt.Sprintf("cs-race-p-%d", i), // distinct proxies; quota is the constraint
				Platform: "steam",
				LineType: LineDual,
				AppID:    cs,
			})
			if err == nil {
				ok.Add(1)
				winMu.Lock()
				if win == nil {
					win = lease
				} else {
					// should not happen with quota 1; release extras if race window
					_ = m.Release(ctx, lease.ID, lease.WorkerID, ReleaseOptions{})
				}
				winMu.Unlock()
				return
			}
			if errors.Is(err, ErrQuotaExceeded) {
				busy.Add(1)
				return
			}
			t.Errorf("cs race unexpected: %v", err)
		}(i)
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("cs quota race: want 1 winner, got ok=%d busy=%d", ok.Load(), busy.Load())
	}
	if busy.Load() != 9 {
		t.Fatalf("cs quota race: want 9 quota, got busy=%d", busy.Load())
	}

	// cleanup
	if win != nil {
		_ = ma.Release(ctx, win.ID, win.WorkerID, ReleaseOptions{})
		_ = mb.Release(ctx, win.ID, win.WorkerID, ReleaseOptions{})
	}
	_ = mb.Release(ctx, lRustB.ID, "worker-b", ReleaseOptions{})
	_ = mb.Release(ctx, lShared.ID, "worker-b-shared", ReleaseOptions{})
}

// TestP34_AcceptanceScenario_EndToEnd walks the full P3 multi-worker story on
// two Manager instances: exclusive lease → buff cool → steam same IP → line +
// game split + soft quota.
func TestP34_AcceptanceScenario_EndToEnd(t *testing.T) {
	rdb := testRedis(t)
	const (
		rust = int64(252490)
		cs   = int64(730)
	)
	ma, mb, _ := newDualWorkers(t, rdb, Options{
		LeaseTTL:        10 * time.Second,
		DefaultCooldown: 20 * time.Second,
		PlatformLines: map[string][]string{
			"buff":  {LineCN, LineDual, LineHK},
			"steam": {LineOversea, LineDual, LineHK},
		},
		MaxProxyLeases: map[int64]int{rust: 3, cs: 2},
	})
	ctx := context.Background()

	proxies := []ProxyCandidate{
		{ID: "cn-rust", LineType: LineCN, OnlyAppIDs: []int64{rust}},
		{ID: "dual-shared", LineType: LineDual},
		{ID: "oversea-cs", LineType: LineOversea, OnlyAppIDs: []int64{cs}},
		{ID: "hk-shared", LineType: LineHK},
	}

	// 1) Worker-a acquires buff on dedicated cn rust IP
	rustProxies := OrderProxiesForAppID(rust, proxies)
	if rustProxies[0].ID != "cn-rust" {
		t.Fatalf("dedicated rust first: %v", idsOf(rustProxies))
	}
	// skip line-incompatible candidates for buff (cn/dual/hk only)
	var buffLease *Lease
	for _, p := range rustProxies {
		if !LineAllowed("buff", p.LineType, nil) {
			continue
		}
		l, err := ma.Acquire(ctx, AcquireRequest{
			WorkerID:   "worker-a",
			Proxy:      p.ID,
			Account:    "buff-acc-1",
			Platform:   "buff",
			LineType:   p.LineType,
			AppID:      rust,
			OnlyAppIDs: p.OnlyAppIDs,
		})
		if err != nil {
			t.Fatalf("worker-a buff select %s: %v", p.ID, err)
		}
		buffLease = l
		break
	}
	if buffLease == nil {
		t.Fatal("no buff-capable proxy for rust")
	}

	// 2) Worker-b cannot double-rent same proxy+platform
	_, err := mb.Acquire(ctx, AcquireRequest{
		WorkerID:   "worker-b",
		Proxy:      buffLease.Proxy,
		Platform:   "buff",
		LineType:   buffLease.LineType,
		AppID:      rust,
		OnlyAppIDs: []int64{rust},
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("no double lease: want ErrBusy, got %v", err)
	}

	// 3) Release with Buff cooldown; Steam same IP on worker-b
	if err := ma.Release(ctx, buffLease.ID, "worker-a", ReleaseOptions{SetCooldown: true}); err != nil {
		t.Fatal(err)
	}
	_, err = mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    buffLease.Proxy,
		Platform: "buff",
		LineType: LineCN,
		AppID:    rust,
	})
	if !errors.Is(err, ErrCooling) {
		t.Fatalf("buff cooling: %v", err)
	}
	// cn line is not in steam prefer → line mismatch even if not cooling
	_, err = mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    buffLease.Proxy,
		Platform: "steam",
		LineType: LineCN,
		AppID:    rust,
	})
	if !errors.Is(err, ErrLineMismatch) {
		t.Fatalf("steam+cn line: want ErrLineMismatch, got %v", err)
	}
	// dual-shared: buff cool independent; steam dual ok
	steamL, err := mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    "dual-shared",
		Platform: "steam",
		LineType: LineDual,
		AppID:    rust,
	})
	if err != nil {
		t.Fatalf("steam dual after buff cool elsewhere: %v", err)
	}

	// If we had dual on buff with cooldown, steam same dual must work:
	// re-lease dual for buff with cool then steam same dual from other worker
	lBuffDual, err := ma.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-a",
		Proxy:    "hk-shared",
		Platform: "buff",
		LineType: LineHK,
		AppID:    rust,
	})
	if err != nil {
		t.Fatalf("buff hk: %v", err)
	}
	if err := ma.Release(ctx, lBuffDual.ID, "worker-a", ReleaseOptions{
		SetCooldown: true,
		Cooldown:    20 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	// same IP steam for other worker
	lSteamSame, err := mb.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b2",
		Proxy:    "hk-shared",
		Platform: "steam",
		LineType: LineHK,
		AppID:    rust,
	})
	if err != nil {
		t.Fatalf("steam same IP after buff cooldown: %v", err)
	}

	// 4) only_appids: CS cannot take rust-only
	_, err = ma.Acquire(ctx, AcquireRequest{
		WorkerID:   "worker-a-cs",
		Proxy:      "cn-rust",
		Platform:   "buff",
		LineType:   LineCN,
		AppID:      cs,
		OnlyAppIDs: []int64{rust},
	})
	if !errors.Is(err, ErrAppIDNotAllowed) {
		t.Fatalf("only_appids: %v", err)
	}
	// CS oversea dedicated ok
	lCS, err := ma.Acquire(ctx, AcquireRequest{
		WorkerID:   "worker-a-cs",
		Proxy:      "oversea-cs",
		Platform:   "steam",
		LineType:   LineOversea,
		AppID:      cs,
		OnlyAppIDs: []int64{cs},
	})
	if err != nil {
		t.Fatalf("cs oversea: %v", err)
	}

	// 5) Soft quota still counted across managers
	// rust has steam dual + steam hk = 2; one more room under max 3
	nRust, err := ma.ActiveLeaseCount(ctx, rust)
	if err != nil {
		t.Fatal(err)
	}
	if nRust != 2 {
		t.Fatalf("expected 2 active rust leases, got %d", nRust)
	}

	_ = mb.Release(ctx, steamL.ID, "worker-b", ReleaseOptions{})
	_ = mb.Release(ctx, lSteamSame.ID, "worker-b2", ReleaseOptions{})
	_ = ma.Release(ctx, lCS.ID, "worker-a-cs", ReleaseOptions{})
}
