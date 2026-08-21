package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/market"
	"buff-go/internal/resource"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

var testSchemaSequence atomic.Uint64

func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("BUFFGO_TEST_DSN")
	if dsn == "" {
		t.Skip("set BUFFGO_TEST_DSN to an isolated PostgreSQL database")
	}

	t.Run("migrations", func(t *testing.T) { testMigrations(t, dsn) })
	t.Run("catalog and mappings", func(t *testing.T) { testCatalogStorage(t, dsn) })
	t.Run("market facts", func(t *testing.T) { testMarketFacts(t, dsn) })
	t.Run("market ordering", func(t *testing.T) { testMarketOrdering(t, dsn) })
	t.Run("batch rollback", func(t *testing.T) { testBatchRollback(t, dsn) })
	t.Run("resources", func(t *testing.T) { testResourceStorage(t, dsn) })
	t.Run("rate-limit DDL", func(t *testing.T) { testRateLimitDDLConstraints(t, dsn) })
	t.Run("rate-limit behavior", func(t *testing.T) { testRateLimitStorage(t, dsn) })
	t.Run("collection DDL", func(t *testing.T) { testCollectionDDLConstraints(t, dsn) })
	t.Run("collection summary pages", func(t *testing.T) { testCollectionSummaryPages(t, dsn) })
}

func testMigrations(t *testing.T, dsn string) {
	db := newTestSchema(t, dsn)
	const workers = 4
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- ApplyMigrations(t.Context(), db)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}
	if err := ApplyMigrations(t.Context(), db); err != nil {
		t.Fatalf("reapply migrations: %v", err)
	}

	rows, err := db.QueryContext(t.Context(), `
SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(tables)
	wantTables := []string{
		"access_nodes",
		"account_node_combinations",
		"buffgo_storage_migrations",
		"collection_daemon_ownership",
		"collection_latest_pages",
		"collection_targets",
		"collection_tasks",
		"market_last_present",
		"market_latest_attempts",
		"market_price_ticks",
		"platform_accounts",
		"platform_product_mappings",
		"proxy_provider_regions",
		"proxy_providers",
		"rate_limit_policies",
		"rate_limit_states",
		"short_pool_watermarks",
		"steam_products",
	}
	if fmt.Sprint(tables) != fmt.Sprint(wantTables) {
		t.Fatalf("tables = %v, want %v", tables, wantTables)
	}
	var productIndex string
	if err := db.QueryRowContext(t.Context(), `
SELECT indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND indexname = 'steam_products_appid_product_id_idx'`).Scan(&productIndex); err != nil {
		t.Fatalf("read product list index: %v", err)
	}
	if !strings.Contains(productIndex, "(appid, product_id)") {
		t.Fatalf("product list index = %q", productIndex)
	}
	var priceTickIndex string
	if err := db.QueryRowContext(t.Context(), `
SELECT indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND indexname = 'market_price_ticks_platform_side_time_idx'`).Scan(&priceTickIndex); err != nil {
		t.Fatalf("read price tick platform-side index: %v", err)
	}
	if !strings.Contains(strings.ToLower(priceTickIndex), "(platform, side, collected_at desc, tick_id desc)") {
		t.Fatalf("price tick platform-side index = %q", priceTickIndex)
	}

	if _, err := db.ExecContext(t.Context(), `
UPDATE buffgo_storage_migrations
SET checksum = $1
WHERE version = '000001_catalog_market'`, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(t.Context(), db); !errors.Is(err, ErrMigrationChecksumDrift) {
		t.Fatalf("checksum drift error = %v", err)
	}

	rollbackDB := newTestSchema(t, dsn)
	if err := ApplyMigrations(t.Context(), rollbackDB); err != nil {
		t.Fatal(err)
	}
	bad := migration{
		Version:  "999999_broken",
		Checksum: strings.Repeat("a", 64),
		SQL:      `CREATE TABLE migration_partial (id BIGINT); SELECT * FROM migration_missing_table`,
	}
	if err := applyMigration(t.Context(), rollbackDB, bad); err == nil {
		t.Fatal("broken migration succeeded")
	}
	var partial *string
	if err := rollbackDB.QueryRowContext(t.Context(), `SELECT to_regclass('migration_partial')::text`).Scan(&partial); err != nil {
		t.Fatal(err)
	}
	if partial != nil {
		t.Fatal("failed migration left a partial table")
	}
	var recorded bool
	if err := rollbackDB.QueryRowContext(t.Context(), `
SELECT EXISTS(SELECT 1 FROM buffgo_storage_migrations WHERE version = '999999_broken')`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded {
		t.Fatal("failed migration was recorded")
	}

	upgradeDB := newTestSchema(t, dsn)
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:2] {
		if err := applyMigration(t.Context(), upgradeDB, migration); err != nil {
			t.Fatal(err)
		}
	}
	var existingNodeID int64
	if err := upgradeDB.QueryRowContext(t.Context(), `
INSERT INTO access_nodes(name, kind, region, egress_mode)
VALUES ('pre-combination-node', 'direct', 'domestic', 'static')
RETURNING node_id`).Scan(&existingNodeID); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(t.Context(), upgradeDB, migrations[2]); err != nil {
		t.Fatal(err)
	}
	var assignmentRevision int64
	if err := upgradeDB.QueryRowContext(t.Context(), `
SELECT assignment_revision FROM access_nodes WHERE node_id = $1`, existingNodeID).Scan(&assignmentRevision); err != nil {
		t.Fatal(err)
	}
	if assignmentRevision != 1 {
		t.Fatalf("migrated assignment revision = %d, want 1", assignmentRevision)
	}

	legacyDB := newTestSchema(t, dsn)
	if _, err := legacyDB.ExecContext(t.Context(), `CREATE TABLE items (id BIGINT); CREATE TABLE schema_migrations (version TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(t.Context(), legacyDB); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"items", "schema_migrations", "steam_products"} {
		var exists bool
		if err := legacyDB.QueryRowContext(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists=%v err=%v", table, exists, err)
		}
	}
}

