package nameid

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"buff-go/internal/buffgo/catalog"
)

// memStore is an in-memory Store for unit tests (no PostgreSQL).
type memStore struct {
	mu    sync.Mutex
	items map[string]catalog.Item // key: appid\x00hash
}

func newMemStore(items ...catalog.Item) *memStore {
	m := &memStore{items: make(map[string]catalog.Item)}
	for _, it := range items {
		_ = it.Normalize()
		m.items[memKey(it.AppID, it.MarketHashName)] = it
	}
	return m
}

func memKey(appid int64, hash string) string {
	return fmt.Sprintf("%d\x00%s", appid, hash)
}

func (m *memStore) ListMissingSteamNameID(ctx context.Context, appid int64, limit int) ([]catalog.Item, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []catalog.Item
	for _, it := range m.items {
		if it.AppID != appid {
			continue
		}
		if strings.TrimSpace(it.SteamItemNameID) != "" {
			continue
		}
		out = append(out, it)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *memStore) UpdateSteamNameID(ctx context.Context, appid int64, marketHashName, nameid string) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	k := memKey(appid, marketHashName)
	it, ok := m.items[k]
	if !ok {
		return fmt.Errorf("item not found: %s@%d", marketHashName, appid)
	}
	it.SteamItemNameID = strings.TrimSpace(nameid)
	m.items[k] = it
	return nil
}

func (m *memStore) get(appid int64, hash string) catalog.Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items[memKey(appid, hash)]
}

type fakeResolver struct {
	// map market_hash_name → nameid; missing or value "ERR" → error
	ids map[string]string
	// calls records resolved hashes in order
	calls []string
}

func (f *fakeResolver) ResolveItemNameID(ctx context.Context, appid int64, marketHashName string) (string, error) {
	_ = ctx
	_ = appid
	f.calls = append(f.calls, marketHashName)
	v, ok := f.ids[marketHashName]
	if !ok {
		return "", fmt.Errorf("not found: %s", marketHashName)
	}
	if v == "ERR" {
		return "", fmt.Errorf("resolve fail: %s", marketHashName)
	}
	return v, nil
}

func TestBackfill_UpdatesMissing(t *testing.T) {
	const appid int64 = 252490
	st := newMemStore(
		catalog.Item{AppID: appid, MarketHashName: "Metal Facemask", SteamItemNameID: ""},
		catalog.Item{AppID: appid, MarketHashName: "Road Sign Kilt", SteamItemNameID: "111"},
		catalog.Item{AppID: appid, MarketHashName: "AK47", SteamItemNameID: ""},
	)
	res := &fakeResolver{ids: map[string]string{
		"Metal Facemask": "999001",
		"AK47":           "999002",
	}}

	stats, err := Backfill(context.Background(), st, res, Options{
		AppID:     appid,
		Limit:     10,
		PageDelay: time.Millisecond, // fast test
		Quiet:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Attempted != 2 || stats.Updated != 2 || stats.Failed != 0 {
		t.Fatalf("stats: %+v want attempted=2 updated=2 failed=0", stats)
	}
	if got := st.get(appid, "Metal Facemask").SteamItemNameID; got != "999001" {
		t.Fatalf("Metal Facemask nameid: %q", got)
	}
	if got := st.get(appid, "AK47").SteamItemNameID; got != "999002" {
		t.Fatalf("AK47 nameid: %q", got)
	}
	// Already had nameid — not listed, not overwritten path.
	if got := st.get(appid, "Road Sign Kilt").SteamItemNameID; got != "111" {
		t.Fatalf("existing nameid wiped: %q", got)
	}
}

func TestBackfill_SkipsFailuresContinues(t *testing.T) {
	const appid int64 = 730
	st := newMemStore(
		catalog.Item{AppID: appid, MarketHashName: "A", SteamItemNameID: ""},
		catalog.Item{AppID: appid, MarketHashName: "B", SteamItemNameID: ""},
		catalog.Item{AppID: appid, MarketHashName: "C", SteamItemNameID: ""},
	)
	res := &fakeResolver{ids: map[string]string{
		"A": "ERR",
		"B": "42",
		// C missing → error
	}}

	stats, err := Backfill(context.Background(), st, res, Options{
		AppID:     appid,
		Limit:     10,
		PageDelay: time.Millisecond,
		Quiet:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Attempted != 3 {
		t.Fatalf("attempted: %d", stats.Attempted)
	}
	if stats.Updated != 1 || stats.Failed != 2 {
		t.Fatalf("stats: %+v", stats)
	}
	if got := st.get(appid, "B").SteamItemNameID; got != "42" {
		t.Fatalf("B: %q", got)
	}
	if got := st.get(appid, "A").SteamItemNameID; got != "" {
		t.Fatalf("A should remain empty: %q", got)
	}
}

func TestBackfill_RespectsLimit(t *testing.T) {
	const appid int64 = 252490
	items := make([]catalog.Item, 0, 5)
	ids := make(map[string]string, 5)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("Item-%d", i)
		items = append(items, catalog.Item{AppID: appid, MarketHashName: name})
		ids[name] = fmt.Sprintf("%d", 1000+i)
	}
	st := newMemStore(items...)
	res := &fakeResolver{ids: ids}

	stats, err := Backfill(context.Background(), st, res, Options{
		AppID:     appid,
		Limit:     2,
		PageDelay: time.Millisecond,
		Quiet:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Attempted != 2 || stats.Updated != 2 {
		t.Fatalf("stats: %+v", stats)
	}
	if len(res.calls) != 2 {
		t.Fatalf("resolver calls: %d", len(res.calls))
	}
}

func TestBackfill_NilDeps(t *testing.T) {
	_, err := Backfill(context.Background(), nil, &fakeResolver{}, Options{AppID: 1})
	if err == nil {
		t.Fatal("expected nil store error")
	}
	_, err = Backfill(context.Background(), newMemStore(), nil, Options{AppID: 1})
	if err == nil {
		t.Fatal("expected nil resolver error")
	}
	_, err = Backfill(context.Background(), newMemStore(), &fakeResolver{}, Options{})
	if err == nil {
		t.Fatal("expected appid error")
	}
}

func TestLimitFromEnv(t *testing.T) {
	t.Setenv(EnvBackfillLimit, "")
	if LimitFromEnv() != 0 {
		t.Fatal("empty → 0")
	}
	t.Setenv(EnvBackfillLimit, "5")
	if LimitFromEnv() != 5 {
		t.Fatal("want 5")
	}
	t.Setenv(EnvBackfillLimit, "nope")
	if LimitFromEnv() != 0 {
		t.Fatal("invalid → 0")
	}
	t.Setenv(EnvBackfillLimit, "-1")
	if LimitFromEnv() != 0 {
		t.Fatal("negative → 0")
	}
}

func TestDelayFromEnv(t *testing.T) {
	t.Setenv(EnvBackfillDelay, "")
	if DelayFromEnv() != defaultPageDelay {
		t.Fatalf("default: %v", DelayFromEnv())
	}
	t.Setenv(EnvBackfillDelay, "500ms")
	if DelayFromEnv() != 500*time.Millisecond {
		t.Fatalf("got %v", DelayFromEnv())
	}
}
