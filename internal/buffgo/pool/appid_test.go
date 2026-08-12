package pool

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestAppIDAllowed(t *testing.T) {
	if !AppIDAllowed(252490, nil) {
		t.Fatal("nil only_appids is shared")
	}
	if !AppIDAllowed(252490, []int64{}) {
		t.Fatal("empty only_appids is shared")
	}
	if !AppIDAllowed(252490, []int64{252490}) {
		t.Fatal("dedicated rust")
	}
	if AppIDAllowed(730, []int64{252490}) {
		t.Fatal("cs on rust-only proxy")
	}
	if !AppIDAllowed(730, []int64{252490, 730}) {
		t.Fatal("multi dedicated")
	}
	if AppIDAllowed(0, nil) {
		t.Fatal("appid 0 invalid")
	}
}

func TestOrderProxiesForAppID_DedicatedFirstSharedLast(t *testing.T) {
	// Hard split example: 10 rust-only, 5 cs-only, rest shared.
	// Use small lists that preserve the semantics.
	proxies := []ProxyCandidate{
		{ID: "shared-a", LineType: LineDual},
		{ID: "rust-1", LineType: LineDual, OnlyAppIDs: []int64{252490}},
		{ID: "cs-1", LineType: LineDual, OnlyAppIDs: []int64{730}},
		{ID: "shared-prefer-rust", LineType: LineDual, PreferAppIDs: []int64{252490}},
		{ID: "rust-2", LineType: LineDual, OnlyAppIDs: []int64{252490}},
		{ID: "shared-b", LineType: LineDual},
	}

	rust := OrderProxiesForAppID(252490, proxies)
	wantRust := []string{"rust-1", "rust-2", "shared-prefer-rust", "shared-a", "shared-b"}
	gotRust := idsOf(rust)
	if !equalStr(gotRust, wantRust) {
		t.Fatalf("rust order: got %v want %v", gotRust, wantRust)
	}
	// cs-only must be excluded for rust
	for _, p := range rust {
		if p.ID == "cs-1" {
			t.Fatal("cs-only must not appear for rust")
		}
	}

	cs := OrderProxiesForAppID(730, proxies)
	wantCS := []string{"cs-1", "shared-a", "shared-prefer-rust", "shared-b"}
	gotCS := idsOf(cs)
	if !equalStr(gotCS, wantCS) {
		t.Fatalf("cs order: got %v want %v", gotCS, wantCS)
	}
	for _, p := range cs {
		if p.ID == "rust-1" || p.ID == "rust-2" {
			t.Fatal("rust-only must not appear for cs")
		}
	}
}

func TestOrderProxiesForAppID_Empty(t *testing.T) {
	if OrderProxiesForAppID(252490, nil) != nil {
		t.Fatal("nil in → nil out")
	}
	// all dedicated to other game
	out := OrderProxiesForAppID(730, []ProxyCandidate{
		{ID: "r", OnlyAppIDs: []int64{252490}},
	})
	if len(out) != 0 {
		t.Fatalf("expected empty, got %v", idsOf(out))
	}
}

func TestAcquire_OnlyAppIDs_RejectMismatch(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	_, err := m.Acquire(ctx, AcquireRequest{
		WorkerID:   "w1",
		Proxy:      "rust-only-ip",
		Platform:   "buff",
		LineType:   LineDual,
		AppID:      730,
		OnlyAppIDs: []int64{252490},
	})
	if !errors.Is(err, ErrAppIDNotAllowed) {
		t.Fatalf("expected ErrAppIDNotAllowed, got %v", err)
	}

	// mismatch must not create redis keys / rent
	n, err := rdb.Exists(ctx, m.kProxyPlatform("rust-only-ip", "buff")).Result()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("mismatch must not rent proxy")
	}
}