func testCollectionDDLConstraints(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	createdAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	nextCheck := createdAt.Add(time.Hour)
	emptyCursor := []byte{}

	insertTarget := func(query string, args ...any) int64 {
		t.Helper()
		var targetID int64
		if err := db.QueryRowContext(ctx, query, args...).Scan(&targetID); err != nil {
			t.Fatal(err)
		}
		return targetID
	}
	summaryTarget := insertTarget(`
INSERT INTO collection_targets (
    kind, platform, appid, side, desired_state, actual_state, reason_code,
    recovery_mode, next_check_at, changed_at
) VALUES ('summary', 'steam', 730, 'ask', 'enabled', 'waiting', 'scheduler_opportunity',
          'automatic', $1, $2)
RETURNING target_id`, nextCheck, createdAt)
	summaryProbe := insertTarget(`
INSERT INTO collection_targets (
    kind, platform, appid, side, desired_state, actual_state, reason_code,
    recovery_mode, changed_at
) VALUES ('summary', 'steam', 730, 'bid', 'enabled', 'blocked', 'session_invalid',
          'manual', $1)
RETURNING target_id`, createdAt)
	summaryOther := insertTarget(`
INSERT INTO collection_targets (
    kind, platform, appid, side, desired_state, actual_state, period_microseconds, changed_at
) VALUES ('summary', 'steam', 252490, 'ask', 'enabled', 'starting', NULL, $1)
RETURNING target_id`, createdAt)

	reject := func(name, query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, invalid := range []struct {
		name  string
		query string
	}{
		{
			name: "catalog kind",
			query: `INSERT INTO collection_targets(kind,platform,appid,side,desired_state,actual_state,changed_at)
                    VALUES ('catalog','steam',730,'ask','enabled','starting',NOW())`,
		},
		{
			name: "summary without appid",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,changed_at)
                    VALUES ('summary','steam','ask','enabled','starting',NOW())`,
		},
		{
			name: "summary period",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,period_microseconds,changed_at)
                    VALUES ('summary','buff','ask','enabled','starting',1,NOW())`,
		},
		{
			name: "summary null side",
			query: `INSERT INTO collection_targets(kind,platform,desired_state,actual_state,changed_at)
                    VALUES ('summary','buff','enabled','starting',NOW())`,
		},
		{
			name: "noncanonical platform",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,changed_at)
                    VALUES ('summary','BUFF','ask','enabled','starting',NOW())`,
		},
		{
			name: "invalid side",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,changed_at)
                    VALUES ('summary','other','sell','enabled','starting',NOW())`,
		},
		{
			name: "waiting without controlled reason",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,recovery_mode,next_check_at,changed_at)
                    VALUES ('summary','buff','bid','enabled','waiting','automatic',NOW(),NOW())`,
		},
		{
			name: "nonfuture automatic recheck",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,reason_code,recovery_mode,next_check_at,changed_at)
                    VALUES ('summary','other','ask','enabled','waiting','next_cycle','automatic',NOW(),NOW())`,
		},
		{
			name: "automatic manual blocker",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,reason_code,recovery_mode,next_check_at,changed_at)
                    VALUES ('summary','igxe','ask','enabled','blocked','session_invalid','automatic',NOW(),NOW())`,
		},
		{
			name: "manual automatic blocker",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,reason_code,recovery_mode,changed_at)
                    VALUES ('summary','igxe','bid','enabled','blocked','cooldown','manual',NOW())`,
		},
		{
			name: "uncontrolled diagnostic",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,reason_code,recovery_mode,changed_at)
                    VALUES ('summary','other','ask','enabled','error','raw upstream body','manual',NOW())`,
		},
		{
			name: "target identity duplicate",
			query: `INSERT INTO collection_targets(kind,platform,appid,side,desired_state,actual_state,changed_at)
                    VALUES ('summary','steam',730,'ask','enabled','starting',NOW())`,
		},
		{
			name: "infinite target time",
			query: `INSERT INTO collection_targets(kind,platform,side,desired_state,actual_state,changed_at)
                    VALUES ('summary','other','bid','enabled','starting','infinity')`,
		},
	} {
		reject(invalid.name, invalid.query)
	}

	reject("negative write_seq", `
