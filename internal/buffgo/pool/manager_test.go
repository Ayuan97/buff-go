package pool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

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
		_ = rdb.Close()
		t.Skipf("redis not available at %s: %v (set BUFFGO_TEST_REDIS)", addr, err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func newTestManager(t *testing.T, rdb *redis.Client) *Manager {
	t.Helper()
	prefix := fmt.Sprintf("buffgo:pool:test:%d:", time.Now().UnixNano())
	m, err := NewManager(rdb, Options{
		LeaseTTL:        5 * time.Second,
		DefaultCooldown: 3 * time.Second,
		KeyPrefix:       prefix,
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
	return m
}

func TestAcquireRelease_RoundTrip(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	lease, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "1.2.3.4:8080",
		Account:  "acc-buff-1",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if lease.ID == "" || lease.WorkerID != "w1" || lease.Platform != "buff" || lease.AppID != 252490 {
		t.Fatalf("lease fields: %+v", lease)
	}
	if lease.Exp.Before(time.Now()) {
		t.Fatalf("exp not in future: %v", lease.Exp)
	}

	got, err := m.Get(ctx, lease.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Proxy != lease.Proxy || got.Account != lease.Account {
		t.Fatalf("get mismatch: %+v", got)
	}

	// source lease mapping
	sl := lease.ToSourceLease()
	if sl.Proxy != lease.Proxy || sl.Account != lease.Account || sl.AppID != 252490 {
		t.Fatalf("ToSourceLease: %+v", sl)
	}

	if err := m.Release(ctx, lease.ID, "w1", ReleaseOptions{}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := m.Get(ctx, lease.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found after release, got %v", err)
	}
}

func TestMultiWorker_NoDoubleLeaseSameProxyPlatform(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	l1, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-a",
		Proxy:    "proxy-1",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("worker-a: %v", err)
	}
	defer m.Release(ctx, l1.ID, "worker-a", ReleaseOptions{})

	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID: "worker-b",
		Proxy:    "proxy-1",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
}

func TestMultiWorker_ConcurrentAcquire(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	const n = 20
	var okCount atomic.Int64
	var busyCount atomic.Int64
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners []string

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lease, err := m.Acquire(ctx, AcquireRequest{
				WorkerID: fmt.Sprintf("w-%d", i),
				Proxy:    "shared-proxy",
				Platform: "steam",
				LineType: "dual",
				AppID:    252490,
			})
			if err == nil {
				okCount.Add(1)
				mu.Lock()
				winners = append(winners, lease.ID)
				mu.Unlock()
				return
			}
			if errors.Is(err, ErrBusy) {
				busyCount.Add(1)
				return
			}
			t.Errorf("unexpected: %v", err)
		}(i)
	}
	wg.Wait()

	if okCount.Load() != 1 {
		t.Fatalf("expected exactly 1 winner, got %d (busy=%d) winners=%v", okCount.Load(), busyCount.Load(), winners)
	}
	if busyCount.Load() != n-1 {
		t.Fatalf("expected %d busy, got %d", n-1, busyCount.Load())
	}
	// cleanup
	for _, id := range winners {
		_ = m.Release(ctx, id, "", ReleaseOptions{}) // may fail owner check — get then release
		if l, err := m.Get(ctx, id); err == nil {
			_ = m.Release(ctx, id, l.WorkerID, ReleaseOptions{})
		}
	}
}

func TestAccount_NoDoubleLease(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	l1, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "p1",
		Account:  "cookie-session-x",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Release(ctx, l1.ID, "w1", ReleaseOptions{})

	// different proxy, same account → busy
	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID: "w2",
		Proxy:    "p2",
		Account:  "cookie-session-x",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy for account, got %v", err)
	}
}

func TestCooldown_IndependentPerPlatform(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	// Buff lease then release with cooldown
	lBuff, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "same-ip",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Release(ctx, lBuff.ID, "w1", ReleaseOptions{
		SetCooldown: true,
		Cooldown:    10 * time.Second,
	}); err != nil {
		t.Fatal(err)
	}

	cooling, err := m.IsCooling(ctx, "same-ip", "buff")
	if err != nil || !cooling {
		t.Fatalf("buff should be cooling: cooling=%v err=%v", cooling, err)
	}
	// re-acquire buff → cooling
	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID: "w2",
		Proxy:    "same-ip",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if !errors.Is(err, ErrCooling) {
		t.Fatalf("expected ErrCooling, got %v", err)
	}

	// Steam on same IP must still work (cross-platform reuse)
	lSteam, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w3",
		Proxy:    "same-ip",
		Platform: "steam",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("steam acquire during buff cooldown: %v", err)
	}
	if err := m.Release(ctx, lSteam.ID, "w3", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}

	// steam not cooling
	steamCooling, err := m.IsCooling(ctx, "same-ip", "steam")
	if err != nil || steamCooling {
		t.Fatalf("steam should not be cooling: %v %v", steamCooling, err)
	}
}