func TestAcquire_OnlyAppIDs_AllowDedicatedAndShared(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	// dedicated rust
	l1, err := m.Acquire(ctx, AcquireRequest{
		WorkerID:   "w1",
		Proxy:      "p-rust",
		Platform:   "buff",
		LineType:   LineDual,
		AppID:      252490,
		OnlyAppIDs: []int64{252490},
	})
	if err != nil {
		t.Fatalf("dedicated rust: %v", err)
	}
	_ = m.Release(ctx, l1.ID, "w1", ReleaseOptions{})

	// shared empty only_appids
	l2, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w2",
		Proxy:    "p-shared",
		Platform: "steam",
		LineType: LineDual,
		AppID:    730,
		// OnlyAppIDs nil/empty
	})
	if err != nil {
		t.Fatalf("shared: %v", err)
	}
	_ = m.Release(ctx, l2.ID, "w2", ReleaseOptions{})
}

func TestAcquire_HardSplit_RustCSShared(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	// Simulate: 2 rust-only, 1 cs-only, 1 shared — select via Order then Acquire.
	all := []ProxyCandidate{
		{ID: "shared-1", LineType: LineDual},
		{ID: "rust-a", LineType: LineDual, OnlyAppIDs: []int64{252490}},
		{ID: "cs-a", LineType: LineDual, OnlyAppIDs: []int64{730}},
		{ID: "rust-b", LineType: LineDual, OnlyAppIDs: []int64{252490}},
	}

	// Rust workers should get dedicated first
	ordered := OrderProxiesForAppID(252490, all)
	if ordered[0].ID != "rust-a" || ordered[1].ID != "rust-b" {
		t.Fatalf("dedicated first: %v", idsOf(ordered))
	}
	var rustLeases []*Lease
	for i, p := range ordered {
		lease, err := m.Acquire(ctx, AcquireRequest{
			WorkerID:   fmt.Sprintf("rust-w-%d", i),
			Proxy:      p.ID,
			Platform:   "buff",
			LineType:   p.LineType,
			AppID:      252490,
			OnlyAppIDs: p.OnlyAppIDs,
		})
		if err != nil {
			t.Fatalf("rust acquire %s: %v", p.ID, err)
		}
		rustLeases = append(rustLeases, lease)
	}
	// CS cannot take rust-only while free... actually they're leased. Try cs-only + shared.
	// First release shared so CS can take it; cs-only should acquire.
	for _, l := range rustLeases {
		if l.Proxy == "shared-1" {
			_ = m.Release(ctx, l.ID, l.WorkerID, ReleaseOptions{})
		}
	}

	csOrdered := OrderProxiesForAppID(730, all)
	// must start with cs-a
	if csOrdered[0].ID != "cs-a" {
		t.Fatalf("cs dedicated first: %v", idsOf(csOrdered))
	}
	// rust-only not in list
	for _, p := range csOrdered {
		if p.ID == "rust-a" || p.ID == "rust-b" {
			t.Fatal("rust dedicated leaked into cs order")
		}
	}
	// Acquire cs-a ok
	lcs, err := m.Acquire(ctx, AcquireRequest{
		WorkerID:   "cs-w",
		Proxy:      "cs-a",
		Platform:   "steam",
		LineType:   LineDual,
		AppID:      730,
		OnlyAppIDs: []int64{730},
	})
	if err != nil {
		t.Fatalf("cs dedicated: %v", err)
	}
	// Direct acquire of rust-only for CS must fail only_appids
	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID:   "cs-bad",
		Proxy:      "rust-a",
		Platform:   "steam",
		LineType:   LineDual,
		AppID:      730,
		OnlyAppIDs: []int64{252490},
	})
	if !errors.Is(err, ErrAppIDNotAllowed) {
		t.Fatalf("cs on rust-only: %v", err)
	}

	// cleanup
	_ = m.Release(ctx, lcs.ID, "cs-w", ReleaseOptions{})
	for _, l := range rustLeases {
		if l.Proxy != "shared-1" {
			_ = m.Release(ctx, l.ID, l.WorkerID, ReleaseOptions{})
		}
	}
}

