package postgres

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"buff-go/internal/resource"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestProviderStorageIntegration(t *testing.T) {
	dsn := os.Getenv("BUFFGO_TEST_DSN")
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN to an isolated PostgreSQL database")
	}

	store, db := migratedStore(t, dsn)
	t.Run("providers", func(t *testing.T) { testProviderLifecycle(t, dsn, store, db) })
	t.Run("watermarks", func(t *testing.T) { testProviderWatermarks(t, store) })
}

func testProviderLifecycle(t *testing.T, dsn string, store *Store, db *sql.DB) {
	ctx := t.Context()
	firstSecret := []byte("provider-first-credential-marker")
	secondSecret := []byte("provider-second-credential-marker")
	secondarySecret := []byte("provider-secondary-credential-marker")

	primary, err := store.CreateProvider(ctx, "primary-provider", true, 20, []resource.NodeRegion{
		resource.NodeRegionForeign,
		resource.NodeRegionHongKong,
	}, firstSecret)
	if err != nil {
		t.Fatal(err)
	}
	if primary.Revision != 1 || !primary.Enabled || primary.Priority != 20 || !primary.HasCredential {
		t.Fatalf("created provider = %+v", primary)
	}
	assertProviderRegions(t, primary, resource.NodeRegionForeign, resource.NodeRegionHongKong)
	assertProviderDoesNotExpose(t, primary, firstSecret)
	assertStoredProviderCredential(t, db, primary.ID, firstSecret)

	secondary, err := store.CreateProvider(ctx, "secondary-provider", true, 10, []resource.NodeRegion{
		resource.NodeRegionDomestic,
	}, secondarySecret)
	if err != nil {
		t.Fatal(err)
	}
	providers, err := store.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].ID != secondary.ID || providers[1].ID != primary.ID {
		t.Fatalf("provider priority order = %+v", providers)
	}
	for _, provider := range providers {
		assertProviderDoesNotExpose(t, provider, firstSecret, secondSecret, secondarySecret)
	}

	duplicateSecret := []byte("provider-duplicate-credential-marker")
	_, err = store.CreateProvider(ctx, primary.Name, true, 30, []resource.NodeRegion{
		resource.NodeRegionForeign,
	}, duplicateSecret)
	if err == nil {
		t.Fatal("duplicate provider creation succeeded")
	}
	if strings.Contains(err.Error(), string(duplicateSecret)) {
		t.Fatal("duplicate provider error exposed credential material")
	}
	if !errors.Is(err, ErrProviderConflict) {
		t.Fatalf("duplicate provider error = %v", err)
	}

	restarted, restartedDB := reopenProviderStore(t, dsn, db)
	restartedProviders, err := restarted.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(restartedProviders) != 2 || restartedProviders[0].ID != secondary.ID || restartedProviders[1].ID != primary.ID {
		t.Fatalf("providers after restart = %+v", restartedProviders)
	}
	assertStoredProviderCredential(t, restartedDB, primary.ID, firstSecret)
	for _, provider := range restartedProviders {
		assertProviderDoesNotExpose(t, provider, firstSecret, secondSecret, secondarySecret, duplicateSecret)
	}

	primary, err = store.UpdateProvider(ctx, primary.ID, primary.Revision, "primary-renamed", false, 5, []resource.NodeRegion{
		resource.NodeRegionDomestic,
		resource.NodeRegionForeign,
	})
	if err != nil {
		t.Fatal(err)
	}
	if primary.Revision != 2 || primary.Name != "primary-renamed" || primary.Enabled || primary.Priority != 5 || !primary.HasCredential {
		t.Fatalf("updated provider = %+v", primary)
	}
	assertProviderRegions(t, primary, resource.NodeRegionDomestic, resource.NodeRegionForeign)
	assertProviderDoesNotExpose(t, primary, firstSecret, secondarySecret)
	assertStoredProviderCredential(t, db, primary.ID, firstSecret)
	if _, err := store.UpdateProvider(ctx, primary.ID, 1, "stale-update", true, 1, []resource.NodeRegion{
		resource.NodeRegionDomestic,
	}); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("stale provider update error = %v", err)
	}

	primary, err = store.ReplaceProviderCredential(ctx, primary.ID, primary.Revision, secondSecret)
	if err != nil {
		t.Fatal(err)
	}
	if primary.Revision != 3 || !primary.HasCredential {
		t.Fatalf("provider after credential replacement = %+v", primary)
	}
	assertProviderDoesNotExpose(t, primary, firstSecret, secondSecret)
	assertStoredProviderCredential(t, db, primary.ID, secondSecret)
	if _, err := store.ReplaceProviderCredential(ctx, primary.ID, 2, []byte("provider-stale-credential-marker")); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("stale credential replacement error = %v", err)
	}
	assertStoredProviderCredential(t, db, primary.ID, secondSecret)

	restarted, _ = reopenProviderStore(t, dsn, db)
	providers, err = restarted.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].ID != primary.ID || providers[0].Revision != 3 {
		t.Fatalf("updated providers after restart = %+v", providers)
	}
	assertProviderRegions(t, providers[0], resource.NodeRegionDomestic, resource.NodeRegionForeign)
	assertProviderDoesNotExpose(t, providers[0], firstSecret, secondSecret)

	if err := store.DeleteProvider(ctx, primary.ID); err != nil {
		t.Fatal(err)
	}
	var regionCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxy_provider_regions WHERE provider_id = $1`, int64(primary.ID)).Scan(&regionCount); err != nil {
		t.Fatal(err)
	}
	if regionCount != 0 {
		t.Fatalf("provider regions after delete = %d", regionCount)
	}
	providers, err = store.ListProviders(ctx)
	if err != nil || len(providers) != 1 || providers[0].ID != secondary.ID {
		t.Fatalf("providers after delete = %+v err=%v", providers, err)
	}
	if err := store.DeleteProvider(ctx, primary.ID); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("second provider delete error = %v", err)
	}
	if _, err := store.UpdateProvider(ctx, primary.ID, primary.Revision, "missing-provider", true, 1, []resource.NodeRegion{
		resource.NodeRegionForeign,
	}); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("missing provider update error = %v", err)
	}
	if _, err := store.ReplaceProviderCredential(ctx, primary.ID, primary.Revision, secondSecret); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("missing provider credential replacement error = %v", err)
	}
}

func testProviderWatermarks(t *testing.T, store *Store) {
	ctx := t.Context()
	marks, err := store.ListWatermarks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantRegions := []resource.NodeRegion{
		resource.NodeRegionDomestic,
		resource.NodeRegionForeign,
		resource.NodeRegionHongKong,
	}
	if len(marks) != len(wantRegions) {
		t.Fatalf("watermark count = %d, want %d", len(marks), len(wantRegions))
	}
	for i, mark := range marks {
		if mark.Region != wantRegions[i] || mark.MinUsable != 0 || mark.Usable != 0 || mark.Revision != 1 {
			t.Fatalf("watermark[%d] = %+v", i, mark)
		}
	}

	foreign, err := store.SetWatermark(ctx, resource.NodeRegionForeign, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if foreign.Region != resource.NodeRegionForeign || foreign.MinUsable != 4 || foreign.Usable != 0 || foreign.Revision != 2 {
		t.Fatalf("updated watermark = %+v", foreign)
	}
	if _, err := store.SetWatermark(ctx, resource.NodeRegionForeign, 1, 5); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("stale watermark revision error = %v", err)
	}
	if _, err := store.SetWatermark(ctx, resource.NodeRegionForeign, 0, 5); !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("invalid watermark revision error = %v", err)
	}
	if _, err := store.SetWatermark(ctx, resource.NodeRegion("invalid-region"), 1, 5); !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("invalid watermark region error = %v", err)
	}
	if _, err := store.SetWatermark(ctx, resource.NodeRegionDomestic, 1, -1); !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("invalid watermark minimum error = %v", err)
	}

	marks, err = store.ListWatermarks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if marks[1] != foreign {
		t.Fatalf("listed foreign watermark = %+v, want %+v", marks[1], foreign)
	}
}

func reopenProviderStore(t *testing.T, dsn string, source *sql.DB) (*Store, *sql.DB) {
	t.Helper()
	var schema string
	if err := source.QueryRowContext(t.Context(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read provider test schema: %v", err)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse provider test DSN: %v", err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	if err := db.PingContext(t.Context()); err != nil {
		_ = db.Close()
		t.Fatalf("reopen provider test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return store, db
}

func assertProviderRegions(t *testing.T, provider resource.ProxyProvider, want ...resource.NodeRegion) {
	t.Helper()
	if fmt.Sprint(provider.Regions) != fmt.Sprint(want) {
		t.Fatalf("provider regions = %v, want %v", provider.Regions, want)
	}
}

func assertProviderDoesNotExpose(t *testing.T, provider resource.ProxyProvider, secrets ...[]byte) {
	t.Helper()
	encoded := fmt.Sprintf("%+v", provider)
	for _, secret := range secrets {
		if len(secret) > 0 && strings.Contains(encoded, string(secret)) {
			t.Fatal("provider read model exposed credential material")
		}
	}
}

func assertStoredProviderCredential(t *testing.T, db *sql.DB, id resource.ProviderID, want []byte) {
	t.Helper()
	var stored []byte
	if err := db.QueryRowContext(t.Context(), `SELECT credential_plaintext FROM proxy_providers WHERE provider_id = $1`, int64(id)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, want) {
		t.Fatalf("stored provider credential does not match written value")
	}
}