UPDATE collection_targets SET write_seq = -1 WHERE target_id = $1`, summaryTarget)
	reject("negative refill_total", `
UPDATE collection_targets SET refill_total = -1 WHERE target_id = $1`, summaryTarget)
	reject("oversize refill cursor", `
UPDATE collection_targets SET refill_cursor = $1 WHERE target_id = $2`, make([]byte, 4097), summaryTarget)

	account, err := store.CreateAccount(ctx, "steam", "ddl-queue-account", []byte("ddl-queue-session"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateNode(ctx, "ddl-queue-node", resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionHongKong, EgressMode: resource.EgressModeStatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	combination, err := store.CreateCombination(ctx, account.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `
INSERT INTO collection_tasks (target_id, enqueue_seq, enqueued_at, kind, payload, state)
VALUES ($1, 1, $2, 'ask_page', '{"start":0,"count":10}', 'queued')`, summaryTarget, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO collection_tasks (target_id, enqueue_seq, enqueued_at, kind, payload, state, claimed_by, claimed_at, claim_generation)
VALUES ($1, 2, $2, 'bid_batch', '{"after_id":0,"limit":10}', 'claimed', $3, $2, 1)`,
		summaryOther, createdAt, combination.ID); err != nil {
		t.Fatalf("claimed task: %v", err)
	}

	reject("duplicate enqueue seq", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state)
VALUES ($1,1,$2,'ask_page','{}','queued')`, summaryTarget, createdAt)
	reject("orphan task target", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state)
VALUES (9223372036854775807,1,$1,'ask_page','{}','queued')`, createdAt)
	reject("invalid task kind", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state)
VALUES ($1,3,$2,'summary','{}','queued')`, summaryTarget, createdAt)
	reject("invalid task state", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state)
VALUES ($1,4,$2,'ask_page','{}','running')`, summaryTarget, createdAt)
	reject("queued with claim", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state,claimed_by,claimed_at,claim_generation)
VALUES ($1,5,$2,'ask_page','{}','queued',$3,$2,1)`, summaryTarget, createdAt, combination.ID)
	reject("claimed without worker", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state,claimed_at,claim_generation)
VALUES ($1,6,$2,'ask_page','{}','claimed',$2,1)`, summaryTarget, createdAt)
	reject("zero enqueue seq", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state)
VALUES ($1,0,$2,'ask_page','{}','queued')`, summaryTarget, createdAt)
	reject("infinite enqueue time", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state)
VALUES ($1,7,'infinity','ask_page','{}','queued')`, summaryTarget)
	reject("orphan claimed_by", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state,claimed_by,claimed_at,claim_generation)
VALUES ($1,8,$2,'ask_page','{}','claimed',9223372036854775807,$2,1)`, summaryTarget, createdAt)
	reject("claimed without generation", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state,claimed_by,claimed_at)
VALUES ($1,9,$2,'ask_page','{}','claimed',$3,$2)`, summaryTarget, createdAt, combination.ID)
	reject("negative claim generation", `
INSERT INTO collection_tasks(target_id,enqueue_seq,enqueued_at,kind,payload,state,claim_generation)
VALUES ($1,10,$2,'ask_page','{}','queued',-1)`, summaryTarget, createdAt)

	digest := make([]byte, 32)
	digest[0] = 1
	if _, err := db.ExecContext(ctx, `
INSERT INTO collection_latest_pages (
    target_id, switch_version, write_seq, cursor_before, cursor_after, payload_digest,
    collected_at, committed_at
) VALUES ($1, 1, 1, $2, $3, $4, $5, $5)`,
		summaryTarget, emptyCursor, []byte("next"), digest, createdAt); err != nil {
		t.Fatal(err)
	}
	reject("duplicate latest page", `
INSERT INTO collection_latest_pages(target_id,switch_version,write_seq,cursor_before,cursor_after,payload_digest,collected_at,committed_at)
VALUES ($1,1,2,$2,$2,$3,$4,$4)`, summaryTarget, emptyCursor, digest, createdAt)
	reject("orphan latest page", `
INSERT INTO collection_latest_pages(target_id,switch_version,write_seq,cursor_before,cursor_after,payload_digest,collected_at,committed_at)
VALUES (9223372036854775807,1,1,$1,$1,$2,$3,$3)`, emptyCursor, digest, createdAt)
	reject("zero write_seq page", `
INSERT INTO collection_latest_pages(target_id,switch_version,write_seq,cursor_before,cursor_after,payload_digest,collected_at,committed_at)
VALUES ($1,1,0,$2,$2,$3,$4,$4)`, summaryProbe, emptyCursor, digest, createdAt)
	reject("oversize page cursor", `
INSERT INTO collection_latest_pages(target_id,switch_version,write_seq,cursor_before,cursor_after,payload_digest,collected_at,committed_at)
VALUES ($1,1,1,$2,$3,$4,$5,$5)`, summaryProbe, make([]byte, 4097), emptyCursor, digest, createdAt)
	reject("noncanonical page digest", `
INSERT INTO collection_latest_pages(target_id,switch_version,write_seq,cursor_before,cursor_after,payload_digest,collected_at,committed_at)
VALUES ($1,1,1,$2,$2,$3,$4,$4)`, summaryOther, emptyCursor, make([]byte, 31), createdAt)
	reject("zero page digest", `
INSERT INTO collection_latest_pages(target_id,switch_version,write_seq,cursor_before,cursor_after,payload_digest,collected_at,committed_at)
VALUES ($1,1,1,$2,$2,$3,$4,$4)`, summaryOther, emptyCursor, make([]byte, 32), createdAt)
	reject("page commit before collection", `
INSERT INTO collection_latest_pages(target_id,switch_version,write_seq,cursor_before,cursor_after,payload_digest,collected_at,committed_at)
VALUES ($1,1,1,$2,$2,$3,$4,$5)`, summaryOther, emptyCursor, digest, createdAt, createdAt.Add(-time.Second))
	reject("infinite page time", `
INSERT INTO collection_latest_pages(target_id,switch_version,write_seq,cursor_before,cursor_after,payload_digest,collected_at,committed_at)
VALUES ($1,1,1,$2,$2,$3,'infinity','infinity')`, summaryOther, emptyCursor, digest)
	reject("negative page switch", `
INSERT INTO collection_latest_pages(target_id,switch_version,write_seq,cursor_before,cursor_after,payload_digest,collected_at,committed_at)
VALUES ($1,-1,1,$2,$2,$3,$4,$4)`, summaryOther, emptyCursor, digest, createdAt)
}

