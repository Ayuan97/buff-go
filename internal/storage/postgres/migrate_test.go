package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationManifest(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 12 {
		t.Fatalf("migration count = %d, want 12", len(migrations))
	}
	wantVersions := []string{
		"000001_catalog_market",
		"000002_resources",
		"000003_resource_combinations",
		"000004_rate_limits",
		"000005_collection",
		"000006_plaintext_credentials",
		"000007_game_direction_summary",
		"000008_steam_product_identity",
		"000009_proxy_providers",
		"000010_page_payloads",
		"000011_product_media_and_page_actor",
		"000012_target_sort_order",
	}
	for index, current := range migrations {
		if current.Version != wantVersions[index] {
			t.Fatalf("version[%d] = %q, want %q", index, current.Version, wantVersions[index])
		}
		sum := sha256.Sum256([]byte(current.SQL))
		if current.Checksum != hex.EncodeToString(sum[:]) {
			t.Fatalf("migration %q checksum does not match embedded bytes", current.Version)
		}
		if len(current.Checksum) != 64 {
			t.Fatalf("migration %q checksum length = %d", current.Version, len(current.Checksum))
		}
	}
}

func TestMigrationManifestIsSortedAndValidated(t *testing.T) {
	source := fstest.MapFS{
		"migrations/000002_second.sql": {Data: []byte("SELECT 2;")},
		"migrations/000001_first.sql":  {Data: []byte("SELECT 1;")},
	}
	migrations, err := loadMigrationsFrom(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{migrations[0].Version, migrations[1].Version}; got[0] != "000001_first" || got[1] != "000002_second" {
		t.Fatalf("migration order = %v", got)
	}

	for name, source := range map[string]fstest.MapFS{
		"missing": {},
		"bad name": {
			"migrations/not_versioned.sql": {Data: []byte("SELECT 1;")},
		},
		"empty": {
			"migrations/000001_empty.sql": {Data: []byte(" \n")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadMigrationsFrom(source); err == nil {
				t.Fatal("expected manifest validation error")
			}
		})
	}
}

func TestStoredChecksumDriftIsRejected(t *testing.T) {
	current := migration{Version: "000001_catalog_market", Checksum: strings.Repeat("a", 64)}
	if err := validateStoredChecksum(current, current.Checksum); err != nil {
		t.Fatalf("matching checksum: %v", err)
	}
	err := validateStoredChecksum(current, strings.Repeat("b", 64))
	if !errors.Is(err, ErrMigrationChecksumDrift) {
		t.Fatalf("drift error = %v", err)
	}
}

func TestCatalogMarketMigrationHasOnlyApprovedTables(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	tablePattern := regexp.MustCompile(`(?im)CREATE\s+TABLE\s+([a-z_][a-z0-9_]*)\s*\(`)
	matches := tablePattern.FindAllStringSubmatch(migrations[0].SQL, -1)
	want := map[string]bool{
		"steam_products":            false,
		"platform_product_mappings": false,
		"market_latest_attempts":    false,
		"market_last_present":       false,
	}
	if len(matches) != len(want) {
		t.Fatalf("business table count = %d, want %d", len(matches), len(want))
	}
	for _, match := range matches {
		if _, ok := want[match[1]]; !ok {
			t.Fatalf("unapproved business table %q", match[1])
		}
		want[match[1]] = true
	}
	for table, found := range want {
		if !found {
			t.Errorf("missing business table %q", table)
		}
	}
}

func TestCatalogMarketMigrationConstraints(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	sqlText := migrations[0].SQL
	products := compactSQL(tableDefinition(t, sqlText, "steam_products"))
	if strings.Contains(products, "unique (appid, name)") {
		t.Fatal("Steam product names must not be a storage identity")
	}
	for _, required := range []string{
		"name text collate \"c\" not null",
		"unique (product_id, appid)",
		"check (product_id > 0)",
		"check (appid > 0)",
		"check (name <> '')",
	} {
		assertContains(t, products, required)
	}
	assertContains(t, compactSQL(sqlText), "create index steam_products_appid_product_id_idx on steam_products (appid, product_id)")

	mappings := compactSQL(tableDefinition(t, sqlText, "platform_product_mappings"))
	for _, required := range []string{
		"platform text collate \"c\" not null",
		"platform_item_id text collate \"c\" not null",
		"primary key (platform, appid, platform_item_id)",
		"foreign key (product_id, appid) references steam_products (product_id, appid)",
		"platform ~ '^[a-z][a-z0-9_.-]{0,31}$'",
	} {
		assertContains(t, mappings, required)
	}

	attempts := compactSQL(tableDefinition(t, sqlText, "market_latest_attempts"))
	for _, forbidden := range []string{"currency", "price_cents", "price_cny_cents", "order_count", "item_count"} {
		if strings.Contains(attempts, forbidden) {
			t.Fatalf("latest attempt contains forbidden field %q", forbidden)
		}
	}
	for _, required := range []string{
		"platform text collate \"c\" not null",
		"side text collate \"c\" not null",
		"status text collate \"c\" not null",
		"reason_code text collate \"c\" not null default ''",
		"primary key (product_id, platform, side)",
		"side in ('bid', 'ask')",
		"status in ('present', 'empty', 'unavailable', 'failed')",
		"status in ('present', 'empty') and reason_code = ''",
		"status in ('unavailable', 'failed') and reason_code ~ '^[a-z][a-z0-9_.-]{0,63}$'",
		"platform ~ '^[a-z][a-z0-9_.-]{0,31}$'",
		"check (isfinite(collected_at))",
	} {
		assertContains(t, attempts, required)
	}
	assertPositiveFences(t, attempts)

	lastPresent := compactSQL(tableDefinition(t, sqlText, "market_last_present"))
	for _, required := range []string{
		"platform text collate \"c\" not null",
		"side text collate \"c\" not null",
		"primary key (product_id, platform, side)",
		"price_cny_cents bigint not null",
		"check (price_cny_cents >= 0)",
		"check (order_count is null or order_count >= 0)",
		"check (item_count is null or item_count >= 0)",
		"platform ~ '^[a-z][a-z0-9_.-]{0,31}$'",
	} {
		assertContains(t, lastPresent, required)
	}
	if strings.Contains(lastPresent, "currency") {
		t.Fatal("last present must fix CNY in the price column name")
	}
	assertPositiveFences(t, lastPresent)
}

func TestResourceMigrationHasOnlyApprovedTables(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	tablePattern := regexp.MustCompile(`(?im)CREATE\s+TABLE\s+([a-z_][a-z0-9_]*)\s*\(`)
	matches := tablePattern.FindAllStringSubmatch(migrations[1].SQL, -1)
	want := map[string]bool{"platform_accounts": false, "access_nodes": false}
	if len(matches) != len(want) {
		t.Fatalf("resource business table count = %d, want %d", len(matches), len(want))
	}
	for _, match := range matches {
		if _, ok := want[match[1]]; !ok {
			t.Fatalf("unapproved resource table %q", match[1])
		}
		want[match[1]] = true
	}
	for table, found := range want {
		if !found {
			t.Errorf("missing resource table %q", table)
		}
	}
	for _, forbidden := range []string{"account_node", "combination", "lease", "rate_limit", "cooldown", "history"} {
		if strings.Contains(compactSQL(migrations[1].SQL), forbidden) {
			t.Errorf("resource migration contains Goal 5B/5C structure %q", forbidden)
		}
	}
}

func TestResourceMigrationConstraints(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	accounts := compactSQL(tableDefinition(t, migrations[1].SQL, "platform_accounts"))
	for _, required := range []string{
		"platform text collate \"c\" not null",
		"alias text collate \"c\" not null",
		"unique (platform, alias)",
		"unique (account_id, platform)",
		"octet_length(alias) between 1 and 128",
		"alias !~ '[[:cntrl:]]'",
		"session_state in ('unverified', 'valid', 'invalid')",
		"session_revision > 0",
		"session_envelope_version = 1",
		"octet_length(session_key_id) between 1 and 64",
		"octet_length(session_nonce) = 12",
		"octet_length(session_ciphertext) > 16",
	} {
		assertContains(t, accounts, required)
	}

	nodes := compactSQL(tableDefinition(t, migrations[1].SQL, "access_nodes"))
	for _, required := range []string{
		"name text collate \"c\" not null",
		"unique (node_id, assigned_platform)",
		"octet_length(name) between 1 and 128",
		"name !~ '[[:cntrl:]]'",
		"kind in ('direct', 'proxy')",
		"region in ('domestic', 'foreign', 'hongkong')",
		"egress_mode in ('static', 'sticky')",
		"state in ('validating', 'available', 'unavailable')",
		"assigned_platform is null or assigned_platform ~ '^[a-z][a-z0-9_.-]{0,31}$'",
		"proxy_envelope_version is not null",
		"proxy_key_id is not null",
		"proxy_nonce is not null",
		"proxy_ciphertext is not null",
		"exit_verified_revision is not null",
		"exit_address <<= '100.64.0.0/10'::inet",
		"sticky_session_valid_until is null or exit_valid_until is null or exit_valid_until <= sticky_session_valid_until",
	} {
		assertContains(t, nodes, required)
	}
}

func TestResourceCombinationMigrationHasOnlyApprovedTable(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	tablePattern := regexp.MustCompile(`(?im)CREATE\s+TABLE\s+([a-z_][a-z0-9_]*)\s*\(`)
	matches := tablePattern.FindAllStringSubmatch(migrations[2].SQL, -1)
	if len(matches) != 1 || matches[0][1] != "account_node_combinations" {
		t.Fatalf("combination tables = %v, want account_node_combinations only", matches)
	}
	compact := compactSQL(migrations[2].SQL)
	for _, forbidden := range []string{"occupancy", "lease", "ttl", "rate_limit", "cooldown", "history"} {
		if strings.Contains(compact, forbidden) {
			t.Fatalf("combination migration contains forbidden runtime structure %q", forbidden)
		}
	}
}

func TestResourceCombinationMigrationConstraints(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	compact := compactSQL(migrations[2].SQL)
	for _, required := range []string{
		"alter table access_nodes add column assignment_revision bigint not null default 1",
		"check (assignment_revision > 0)",
		"create index account_node_combinations_node_id_idx on account_node_combinations (node_id)",
	} {
		assertContains(t, compact, required)
	}

	combinations := compactSQL(tableDefinition(t, migrations[2].SQL, "account_node_combinations"))
	for _, required := range []string{
		"combination_id bigint generated always as identity",
		"platform text collate \"c\" not null",
		"primary key (combination_id)",
		"unique (account_id, node_id)",
		"foreign key (account_id, platform) references platform_accounts (account_id, platform) on update restrict on delete restrict",
		"foreign key (node_id, platform) references access_nodes (node_id, assigned_platform) on update restrict on delete restrict",
		"platform ~ '^[a-z][a-z0-9_.-]{0,31}$'",
	} {
		assertContains(t, combinations, required)
	}
}

func TestRateLimitMigrationHasOnlyApprovedTables(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	tablePattern := regexp.MustCompile(`(?im)CREATE\s+TABLE\s+([a-z_][a-z0-9_]*)\s*\(`)
	matches := tablePattern.FindAllStringSubmatch(migrations[3].SQL, -1)
	want := map[string]bool{"rate_limit_policies": false, "rate_limit_states": false}
	if len(matches) != len(want) {
		t.Fatalf("rate-limit table count = %d, want %d", len(matches), len(want))
	}
	for _, match := range matches {
		if _, ok := want[match[1]]; !ok {
			t.Fatalf("unapproved rate-limit table %q", match[1])
		}
		want[match[1]] = true
	}
	for table, found := range want {
		if !found {
			t.Errorf("missing rate-limit table %q", table)
		}
	}
	compact := compactSQL(migrations[3].SQL)
	for _, forbidden := range []string{"node_id", "combination_id", "lease", "ttl", "cookie", "session_ciphertext", "proxy_ciphertext"} {
		if strings.Contains(compact, forbidden) {
			t.Fatalf("rate-limit migration contains forbidden identity or secret %q", forbidden)
		}
	}
}

func TestRateLimitMigrationConstraints(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	policies := compactSQL(tableDefinition(t, migrations[3].SQL, "rate_limit_policies"))
	for _, required := range []string{
		"policy_id bigint generated always as identity",
		"unique (platform, rule_key)",
		"unique (policy_id, scope, kind)",
		"scope in ('platform', 'interface', 'account', 'ip', 'account_ip')",
		"kind in ('cooldown_only', 'min_interval', 'rolling_window')",
		"max_requests between 1 and 1024",
		"revision > 0",
		"isfinite(changed_at) and isfinite(ready_at) and ready_at >= changed_at",
	} {
		assertContains(t, policies, required)
	}

	states := compactSQL(tableDefinition(t, migrations[3].SQL, "rate_limit_states"))
	for _, required := range []string{
		"foreign key (policy_id, scope, kind) references rate_limit_policies (policy_id, scope, kind) on update restrict on delete restrict",
		"rolling_admitted_at timestamptz[] not null",
		"cardinality(rolling_admitted_at) <= 1024",
		"array_ndims(rolling_admitted_at) = 1",
		"array_position(rolling_admitted_at, null) is null",
		"rate_limit_rolling_state_valid( rolling_admitted_at, last_admitted_at, clock_floor_at )",
		"kind = 'cooldown_only' and next_allowed_at is null and cardinality(rolling_admitted_at) = 0 and last_admitted_at is null",
		"next_allowed_at is null and last_admitted_at is null",
		"next_allowed_at is not null and last_admitted_at is not null and next_allowed_at > last_admitted_at",
		"cardinality(rolling_admitted_at) = 0 and last_admitted_at is null",
		"cardinality(rolling_admitted_at) > 0 and last_admitted_at is not null",
		"exit_address <<= '100.64.0.0/10'::inet",
		"cooldown_until > cooldown_observed_at",
		"cooldown_reason_code in ('http_429', 'risk_control')",
	} {
		assertContains(t, states, required)
	}
	compact := compactSQL(migrations[3].SQL)
	for _, required := range []string{
		"create unique index rate_limit_states_global_key on rate_limit_states (policy_id) where scope in ('platform', 'interface')",
		"create unique index rate_limit_states_account_key on rate_limit_states (policy_id, account_id) where scope = 'account'",
		"create unique index rate_limit_states_ip_key on rate_limit_states (policy_id, exit_address) where scope = 'ip'",
		"create unique index rate_limit_states_account_ip_key on rate_limit_states (policy_id, account_id, exit_address) where scope = 'account_ip'",
		"create index rate_limit_states_policy_clock_idx on rate_limit_states (policy_id, clock_floor_at desc)",
	} {
		assertContains(t, compact, required)
	}
}

func TestCollectionMigrationHasOnlyApprovedTables(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	tablePattern := regexp.MustCompile(`(?im)CREATE\s+TABLE\s+([a-z_][a-z0-9_]*)\s*\(`)
	matches := tablePattern.FindAllStringSubmatch(migrations[4].SQL, -1)
	want := map[string]bool{
		"collection_targets": false,
		"collection_runs":    false,
		"collection_pages":   false,
	}
	if len(matches) != len(want) {
		t.Fatalf("collection table count = %d, want %d", len(matches), len(want))
	}
	for _, match := range matches {
		if _, ok := want[match[1]]; !ok {
			t.Fatalf("unapproved collection table %q", match[1])
		}
		want[match[1]] = true
	}
	for table, found := range want {
		if !found {
			t.Errorf("missing collection table %q", table)
		}
	}
	compact := compactSQL(migrations[4].SQL)
	for _, forbidden := range []string{
		"create table collection_tasks",
		"create table collection_rules",
		"create table collection_scheduler",
		"create table jobs",
		"create trigger",
	} {
		if strings.Contains(compact, forbidden) {
			t.Fatalf("collection migration contains forbidden structure %q", forbidden)
		}
	}
}

func TestCollectionMigrationConstraints(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	sqlText := migrations[4].SQL
	targets := compactSQL(tableDefinition(t, sqlText, "collection_targets"))
	for _, required := range []string{
		"target_id bigint generated always as identity",
		"kind text collate \"c\" not null",
		"platform text collate \"c\" not null",
		"side text collate \"c\"",
		"kind in ('catalog', 'summary')",
		"platform ~ '^[a-z][a-z0-9_.-]{0,31}$'",
		"kind = 'catalog' and platform = 'steam' and appid is not null and appid > 0 and side is null and period_microseconds is not null and period_microseconds > 0 and period_microseconds <= 9223372036854775",
		"kind = 'summary' and appid is null and side is not null and side in ('bid', 'ask') and period_microseconds is null",
		"desired_state in ('enabled', 'disabled')",
		"actual_state in ('starting', 'waiting', 'running', 'blocked', 'stopping', 'stopped', 'error')",
		"recovery_mode in ('', 'automatic', 'manual')",
		"reason_code in ('next_cycle', 'scheduler_opportunity', 'transient_failure')",
		"reason_code in ('no_combination', 'cooldown', 'egress_unavailable')",
		"reason_code in ('missing_rate_policy', 'session_invalid', 'invalid_config', 'interface_unverified')",
		"next_check_at > changed_at",
		"revision bigint not null default 1",
		"switch_version bigint not null default 1",
		"next_run_sequence bigint not null default 1",
		"check (revision > 0)",
		"check (switch_version > 0)",
		"check (next_run_sequence > 0)",
		"check (isfinite(changed_at))",
	} {
		assertContains(t, targets, required)
	}
	for _, forbidden := range []string{"product_id", "run_id", "page_sequence", "current_cursor"} {
		if strings.Contains(targets, forbidden) {
			t.Fatalf("collection target contains run/page field %q", forbidden)
		}
	}

	runs := compactSQL(tableDefinition(t, sqlText, "collection_runs"))
	for _, required := range []string{
		"run_id bigint generated always as identity",
		"target_id bigint",
		"kind text collate \"c\" not null",
		"platform text collate \"c\" not null",
		"appid bigint not null",
		"side text collate \"c\"",
		"product_id bigint",
		"switch_version bigint",
		"run_sequence bigint not null",
		"status text collate \"c\" not null",
		"completeness text collate \"c\"",
		"current_cursor bytea not null",
		"last_page_sequence bigint not null default 0",
		"unique (target_id, run_sequence)",
		"foreign key (target_id) references collection_targets (target_id)",
		"foreign key (product_id, appid) references steam_products (product_id, appid)",
		"kind in ('catalog', 'summary', 'detail')",
		"status in ('pending', 'running', 'succeeded', 'failed', 'stopped')",
		"completeness is null or completeness in ('complete', 'partial')",
		"reason_code = '' or reason_code in ( 'network_error', 'platform_error', 'timeout', 'login_invalid', 'parse_error', 'semantic_error', 'configuration_error', 'internal_error', 'process_restarted', 'switch_disabled', 'cancelled' )",
		"status = 'pending' and completeness is null and reason_code = '' and last_page_sequence = 0 and started_at is null and finished_at is null",
		"status = 'succeeded' and completeness is not null and completeness in ('complete', 'partial') and reason_code = '' and started_at is not null and finished_at is not null",
		"started_at is not null or completeness = 'partial'",
		"octet_length(current_cursor) <= 4096",
		"check (last_page_sequence >= 0)",
		"last_page_sequence = 0 or started_at is not null",
		"completeness is distinct from 'complete' or last_page_sequence > 0",
		"status <> 'succeeded' or last_page_sequence > 0",
		"isfinite(created_at)",
		"started_at is null or (isfinite(started_at) and started_at >= created_at)",
		"finished_at is null or (isfinite(finished_at) and finished_at >= created_at)",
	} {
		assertContains(t, runs, required)
	}
	if strings.Contains(runs, "period_microseconds") || strings.Contains(runs, "desired_state") || strings.Contains(runs, "actual_state") {
		t.Fatal("collection run contains target control state")
	}

	pages := compactSQL(tableDefinition(t, sqlText, "collection_pages"))
	for _, required := range []string{
		"primary key (run_id, page_sequence)",
		"foreign key (run_id) references collection_runs (run_id)",
		"check (page_sequence > 0)",
		"cursor_before bytea not null",
		"cursor_after bytea not null",
		"octet_length(cursor_before) <= 4096",
		"octet_length(cursor_after) <= 4096",
		"octet_length(payload_digest) = 32",
		"payload_digest <> decode(repeat('00', 32), 'hex')",
		"isfinite(collected_at) and isfinite(committed_at) and committed_at >= collected_at",
	} {
		assertContains(t, pages, required)
	}

	compact := compactSQL(sqlText)
	for _, required := range []string{
		"create unique index collection_targets_catalog_identity_key on collection_targets (platform, appid) where kind = 'catalog'",
		"create unique index collection_targets_summary_identity_key on collection_targets (platform, side) where kind = 'summary'",
		"create unique index collection_runs_active_catalog_key on collection_runs (target_id) where kind = 'catalog' and status in ('pending', 'running')",
		"create unique index collection_runs_active_summary_key on collection_runs (target_id, appid) where kind = 'summary' and status in ('pending', 'running')",
		"create unique index collection_runs_active_detail_key on collection_runs (platform, appid, side, product_id) where kind = 'detail' and status in ('pending', 'running')",
	} {
		assertContains(t, compact, required)
	}
}

func TestStorageMigrationMetadataIsIndependent(t *testing.T) {
	metadata := compactSQL(storageMigrationsTableSQL)
	for _, required := range []string{
		"create table if not exists buffgo_storage_migrations",
		"version text primary key",
		"checksum text not null",
		"applied_at timestamptz not null",
	} {
		assertContains(t, metadata, required)
	}
	if strings.Contains(metadata, "schema_migrations") {
		t.Fatal("storage migrations must not reuse the legacy migration table")
	}
}

func TestGameDirectionSummaryMigrationConstraints(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if migrations[6].Version != "000007_game_direction_summary" {
		t.Fatalf("version[6] = %q", migrations[6].Version)
	}
	compact := compactSQL(migrations[6].SQL)
	for _, required := range []string{
		"drop column assigned_platform",
		"create table node_direction_assignments",
		"foreign key (node_id, platform) references node_direction_assignments (node_id, platform)",
		"kind = 'summary' and appid is not null and appid > 0 and side is not null and side in ('bid', 'ask') and period_microseconds is null",
		"drop index collection_targets_catalog_identity_key",
		"create unique index collection_targets_summary_identity_key on collection_targets (platform, appid, side) where kind = 'summary'",
		"drop index collection_runs_active_catalog_key",
		"create unique index collection_runs_active_summary_key on collection_runs (target_id) where kind = 'summary' and status in ('pending', 'running')",
		"kind in ('summary', 'detail')",
		"check (kind = 'summary')",
	} {
		assertContains(t, compact, required)
	}
	for _, forbidden := range []string{
		"unique (node_id, assigned_platform)",
		"references access_nodes (node_id, assigned_platform)",
		"create unique index collection_targets_catalog_identity_key",
		"on collection_runs (target_id, appid)",
	} {
		if strings.Contains(compact, forbidden) {
			t.Fatalf("000007 still contains %q", forbidden)
		}
	}
}

func TestSteamProductIdentityMigration(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if migrations[7].Version != "000008_steam_product_identity" {
		t.Fatalf("version[7] = %q", migrations[7].Version)
	}
	compact := compactSQL(migrations[7].SQL)
	assertContains(t, compact, "create unique index steam_products_appid_name_key on steam_products (appid, name)")
}

func TestProxyProviderMigration(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if migrations[8].Version != "000009_proxy_providers" {
		t.Fatalf("version[8] = %q", migrations[8].Version)
	}
	compact := compactSQL(migrations[8].SQL)
	assertContains(t, compact, "create table short_pool_watermarks")
	assertContains(t, compact, "create table proxy_providers")
	assertContains(t, compact, "create table proxy_provider_regions")
	assertContains(t, compact, "credential_plaintext")
}

func TestPagePayloadMigration(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if migrations[9].Version != "000010_page_payloads" {
		t.Fatalf("version[9] = %q", migrations[9].Version)
	}
	compact := compactSQL(migrations[9].SQL)
	assertContains(t, compact, "create table collection_page_payloads")
	// 页被删时副本必须跟着走，否则会留下查不到归属的孤儿
	assertContains(t, compact, "on delete cascade")
	assertContains(t, compact, "collection_page_payloads_gzip_size")
}

func TestProductMediaAndPageActorMigration(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if migrations[10].Version != "000011_product_media_and_page_actor" {
		t.Fatalf("version[10] = %q", migrations[10].Version)
	}
	compact := compactSQL(migrations[10].SQL)
	assertContains(t, compact, "add column icon_path")
	assertContains(t, compact, "add column item_type")
	assertContains(t, compact, "add column name_color")
	assertContains(t, compact, "add column account_id")
	assertContains(t, compact, "add column exit_address")
	// 归属是诊断信息，账号被删不该影响采集历史，所以这里不能出现外键
	if strings.Contains(compact, "collection_pages") && strings.Contains(compact, "references platform_accounts") {
		t.Fatal("page attribution must not reference accounts")
	}
}

func TestTargetSortOrderMigration(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if migrations[11].Version != "000012_target_sort_order" {
		t.Fatalf("version[11] = %q", migrations[11].Version)
	}
	compact := compactSQL(migrations[11].SQL)
	// 默认值必须是接入采集时的固定顺序，否则这次迁移会改变已有目标的采集结果
	assertContains(t, compact, "add column sort_column text collate \"c\" not null default 'price'")
	assertContains(t, compact, "add column sort_dir text collate \"c\" not null default 'asc'")
	assertContains(t, compact, "sort_column in ('price', 'quantity', 'name')")
	assertContains(t, compact, "sort_dir in ('asc', 'desc')")
}

func tableDefinition(t *testing.T, sqlText, table string) string {
	t.Helper()
	marker := "CREATE TABLE " + table + " ("
	start := strings.Index(sqlText, marker)
	if start < 0 {
		t.Fatalf("missing table %q", table)
	}
	remainder := sqlText[start+len(marker):]
	end := strings.Index(remainder, "\n);")
	if end < 0 {
		t.Fatalf("unterminated table %q", table)
	}
	return remainder[:end]
}

func compactSQL(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func assertContains(t *testing.T, value, fragment string) {
	t.Helper()
	if !strings.Contains(value, fragment) {
		t.Errorf("missing SQL fragment %q", fragment)
	}
}

func assertPositiveFences(t *testing.T, table string) {
	t.Helper()
	for _, field := range []string{"switch_version", "run_sequence", "page_sequence"} {
		assertContains(t, table, field+" bigint not null")
		assertContains(t, table, "check ("+field+" > 0)")
	}
}
