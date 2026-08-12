package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"buff-go/internal/buffgo/migrate"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// integrationDSN returns DATABASE_URL / BUFFGO_TEST_DSN when set.
func integrationDSN() string {
	if v := os.Getenv("BUFFGO_TEST_DSN"); v != "" {
		return v
	}
	return os.Getenv("DATABASE_URL")
}

func TestStore_UpsertItems_Integration(t *testing.T) {
	dsn := integrationDSN()
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN or DATABASE_URL for PostgreSQL integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := migrate.Apply(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store, db, err := OpenStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const appid int64 = 252490
	if err := store.EnsureGame(ctx, KnownGame(appid)); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Metal Facemask", "Road Sign Kilt", "AK47"} {
		_, _ = db.ExecContext(ctx, `DELETE FROM items WHERE appid = $1 AND market_hash_name = $2`, appid, name)
	}

	root := findRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "testdata", "rust_catalog_sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	items, gotApp, err := ParseImportJSON(data, 0)
	if err != nil || gotApp != appid {
		t.Fatalf("import parse: appid=%d err=%v", gotApp, err)
	}

	subset := items[:3]
	up, err := store.UpsertItems(ctx, subset)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if up.Written != 3 {
		t.Fatalf("written: %d", up.Written)
	}

	// Re-upsert same keys (idempotent unique key)
	up2, err := store.UpsertItems(ctx, subset)
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if up2.Written != 3 {
		t.Fatalf("re-written: %d", up2.Written)
	}

	n, err := store.CountByAppID(ctx, appid)
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Fatalf("count appid=%d: %d want >=3", appid, n)
	}

	// Direct lookup: ListByAppID is ordered/limited and shared test DBs may hold many rows.
	var cnt int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE appid = $1 AND market_hash_name = $2`,
		appid, "Metal Facemask",
	).Scan(&cnt)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("Metal Facemask unique key: count=%d want 1", cnt)
	}

	list, err := store.ListByAppID(ctx, appid, 500)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range list {
		if it.MarketHashName == "Metal Facemask" && it.AppID == appid {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Metal Facemask not visible via ListByAppID for appid=%d (n=%d)", appid, len(list))
	}
}

func TestStore_ListMissingAndUpdateSteamNameID_Integration(t *testing.T) {
	dsn := integrationDSN()
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN or DATABASE_URL for PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrate.Apply(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, db, err := OpenStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const appid int64 = 252490
	if err := store.EnsureGame(ctx, KnownGame(appid)); err != nil {
		t.Fatal(err)
	}
	// Unique names for this test so shared DBs don't collide with other suites.
	hashMissing := "buffgo_nameid_test_missing"
	hashHas := "buffgo_nameid_test_has"
	for _, name := range []string{hashMissing, hashHas} {
		_, _ = db.ExecContext(ctx, `DELETE FROM items WHERE appid = $1 AND market_hash_name = $2`, appid, name)
	}
	if _, err := store.UpsertItems(ctx, []Item{
		{AppID: appid, MarketHashName: hashMissing, Name: hashMissing, SteamItemNameID: ""},
		{AppID: appid, MarketHashName: hashHas, Name: hashHas, SteamItemNameID: "555"},
	}); err != nil {
		t.Fatal(err)
	}

	missing, err := store.ListMissingSteamNameID(ctx, appid, 50)
	if err != nil {
		t.Fatal(err)
	}
	foundMissing := false
	for _, it := range missing {
		if it.MarketHashName == hashMissing {
			foundMissing = true
		}
		if it.MarketHashName == hashHas {
			t.Fatalf("item with nameid should not be listed missing: %+v", it)
		}
	}
	if !foundMissing {
		t.Fatalf("expected %s in missing list (n=%d)", hashMissing, len(missing))
	}

	if err := store.UpdateSteamNameID(ctx, appid, hashMissing, "777888"); err != nil {
		t.Fatal(err)
	}
	missing2, err := store.ListMissingSteamNameID(ctx, appid, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range missing2 {
		if it.MarketHashName == hashMissing {
			t.Fatal("updated item still listed as missing")
		}
	}

	// Empty nameid rejected.
	if err := store.UpdateSteamNameID(ctx, appid, hashMissing, "  "); err == nil {
		t.Fatal("expected empty nameid error")
	}
}

func TestService_ImportFile_Integration(t *testing.T) {
	dsn := integrationDSN()
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN or DATABASE_URL for PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrate.Apply(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, db, err := OpenStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	svc := NewService(store, nil)
	root := findRepoRoot(t)
	path := filepath.Join(root, "testdata", "rust_catalog_sample.json")
	res, err := svc.ImportFile(ctx, path, 252490)
	if err != nil {
		t.Fatal(err)
	}
	if res.AppID != 252490 || res.Count < 8 || res.Upsert.Written < 8 {
		t.Fatalf("import result: %+v", res)
	}
	if len(res.Sample) == 0 {
		t.Fatal("expected sample rows")
	}
}