func testCatalogStorage(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	first, err := store.CreateSteamProduct(ctx, 730, "AK-47 | Redline")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateSteamProduct(ctx, 730, "AK-47 | Redline (Field-Tested)")
	if err != nil {
		t.Fatal(err)
	}
	if first.ProductID == second.ProductID {
		t.Fatal("different names must be different products")
	}
	same, err := store.CreateSteamProduct(ctx, 730, "AK-47 | Redline")
	if err != nil {
		t.Fatal(err)
	}
	if same.ProductID != first.ProductID {
		t.Fatal("same (appid, name) must upsert to one product")
	}
	otherApp, err := store.CreateSteamProduct(ctx, 252490, "AK-47 | Redline")
	if err != nil {
		t.Fatal(err)
	}
	if otherApp.ProductID == first.ProductID {
		t.Fatal("same name on another appid must be a different product")
	}
	products, err := store.ListSteamProductsByAppID(ctx, 730)
	if err != nil || len(products) != 2 {
		t.Fatalf("products = %v err=%v", products, err)
	}
	read, found, err := store.SteamProduct(ctx, first.ProductID)
	if err != nil || !found || read != first {
		t.Fatalf("product = %+v found=%v err=%v", read, found, err)
	}

	mapping := catalog.PlatformMapping{Platform: "buff", AppID: 730, PlatformItemID: "Item-A", ProductID: first.ProductID}
	if err := store.PutPlatformMapping(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	if err := store.PutPlatformMapping(ctx, mapping); err != nil {
		t.Fatalf("idempotent mapping: %v", err)
	}
	conflict := mapping
	conflict.ProductID = second.ProductID
	if err := store.PutPlatformMapping(ctx, conflict); !errors.Is(err, ErrMappingConflict) {
		t.Fatalf("mapping conflict = %v", err)
	}
	stored, found, err := store.PlatformMapping(ctx, mapping.Platform, mapping.AppID, mapping.PlatformItemID)
	if err != nil || !found || stored != mapping {
		t.Fatalf("mapping = %+v found=%v err=%v", stored, found, err)
	}

	caseVariant := mapping
	caseVariant.PlatformItemID = "item-a"
	caseVariant.ProductID = second.ProductID
	if err := store.PutPlatformMapping(ctx, caseVariant); err != nil {
		t.Fatalf("byte-distinct platform id rejected: %v", err)
	}

	crossApp := catalog.PlatformMapping{Platform: "buff", AppID: 730, PlatformItemID: "cross-app", ProductID: otherApp.ProductID}
	if err := store.PutPlatformMapping(ctx, crossApp); err == nil {
		t.Fatal("cross-app mapping accepted")
	}
	for _, statement := range []string{
		`INSERT INTO steam_products(appid,name) VALUES (0,'bad')`,
		`INSERT INTO steam_products(appid,name) VALUES (730,'')`,
		fmt.Sprintf(`INSERT INTO platform_product_mappings(platform,appid,platform_item_id,product_id) VALUES ('BUFF',730,'bad',%d)`, first.ProductID),
		fmt.Sprintf(`INSERT INTO platform_product_mappings(platform,appid,platform_item_id,product_id) VALUES ('buff',730,'',%d)`, first.ProductID),
	} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("invalid SQL accepted: %s", statement)
		}
	}

	concurrentA := catalog.PlatformMapping{Platform: "steam", AppID: 730, PlatformItemID: "concurrent", ProductID: first.ProductID}
	concurrentB := concurrentA
	concurrentB.ProductID = second.ProductID
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, candidate := range []catalog.PlatformMapping{concurrentA, concurrentB} {
		go func(value catalog.PlatformMapping) {
			<-start
			results <- store.PutPlatformMapping(ctx, value)
		}(candidate)
	}
	close(start)
	errA, errB := <-results, <-results
	if (errA == nil) == (errB == nil) ||
		(errA != nil && !errors.Is(errA, ErrMappingConflict)) ||
		(errB != nil && !errors.Is(errB, ErrMappingConflict)) {
		t.Fatalf("concurrent mapping errors = %v, %v", errA, errB)
	}
}