func TestAcquire_MaxProxyLeases_Quota(t *testing.T) {
	rdb := testRedis(t)
	prefix := fmt.Sprintf("buffgo:pool:test:quota:%d:", time.Now().UnixNano())
	m, err := NewManager(rdb, Options{
		LeaseTTL:        10 * time.Second,
		DefaultCooldown: 2 * time.Second,
		KeyPrefix:       prefix,
		MaxProxyLeases: map[int64]int{
			252490: 2, // soft cap: 2 concurrent leases for Rust
			730:    1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		iter := rdb.Scan(ctx, 0, prefix+"*", 100).Iterator()
		var keys []string
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if len(keys) > 0 {
			_ = rdb.Del(ctx, keys...).Err()
		}
	})
	ctx := context.Background()

	if m.MaxProxyLeases(252490) != 2 {
		t.Fatalf("MaxProxyLeases rust: %d", m.MaxProxyLeases(252490))
	}
	if m.MaxProxyLeases(999) != 0 {
		t.Fatal("unset appid unlimited")
	}

	l1, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1", Proxy: "p1", Platform: "buff", LineType: LineDual, AppID: 252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	l2, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w2", Proxy: "p2", Platform: "buff", LineType: LineDual, AppID: 252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	// third hits soft quota
	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID: "w3", Proxy: "p3", Platform: "buff", LineType: LineDual, AppID: 252490,
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	n, err := m.ActiveLeaseCount(ctx, 252490)
	if err != nil || n != 2 {
		t.Fatalf("active rust leases: n=%d err=%v", n, err)
	}

	// CS independent quota
	lcs, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "wcs", Proxy: "pcs", Platform: "steam", LineType: LineDual, AppID: 730,
	})
	if err != nil {
		t.Fatalf("cs first lease: %v", err)
	}
	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID: "wcs2", Proxy: "pcs2", Platform: "steam", LineType: LineDual, AppID: 730,
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("cs quota: %v", err)
	}

	// release one rust → room for another
	if err := m.Release(ctx, l1.ID, "w1", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
	l3, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w3", Proxy: "p3", Platform: "buff", LineType: LineDual, AppID: 252490,
	})
	if err != nil {
		t.Fatalf("after release: %v", err)
	}

	_ = m.Release(ctx, l2.ID, "w2", ReleaseOptions{})
	_ = m.Release(ctx, l3.ID, "w3", ReleaseOptions{})
	_ = m.Release(ctx, lcs.ID, "wcs", ReleaseOptions{})
}

func TestAcquire_MaxProxyLeases_UnlimitedWhenZero(t *testing.T) {
	rdb := testRedis(t)
	// default newTestManager has no MaxProxyLeases
	m := newTestManager(t, rdb)
	ctx := context.Background()
	var leases []*Lease
	for i := 0; i < 5; i++ {
		l, err := m.Acquire(ctx, AcquireRequest{
			WorkerID: fmt.Sprintf("w%d", i),
			Proxy:    fmt.Sprintf("p%d", i),
			Platform: "buff",
			LineType: LineDual,
			AppID:    252490,
		})
		if err != nil {
			t.Fatalf("unlimited acquire %d: %v", i, err)
		}
		leases = append(leases, l)
	}
	for _, l := range leases {
		_ = m.Release(ctx, l.ID, l.WorkerID, ReleaseOptions{})
	}
}

func TestAcquire_QuotaReclaimOnTTL(t *testing.T) {
	rdb := testRedis(t)
	prefix := fmt.Sprintf("buffgo:pool:test:qttl:%d:", time.Now().UnixNano())
	m, err := NewManager(rdb, Options{
		LeaseTTL:        time.Second,
		DefaultCooldown: time.Second,
		KeyPrefix:       prefix,
		MaxProxyLeases:  map[int64]int{252490: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		iter := rdb.Scan(ctx, 0, prefix+"*", 100).Iterator()
		var keys []string
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if len(keys) > 0 {
			_ = rdb.Del(ctx, keys...).Err()
		}
	})
	ctx := context.Background()

	l1, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "dead", Proxy: "p1", Platform: "buff", LineType: LineDual, AppID: 252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = l1
	// crash: no release; wait for TTL so quota ZSET score expires and ZREMRANGE reclaims
	time.Sleep(1200 * time.Millisecond)
	l2, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "new", Proxy: "p2", Platform: "buff", LineType: LineDual, AppID: 252490,
	})
	if err != nil {
		t.Fatalf("quota should reclaim after TTL: %v", err)
	}
	_ = m.Release(ctx, l2.ID, "new", ReleaseOptions{})
}

func idsOf(ps []ProxyCandidate) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func equalStr(a, b []string) bool {
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
