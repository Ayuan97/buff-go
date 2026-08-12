package pool

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestLineAllowed_Defaults(t *testing.T) {
	cases := []struct {
		platform, line string
		want           bool
	}{
		{"buff", "cn", true},
		{"buff", "dual", true},
		{"buff", "hk", true},
		{"buff", "oversea", false},
		{"steam", "oversea", true},
		{"steam", "dual", true},
		{"steam", "hk", true},
		{"steam", "cn", false},
		{"BUFF", "CN", true},
		{"steam", "", false},
		{"steam", "weird", false},
		{"unknown", "dual", false},
	}
	for _, tc := range cases {
		got := LineAllowed(tc.platform, tc.line, nil)
		if got != tc.want {
			t.Errorf("LineAllowed(%q,%q,nil)=%v want %v", tc.platform, tc.line, got, tc.want)
		}
	}
}

func TestLineAllowed_CustomPrefer(t *testing.T) {
	prefer := map[string][]string{
		"buff":  {"cn"},
		"steam": {"oversea", "dual"},
	}
	if LineAllowed("buff", "hk", prefer) {
		t.Fatal("hk should not match custom buff prefer")
	}
	if !LineAllowed("buff", "cn", prefer) {
		t.Fatal("cn should match")
	}
	if LineAllowed("steam", "hk", prefer) {
		t.Fatal("hk removed from steam prefer")
	}
	if !LineAllowed("steam", "dual", prefer) {
		t.Fatal("dual still allowed")
	}
}

func TestPreferLinesFor_MergeAndDefaults(t *testing.T) {
	def := PreferLinesFor("buff", nil)
	if len(def) != 3 || def[0] != LineCN {
		t.Fatalf("buff default: %v", def)
	}
	custom := PreferLinesFor("buff", map[string][]string{"buff": {"cn", "cn", "??", "hk"}})
	if len(custom) != 2 || custom[0] != "cn" || custom[1] != "hk" {
		t.Fatalf("dedupe/filter: %v", custom)
	}
}

func TestMergePlatformLines_Overlay(t *testing.T) {
	m := MergePlatformLines(map[string][]string{
		"buff":  {"cn"},
		"other": {"dual"},
	})
	if len(m["buff"]) != 1 || m["buff"][0] != "cn" {
		t.Fatalf("buff overlay: %v", m["buff"])
	}
	if len(m["steam"]) != 3 {
		t.Fatalf("steam default preserved: %v", m["steam"])
	}
	if len(m["other"]) != 1 || m["other"][0] != "dual" {
		t.Fatalf("other: %v", m["other"])
	}
}

func TestAcquire_LineMismatch_BuffOversea(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	_, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "p-oversea",
		Platform: "buff",
		LineType: LineOversea,
		AppID:    252490,
	})
	if !errors.Is(err, ErrLineMismatch) {
		t.Fatalf("expected ErrLineMismatch, got %v", err)
	}
}

func TestAcquire_LineMismatch_SteamCN(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	_, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "p-cn",
		Platform: "steam",
		LineType: LineCN,
		AppID:    252490,
	})
	if !errors.Is(err, ErrLineMismatch) {
		t.Fatalf("expected ErrLineMismatch, got %v", err)
	}
}

func TestAcquire_LineMatch_Defaults(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	ctx := context.Background()

	cases := []struct {
		platform, line, proxy string
	}{
		{"buff", LineCN, "p1"},
		{"buff", LineHK, "p2"},
		{"buff", LineDual, "p3"},
		{"steam", LineOversea, "p4"},
		{"steam", LineHK, "p5"},
		{"steam", LineDual, "p6"},
	}
	for _, tc := range cases {
		lease, err := m.Acquire(ctx, AcquireRequest{
			WorkerID: "w-" + tc.proxy,
			Proxy:    tc.proxy,
			Platform: tc.platform,
			LineType: tc.line,
			AppID:    252490,
		})
		if err != nil {
			t.Fatalf("acquire %s/%s: %v", tc.platform, tc.line, err)
		}
		if lease.LineType != tc.line {
			t.Fatalf("lease.LineType=%q want %q", lease.LineType, tc.line)
		}
		if err := m.Release(ctx, lease.ID, "w-"+tc.proxy, ReleaseOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAcquire_CustomPlatformLines_OnlyCNForBuff(t *testing.T) {
	rdb := testRedis(t)
	prefix := fmt.Sprintf("buffgo:pool:test:linecfg:%d:", time.Now().UnixNano())
	m, err := NewManager(rdb, Options{
		LeaseTTL:        5 * time.Second,
		DefaultCooldown: 2 * time.Second,
		KeyPrefix:       prefix,
		PlatformLines:   map[string][]string{"buff": {"cn"}},
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

	// dual no longer allowed for buff under custom config
	_, err = m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "p-dual",
		Platform: "buff",
		LineType: LineDual,
		AppID:    252490,
	})
	if !errors.Is(err, ErrLineMismatch) {
		t.Fatalf("dual should mismatch custom buff→cn only: %v", err)
	}

	lease, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w1",
		Proxy:    "p-cn",
		Platform: "buff",
		LineType: LineCN,
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("cn should acquire: %v", err)
	}
	_ = m.Release(ctx, lease.ID, "w1", ReleaseOptions{})

	// steam defaults preserved under overlay merge
	lease2, err := m.Acquire(ctx, AcquireRequest{
		WorkerID: "w2",
		Proxy:    "p-steam",
		Platform: "steam",
		LineType: LineOversea,
		AppID:    252490,
	})
	if err != nil {
		t.Fatalf("steam default still works: %v", err)
	}
	_ = m.Release(ctx, lease2.ID, "w2", ReleaseOptions{})
}

func TestManager_AllowsLine(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	if !m.AllowsLine("buff", "cn") || m.AllowsLine("buff", "oversea") {
		t.Fatal("AllowsLine defaults wrong")
	}
	if !m.AllowsLine("steam", "oversea") || m.AllowsLine("steam", "cn") {
		t.Fatal("AllowsLine steam defaults wrong")
	}
	lines := m.PlatformLines()
	if len(lines["buff"]) != 3 || len(lines["steam"]) != 3 {
		t.Fatalf("PlatformLines: %v", lines)
	}
}

func TestAcquire_InvalidLineType(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	_, err := m.Acquire(context.Background(), AcquireRequest{
		WorkerID: "w",
		Proxy:    "p",
		Platform: "buff",
		LineType: "galaxy",
		AppID:    252490,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid, got %v", err)
	}
}

func TestAcquire_MissingLineType(t *testing.T) {
	rdb := testRedis(t)
	m := newTestManager(t, rdb)
	_, err := m.Acquire(context.Background(), AcquireRequest{
		WorkerID: "w",
		Proxy:    "p",
		Platform: "buff",
		AppID:    252490,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid for missing line_type, got %v", err)
	}
}

func TestValidLineType(t *testing.T) {
	for _, l := range KnownLineTypes {
		if !ValidLineType(l) {
			t.Fatalf("expected valid %q", l)
		}
	}
	if ValidLineType("local") {
		t.Fatal("local is not a product line_type")
	}
}