func testMarketFacts(t *testing.T, dsn string) {
	store, _ := migratedStore(t, dsn)
	ctx := t.Context()
	product, err := store.CreateSteamProduct(ctx, 730, "Market Product")
	if err != nil {
		t.Fatal(err)
	}
	key := MarketKey{ProductID: product.ProductID, Platform: "steam", Side: market.SideAsk}
	base := time.Date(2026, 8, 11, 12, 0, 0, 123456789, time.UTC)
	zero := int64(0)
	present := makePresent(t, market.SideAsk, 0, &zero, nil, nil, base)
	page := oneAttemptBatch(product, "steam", market.WriteOrder{SwitchVersion: 1, WriteSequence: 1}, present, "")
	if applied, err := store.saveObservations(ctx, page); err != nil || !applied {
		t.Fatalf("present commit applied=%v err=%v", applied, err)
	}
	latest, found, err := store.LatestAttempt(ctx, key)
	if err != nil || !found || latest.Status != market.StatusPresent {
		t.Fatalf("latest = %+v found=%v err=%v", latest, found, err)
	}
	last, found, err := store.LastPresent(ctx, key)
	if err != nil || !found {
		t.Fatalf("last present found=%v err=%v", found, err)
	}
	if last.Observation.Summary.PriceCents != 0 || last.Observation.Summary.OrderCount == nil || *last.Observation.Summary.OrderCount != 0 || last.Observation.Summary.ItemCount != nil {
		t.Fatalf("last present = %+v", last.Observation.Summary)
	}
	if last.Observation.SourceTime != nil || last.Observation.CollectedAt.Nanosecond() != 123456000 {
		t.Fatalf("present times = source:%v collected:%v", last.Observation.SourceTime, last.Observation.CollectedAt)
	}
	bid := makePresent(t, market.SideBid, 900, nil, nil, nil, base)
	if _, err := store.saveObservations(ctx, oneAttemptBatch(product, "steam", market.WriteOrder{SwitchVersion: 1, WriteSequence: 1}, bid, "")); err != nil {
		t.Fatalf("bid commit: %v", err)
	}
	bidKey := MarketKey{ProductID: product.ProductID, Platform: "steam", Side: market.SideBid}
	bidLast, found, err := store.LastPresent(ctx, bidKey)
	if err != nil || !found || bidLast.Observation.Summary.PriceCents != 900 {
		t.Fatalf("bid last = %+v found=%v err=%v", bidLast, found, err)
	}
	askLast, found, err := store.LastPresent(ctx, key)
	if err != nil || !found || askLast.Observation.Summary.PriceCents != 0 {
		t.Fatalf("ask changed after bid write = %+v found=%v err=%v", askLast, found, err)
	}

	statuses := []struct {
		status market.ObservationStatus
		reason string
	}{
		{market.StatusFailed, "http.timeout"},
		{market.StatusEmpty, ""},
		{market.StatusUnavailable, "unsupported.item"},
	}
	for index, state := range statuses {
		observation := market.Observation{Side: market.SideAsk, Status: state.status, CollectedAt: base.Add(time.Duration(index+1) * time.Minute)}
		page := oneAttemptBatch(product, "steam", market.WriteOrder{SwitchVersion: 1, WriteSequence: int64(index + 2)}, observation, state.reason)
		if _, err := store.saveObservations(ctx, page); err != nil {
			t.Fatalf("commit %s: %v", state.status, err)
		}
		latest, found, err = store.LatestAttempt(ctx, key)
		if err != nil || !found || latest.Status != state.status || latest.ReasonCode != state.reason {
			t.Fatalf("latest after %s = %+v found=%v err=%v", state.status, latest, found, err)
		}
		unchanged, found, err := store.LastPresent(ctx, key)
		if err != nil || !found || unchanged.Order.WriteSequence != 1 {
			t.Fatalf("historical price changed after %s: %+v found=%v err=%v", state.status, unchanged, found, err)
		}
	}

	failedOnly, err := store.CreateSteamProduct(ctx, 730, "Never Present")
	if err != nil {
		t.Fatal(err)
	}
	failed := market.Observation{Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: base}
	if _, err := store.saveObservations(ctx, oneAttemptBatch(failedOnly, "steam", market.WriteOrder{SwitchVersion: 1, WriteSequence: 1}, failed, "decode.invalid")); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.LastPresent(ctx, MarketKey{ProductID: failedOnly.ProductID, Platform: "steam", Side: market.SideAsk}); err != nil || found {
		t.Fatalf("failed-only last present found=%v err=%v", found, err)
	}
}

