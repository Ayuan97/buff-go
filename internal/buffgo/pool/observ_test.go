package pool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"buff-go/internal/telemetry"
)

// TestP53_LeaseRateLimitObservability proves Acquire/Release emit structured
// metrics for lease ok/conflict, platform cooling, and appid quota (P5.3).
func TestP53_LeaseRateLimitObservability(t *testing.T) {
	rdb := testRedis(t)
	mem := telemetry.NewMemory(128)
	prefix := fmt.Sprintf("buffgo:pool:obs:%d:", time.Now().UnixNano())
	// Phase A: no quota — exercise busy / cool / line / cross-platform.
	m, err := NewManager(rdb, Options{
		LeaseTTL:        5 * time.Second,
		DefaultCooldown: 3 * time.Second,
		KeyPrefix:       prefix,
		Recorder:        mem,
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

	// 1) successful acquire
	l1, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "proxy-a",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("acquire1: %v", err)
	}

	// 2) conflict: same proxy+platform busy (quota not set so busy is visible)
	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID: "w2",
		Proxy:    "proxy-a",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("want busy, got %v", err)
	}

	// 3) release with cooldown
	if err := m.Release(ctx, l1.ID, "w1", ReleaseOptions{SetCooldown: true}); err != nil {
		t.Fatalf("release: %v", err)
	}

	// 4) cooling hit on same proxy+platform
	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "proxy-a",
		Platform: "buff",
		LineType: "dual",
		AppID:    252490,
	})
	if !errors.Is(err, ErrCooling) {
		t.Fatalf("want cooling, got %v", err)
	}

	// 5) line mismatch conflict (no redis rent)
	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "proxy-c",
		Platform: "buff",
		LineType: "oversea", // buff prefers cn/dual/hk
		AppID:    252490,
	})
	if !errors.Is(err, ErrLineMismatch) {
		t.Fatalf("want line mismatch, got %v", err)
	}

	// 6) steam same IP while buff cooling — acquire ok (cross-platform)
	lSteam, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "proxy-a",
		Platform: "steam",
		LineType: "dual",
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("steam acquire: %v", err)
	}
	_ = m.Release(ctx, lSteam.ID, "w1", ReleaseOptions{})

	// Phase B: quota-limited manager on a clean appid path (max=1).
	mq, err := NewManager(rdb, Options{
		LeaseTTL:       5 * time.Second,
		KeyPrefix:      prefix,
		MaxProxyLeases: map[int64]int{730: 1},
		Recorder:       mem,
	})
	if err != nil {
		t.Fatal(err)
	}
	lQ, err := mq.Acquire(ctx, AcquireRequest{
		WorkerID: "w-q1",
		Proxy:    "proxy-q1",
		Platform: "steam",
		LineType: "oversea",
		AppID:    730,
	})
	if err != nil {
		t.Fatalf("quota holder: %v", err)
	}
	_, err = mq.Acquire(ctx, AcquireRequest{
		WorkerID: "w-q2",
		Proxy:    "proxy-q2",
		Platform: "steam",
		LineType: "oversea",
		AppID:    730,
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("want quota, got %v", err)
	}
	_ = mq.Release(ctx, lQ.ID, "w-q1", ReleaseOptions{})

	snap := mem.Snapshot()
	// acquires: l1 + lSteam + lQ = 3
	if snap.LeaseAcquire != 3 {
		t.Fatalf("lease_acquire=%d want 3; events:\n%s", snap.LeaseAcquire, mem.FormatSnapshot())
	}
	// releases: l1 (cooldown) + lSteam + lQ = 3
	if snap.LeaseRelease != 3 {
		t.Fatalf("lease_release=%d want 3", snap.LeaseRelease)
	}
	// busy + line_mismatch = 2 conflicts (quota is rate_limit)
	if snap.LeaseConflict < 2 {
		t.Fatalf("lease_conflict=%d want >=2", snap.LeaseConflict)
	}
	// cooling + quota
	if snap.RateLimitHit != 2 {
		t.Fatalf("rate_limit=%d want 2 (cooling+quota); %s", snap.RateLimitHit, mem.FormatSnapshot())
	}
	if mem.Count(telemetry.KindRateLimit, telemetry.ReasonCooling) != 1 {
		t.Fatalf("cooling events=%d", mem.Count(telemetry.KindRateLimit, telemetry.ReasonCooling))
	}
	if mem.Count(telemetry.KindRateLimit, telemetry.ReasonQuota) != 1 {
		t.Fatalf("quota events=%d", mem.Count(telemetry.KindRateLimit, telemetry.ReasonQuota))
	}
	if mem.Count(telemetry.KindLeaseRelease, telemetry.ReasonCooldownSet) != 1 {
		t.Fatalf("cooldown_set releases=%d", mem.Count(telemetry.KindLeaseRelease, telemetry.ReasonCooldownSet))
	}

	// labeled platform visibility
	if mem.CountLabeled(telemetry.KindLeaseAcquire, telemetry.ReasonOK, "buff", "", 252490) != 1 {
		t.Fatalf("buff acquire label missing")
	}
	if mem.CountLabeled(telemetry.KindLeaseAcquire, telemetry.ReasonOK, "steam", "", 252490) != 1 {
		t.Fatalf("steam acquire label missing")
	}
}