func TestRenew_ExtendsTTLAndOwnerCheck(t *testing.T) {
	rdb := testRedis(t)
	// short TTL for renew proof
	prefix := fmt.Sprintf("buffgo:pool:test:%d:", time.Now().UnixNano())
	m, err := NewManager(rdb, Options{
		LeaseTTL:        2 * time.Second,
		DefaultCooldown: time.Second,
		KeyPrefix:       prefix,
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
	lease, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "owner",
		Proxy:    "p-renew",
		Account:  "acc-renew",
		Platform: "steam",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = m.Renew(ctx, lease.ID, "other")
	if !errors.Is(err, ErrNotOwner) {
		t.Fatalf("expected not owner, got %v", err)
	}

	// wait less than TTL, renew, then wait past original TTL — lease still present
	time.Sleep(800 * time.Millisecond)
	renewed, err := m.Renew(ctx, lease.ID, "owner")
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !renewed.Exp.After(lease.Exp) {
		t.Fatalf("exp not extended: old=%v new=%v", lease.Exp, renewed.Exp)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := m.Get(ctx, lease.ID); err != nil {
		t.Fatalf("lease should still exist after renew: %v", err)
	}

	if err := m.Release(ctx, lease.ID, "owner", ReleaseOptions{SetCooldown: true}); err != nil {
		t.Fatal(err)
	}
}

func TestTTL_CrashSafety(t *testing.T) {
	rdb := testRedis(t)
	prefix := fmt.Sprintf("buffgo:pool:test:%d:", time.Now().UnixNano())
	m, err := NewManager(rdb, Options{
		LeaseTTL:        time.Second,
		DefaultCooldown: time.Second,
		KeyPrefix:       prefix,
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
	lease, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "dead-worker",
		Proxy:    "p-ttl",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatal(err)
	}

	// simulate crash: no Release; wait for TTL
	time.Sleep(1200 * time.Millisecond)
	if _, err := m.Get(ctx, lease.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected expired lease gone, got %v", err)
	}
	// resource free for another worker
	l2, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "new-worker",
		Proxy:    "p-ttl",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("re-acquire after TTL: %v", err)
	}
	_ = m.Release(ctx, l2.ID, "new-worker", ReleaseOptions{})
}

func TestRelease_NotOwner(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	lease, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "p",
		Platform: "buff",
		LineType: "dual",
		AppID:    730,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Release(ctx, lease.ID, "w2", ReleaseOptions{}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("expected not owner, got %v", err)
	}
	if err := m.Release(ctx, lease.ID, "w1", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestAcquire_Validation(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	_, err := m.Acquire(context.Background(), AcquireRequest{})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid, got %v", err)
	}
}

func TestNewManager_RequiresClient(t *testing.T) {
	_, err := NewManager(nil, Options{})
	if err == nil {
		t.Fatal("expected error")
	}
}