func testMarketOrdering(t *testing.T, dsn string) {
	store, _ := migratedStore(t, dsn)
	ctx := t.Context()
	first, _ := store.CreateSteamProduct(ctx, 730, "First")
	second, _ := store.CreateSteamProduct(ctx, 730, "Second")
	third, _ := store.CreateSteamProduct(ctx, 730, "Third")
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	original := makePresent(t, market.SideAsk, 1000, nil, nil, nil, base)
	originalBatch := oneAttemptBatch(first, "buff", market.WriteOrder{SwitchVersion: 1, WriteSequence: 2}, original, "")
	if _, err := store.saveObservations(ctx, originalBatch); err != nil {
		t.Fatal(err)
	}
	if applied, err := store.saveObservations(ctx, originalBatch); err != nil || applied {
		t.Fatalf("equal retry applied=%v err=%v", applied, err)
	}
	if applied, err := store.saveObservations(ctx, oneAttemptBatch(second, "buff", originalBatch.Order, original, "")); err != nil || !applied {
		t.Fatalf("same order for a different identity applied=%v err=%v", applied, err)
	}

	changed := makePresent(t, market.SideAsk, 1001, nil, nil, nil, base)
	if _, err := store.saveObservations(ctx, oneAttemptBatch(first, "buff", originalBatch.Order, changed, "")); !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("equal changed payload error = %v", err)
	}
	laterRequest := makePresent(t, market.SideAsk, 9999, nil, nil, nil, base.Add(24*time.Hour))
	laterBatch := oneAttemptBatch(first, "buff", market.WriteOrder{SwitchVersion: 1, WriteSequence: 1}, laterRequest, "")
	if applied, err := store.saveObservations(ctx, laterBatch); err != nil || !applied {
		t.Fatalf("later request with lower write sequence applied=%v err=%v", applied, err)
	}

	earlierRequest := market.Observation{Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: base.Add(-time.Hour)}
	earlierBatch := oneAttemptBatch(first, "buff", market.WriteOrder{SwitchVersion: 1, WriteSequence: 3}, earlierRequest, "http.timeout")
	if applied, err := store.saveObservations(ctx, earlierBatch); err != nil || applied {
		t.Fatalf("earlier request with higher write sequence applied=%v err=%v", applied, err)
	}
	latest, _, err := store.LatestAttempt(ctx, MarketKey{ProductID: first.ProductID, Platform: "buff", Side: market.SideAsk})
	if err != nil || latest.Order != laterBatch.Order || !latest.CollectedAt.Equal(base.Add(24*time.Hour)) {
		t.Fatalf("latest = %+v err=%v", latest, err)
	}

	mixed := observationBatch{
		AppID:    730,
		Platform: "buff",
		Side:     market.SideAsk,
		Order:    originalBatch.Order,
		Attempts: []AttemptWrite{
			{ProductID: first.ProductID, Observation: original},
			{ProductID: third.ProductID, Observation: original},
		},
	}
	if applied, err := store.saveObservations(ctx, mixed); err != nil || !applied {
		t.Fatalf("mixed retry applied=%v err=%v", applied, err)
	}
	if thirdLatest, found, err := store.LatestAttempt(ctx, MarketKey{ProductID: third.ProductID, Platform: "buff", Side: market.SideAsk}); err != nil || !found || thirdLatest.Order != mixed.Order {
		t.Fatalf("mixed batch new product latest=%+v found=%v err=%v", thirdLatest, found, err)
	}
	latest, _, err = store.LatestAttempt(ctx, MarketKey{ProductID: first.ProductID, Platform: "buff", Side: market.SideAsk})
	if err != nil || latest.Order != laterBatch.Order || !latest.CollectedAt.Equal(base.Add(24*time.Hour)) {
		t.Fatalf("mixed batch rolled first product back: latest=%+v err=%v", latest, err)
	}

	low := oneAttemptBatch(second, "steam", market.WriteOrder{SwitchVersion: 1, WriteSequence: 1}, market.Observation{Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: base}, "http.low")
	high := oneAttemptBatch(second, "steam", market.WriteOrder{SwitchVersion: 2, WriteSequence: 1}, market.Observation{Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: base.Add(-time.Hour)}, "http.high")
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, page := range []observationBatch{low, high} {
		go func(value observationBatch) {
			<-start
			_, err := store.saveObservations(ctx, value)
			results <- err
		}(page)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil && !errors.Is(err, ErrStaleObservation) {
			t.Fatalf("concurrent observation error = %v", err)
		}
	}
	latest, _, err = store.LatestAttempt(ctx, MarketKey{ProductID: second.ProductID, Platform: "steam", Side: market.SideAsk})
	if err != nil || latest.Order != high.Order || latest.ReasonCode != "http.high" {
		t.Fatalf("concurrent latest = %+v err=%v", latest, err)
	}
}