// TestP53_ManagerUsesDefaultRecorder when Options.Recorder is nil.
func TestP53_ManagerUsesDefaultRecorder(t *testing.T) {
	rdb := testRedis(t)
	prev := telemetry.Default()
	mem := telemetry.NewMemory(32)
	telemetry.SetDefault(mem)
	t.Cleanup(func() { telemetry.SetDefault(prev) })

	m := newTestManager(t, rdb)
	// newTestManager does not set Recorder → falls back to Default
	if m.Recorder() != nil {
		t.Fatal("expected nil explicit recorder")
	}
	ctx := context.Background()
	l, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w-def",
		Proxy:    "px",
		Platform: "steam",
		LineType: "oversea",
		AppID:    730,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = m.Release(ctx, l.ID, "w-def", ReleaseOptions{})
	if mem.Snapshot().LeaseAcquire != 1 || mem.Snapshot().LeaseRelease != 1 {
		t.Fatalf("default recorder not used: %+v", mem.Snapshot())
	}
}

func TestManagerTelemetryRedactsLegacyAccountAndProxy(t *testing.T) {
	rdb := testRedis(t)
	mem := telemetry.NewMemory(8)
	prefix := fmt.Sprintf("buffgo:pool:redact:%d:", time.Now().UnixNano())
	m, err := NewManager(rdb, Options{LeaseTTL: 5 * time.Second, KeyPrefix: prefix, Recorder: mem})
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

	const (
		worker  = "host-worker-123"
		proxy   = "http://proxy-user:proxy-pass@10.0.0.9:8080"
		account = "sessionid=account-secret; steamLoginSecure=login-secret"
	)
	lease, err := m.Acquire(context.Background(), AcquireRequest{
		WorkerID: worker, Proxy: proxy, Account: account, Platform: "steam", LineType: "oversea", AppID: 730,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Release(context.Background(), lease.ID, worker, ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}

	events := mem.Events()
	if len(events) != 2 {
		t.Fatalf("events=%d want 2", len(events))
	}
	for _, event := range events {
		serialized := event.WorkerID + event.Proxy + event.Account + event.LeaseID + event.Detail
		for _, secret := range []string{worker, proxy, account, "proxy-pass", "account-secret", "login-secret", lease.ID} {
			if strings.Contains(serialized, secret) {
				t.Fatalf("manager telemetry leaked %q: %+v", secret, event)
			}
		}
		if event.WorkerID != telemetry.SafeWorkerRef(worker) || event.Proxy != telemetry.SafeNodeRef(proxy) ||
			event.Account != telemetry.SafeAccountRef(account) || event.LeaseID != telemetry.SafeLeaseRef(lease.ID) {
			t.Fatalf("manager telemetry lost correlation: %+v", event)
		}
	}
}