func testBatchRollback(t *testing.T, dsn string) {
	store, db := migratedStore(t, dsn)
	ctx := t.Context()
	valid, _ := store.CreateSteamProduct(ctx, 730, "Valid")
	orphan, _ := store.CreateSteamProduct(ctx, 730, "Orphan Present")
	orphanPrice, _ := store.CreateSteamProduct(ctx, 730, "Orphan Price")
	futurePrice, _ := store.CreateSteamProduct(ctx, 730, "Future Price")
	wrongApp, _ := store.CreateSteamProduct(ctx, 252490, "Wrong App")
	at := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	observation := makePresent(t, market.SideAsk, 12345, nil, nil, nil, at)
	page := observationBatch{
		AppID:    730,
		Platform: "steam",
		Side:     market.SideAsk,
		Order:    market.WriteOrder{SwitchVersion: 1, WriteSequence: 1},
		Attempts: []AttemptWrite{
			{ProductID: valid.ProductID, Observation: observation},
			{ProductID: wrongApp.ProductID, Observation: observation},
		},
	}
	if _, err := store.saveObservations(ctx, page); err == nil {
		t.Fatal("cross-app page succeeded")
	}
	if _, found, err := store.LatestAttempt(ctx, MarketKey{ProductID: valid.ProductID, Platform: "steam", Side: market.SideAsk}); err != nil || found {
		t.Fatalf("invalid batch partially wrote valid product found=%v err=%v", found, err)
	}

	if _, err := db.ExecContext(ctx, `ALTER TABLE market_last_present ADD CONSTRAINT reject_test_price CHECK (price_cny_cents <> 12345)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.saveObservations(ctx, oneAttemptBatch(valid, "steam", page.Order, observation, "")); err == nil {
		t.Fatal("last-present constraint failure did not fail page")
	}
	if _, found, err := store.LatestAttempt(ctx, MarketKey{ProductID: valid.ProductID, Platform: "steam", Side: market.SideAsk}); err != nil || found {
		t.Fatalf("failed present batch left latest row found=%v err=%v", found, err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE market_last_present DROP CONSTRAINT reject_test_price`); err != nil {
		t.Fatal(err)
	}
	secondValid, _ := store.CreateSteamProduct(ctx, 730, "Second Valid")
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
ALTER TABLE market_last_present
ADD CONSTRAINT reject_second_test_price
CHECK (product_id <> %d OR price_cny_cents <> 12345)`, secondValid.ProductID)); err != nil {
		t.Fatal(err)
	}
	crossIdentity := observationBatch{
		AppID:    730,
		Platform: "steam",
		Side:     market.SideAsk,
		Order:    page.Order,
		Attempts: []AttemptWrite{
			{ProductID: valid.ProductID, Observation: observation},
			{ProductID: secondValid.ProductID, Observation: observation},
		},
	}
	if _, err := store.saveObservations(ctx, crossIdentity); err == nil {
		t.Fatal("second identity SQL failure did not fail batch")
	}
	for _, productID := range []catalog.ProductID{valid.ProductID, secondValid.ProductID} {
		if _, found, err := store.LatestAttempt(ctx, MarketKey{ProductID: productID, Platform: "steam", Side: market.SideAsk}); err != nil || found {
			t.Fatalf("cross-identity failure left latest for %d found=%v err=%v", productID, found, err)
		}
		if _, found, err := store.LastPresent(ctx, MarketKey{ProductID: productID, Platform: "steam", Side: market.SideAsk}); err != nil || found {
			t.Fatalf("cross-identity failure left price for %d found=%v err=%v", productID, found, err)
		}
	}

	for _, statement := range []string{
		fmt.Sprintf(`INSERT INTO market_latest_attempts(product_id,platform,side,status,reason_code,collected_at,switch_version,write_seq) VALUES (%d,'steam','sell','failed','http.error',NOW(),1,1)`, valid.ProductID),
		fmt.Sprintf(`INSERT INTO market_latest_attempts(product_id,platform,side,status,reason_code,collected_at,switch_version,write_seq) VALUES (%d,'steam','ask','unknown','',NOW(),1,1)`, valid.ProductID),
		fmt.Sprintf(`INSERT INTO market_latest_attempts(product_id,platform,side,status,reason_code,collected_at,switch_version,write_seq) VALUES (%d,'steam','ask','failed','raw error body',NOW(),1,1)`, valid.ProductID),
		fmt.Sprintf(`INSERT INTO market_last_present(product_id,platform,side,price_cny_cents,collected_at,switch_version,write_seq) VALUES (%d,'steam','ask',-1,NOW(),1,1)`, valid.ProductID),
		fmt.Sprintf(`INSERT INTO market_last_present(product_id,platform,side,price_cny_cents,order_count,collected_at,switch_version,write_seq) VALUES (%d,'steam','ask',1,-1,NOW(),1,1)`, valid.ProductID),
	} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("invalid market SQL accepted: %s", statement)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO market_latest_attempts (
    product_id, platform, side, status, reason_code, source_time, collected_at,
    switch_version, write_seq
) VALUES (%d, 'steam', 'ask', 'present', '', '0001-01-01 00:00:00+00', NOW(), 1, 1)`, valid.ProductID)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LatestAttempt(ctx, MarketKey{ProductID: valid.ProductID, Platform: "steam", Side: market.SideAsk}); !errors.Is(err, ErrMarketIntegrity) {
		t.Fatalf("zero source_time read error = %v", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO market_latest_attempts (
    product_id, platform, side, status, reason_code, collected_at,
    switch_version, write_seq
) VALUES (%d, 'steam', 'ask', 'present', '', NOW(), 1, 1)`, orphan.ProductID)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LatestAttempt(ctx, MarketKey{ProductID: orphan.ProductID, Platform: "steam", Side: market.SideAsk}); !errors.Is(err, ErrMarketIntegrity) {
		t.Fatalf("present attempt without matching price error = %v", err)
	}
	orphanLatestRecovery := market.Observation{Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: at.Add(time.Minute)}
	if _, err := store.saveObservations(ctx, oneAttemptBatch(orphan, "steam", market.WriteOrder{SwitchVersion: 1, WriteSequence: 2}, orphanLatestRecovery, "storage.retry")); !errors.Is(err, ErrMarketIntegrity) {
		t.Fatalf("write over orphan latest error = %v", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO market_last_present (
    product_id, platform, side, price_cny_cents, collected_at,
    switch_version, write_seq
) VALUES (%d, 'steam', 'ask', 100, NOW(), 1, 1)`, orphanPrice.ProductID)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LastPresent(ctx, MarketKey{ProductID: orphanPrice.ProductID, Platform: "steam", Side: market.SideAsk}); !errors.Is(err, ErrMarketIntegrity) {
		t.Fatalf("price without matching latest error = %v", err)
	}
	orphanRecovery := market.Observation{Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: at.Add(time.Minute)}
	if _, err := store.saveObservations(ctx, oneAttemptBatch(orphanPrice, "steam", market.WriteOrder{SwitchVersion: 1, WriteSequence: 2}, orphanRecovery, "storage.retry")); !errors.Is(err, ErrMarketIntegrity) {
		t.Fatalf("write over orphan price error = %v", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
INSERT INTO market_latest_attempts (
    product_id, platform, side, status, reason_code, collected_at,
    switch_version, write_seq
) VALUES (%[1]d, 'steam', 'ask', 'failed', 'http.timeout', NOW(), 1, 1);
INSERT INTO market_last_present (
    product_id, platform, side, price_cny_cents, collected_at,
    switch_version, write_seq
) VALUES (%[1]d, 'steam', 'ask', 100, NOW(), 1, 2)`, futurePrice.ProductID)); err != nil {
		t.Fatal(err)
	}
	futureKey := MarketKey{ProductID: futurePrice.ProductID, Platform: "steam", Side: market.SideAsk}
	if _, _, err := store.LatestAttempt(ctx, futureKey); !errors.Is(err, ErrMarketIntegrity) {
		t.Fatalf("latest before historical price error = %v", err)
	}
	if _, _, err := store.LastPresent(ctx, futureKey); !errors.Is(err, ErrMarketIntegrity) {
		t.Fatalf("historical price newer than latest error = %v", err)
	}
}

func migratedStore(t *testing.T, dsn string) (*Store, *sql.DB) {
	t.Helper()
	db := newTestSchema(t, dsn)
	if err := ApplyMigrations(t.Context(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return store, db
}

func newTestSchema(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	admin := stdlib.OpenDB(*adminConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("connect explicit test PostgreSQL: %v", err)
	}
	schema := fmt.Sprintf("buffgo_goal4a_%d_%d", os.Getpid(), testSchemaSequence.Add(1))
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("create test schema: %v", err)
	}

	scopedConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	scopedConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*scopedConfig)
	if err := db.PingContext(ctx); err != nil {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		t.Fatalf("connect test schema: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		_ = admin.Close()
	})
	return db
}

func makePresent(t *testing.T, side market.Side, price market.CNYCents, orderCount, itemCount *int64, sourceTime *time.Time, collectedAt time.Time) market.Observation {
	t.Helper()
	observation, err := market.NewPresentObservation(market.PresentInput{
		Currency:    market.CurrencyCNY,
		Side:        side,
		PriceCents:  &price,
		OrderCount:  orderCount,
		ItemCount:   itemCount,
		SourceTime:  sourceTime,
		CollectedAt: collectedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func oneAttemptBatch(product catalog.SteamProduct, platform string, order market.WriteOrder, observation market.Observation, reason string) observationBatch {
	return observationBatch{
		AppID:    product.AppID,
		Platform: platform,
		Side:     observation.Side,
		Order:    order,
		Attempts: []AttemptWrite{{ProductID: product.ProductID, Observation: observation, ReasonCode: reason}},
	}
}
