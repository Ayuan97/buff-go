package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

func testRateLimitDDLConstraints(t *testing.T, dsn string) {
	t.Helper()
	_, db := migratedStore(t, dsn)
	ctx := t.Context()
	assertRateLimitDatabaseShape(t, db)

	rollingPolicyID := insertRawRatePolicy(t, db, "steam", "platform_window", "platform", "", "rolling_window")
	for name, expression := range map[string]string{
		"null element":      `ARRAY[NOW(), NULL]::TIMESTAMPTZ[]`,
		"infinite":          `ARRAY['infinity'::TIMESTAMPTZ]`,
		"multidimensional":  `ARRAY[[NOW(), NOW()], [NOW(), NOW()]]`,
		"over bound":        `array_fill(NOW(), ARRAY[1025])`,
		"out of order":      `ARRAY[NOW(), NOW() - INTERVAL '1 second']`,
		"after clock floor": `ARRAY[NOW() + INTERVAL '1 second']`,
	} {
		t.Run(name, func(t *testing.T) {
			statement := fmt.Sprintf(`
INSERT INTO rate_limit_states (
    policy_id, scope, kind, policy_revision, rolling_admitted_at,
    last_admitted_at, clock_floor_at
) VALUES ($1, 'platform', 'rolling_window', 1, %s, NOW(), NOW())`, expression)
			if _, err := db.ExecContext(ctx, statement, rollingPolicyID); err == nil {
				t.Fatal("invalid rolling state was accepted")
			}
		})
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, policy_revision, rolling_admitted_at,
    last_admitted_at, clock_floor_at
) VALUES (
    $1, 'platform', 'rolling_window', 1,
    ARRAY[NOW() - INTERVAL '2 seconds', NOW() - INTERVAL '1 second'],
    NOW() - INTERVAL '1 second', NOW()
)`, rollingPolicyID); err != nil {
		t.Fatalf("valid rolling state: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, policy_revision, rolling_admitted_at,
    last_admitted_at, clock_floor_at
) VALUES ($1, 'platform', 'rolling_window', 1, ARRAY[NOW()], NOW(), NOW())`, rollingPolicyID); err == nil {
		t.Fatal("duplicate concrete platform state was accepted")
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, policy_revision, next_allowed_at, clock_floor_at
) VALUES ($1, 'platform', 'min_interval', 1, NOW(), NOW())`, rollingPolicyID); err == nil {
		t.Fatal("state kind different from its policy was accepted")
	}

	ipPolicyID := insertRawRatePolicy(t, db, "steam", "ip_cooldown", "ip", "", "cooldown_only")
	for _, address := range []string{"10.0.0.1", "100.64.0.1", "127.0.0.1", "240.0.0.1", "::1", "fc00::1", "::ffff:1.1.1.1", "1.1.1.0/24"} {
		if _, err := db.ExecContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, exit_address, policy_revision, clock_floor_at
) VALUES ($1, 'ip', 'cooldown_only', $2::INET, 1, NOW())`, ipPolicyID, address); err == nil {
			t.Fatalf("invalid exit address %q was accepted", address)
		}
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, exit_address, policy_revision, clock_floor_at
) VALUES ($1, 'ip', 'cooldown_only', '1.1.1.1'::INET, 1, NOW())`, ipPolicyID); err != nil {
		t.Fatalf("public host address: %v", err)
	}
	badReasonPolicyID := insertRawRatePolicy(t, db, "steam", "bad_reason", "account", "", "cooldown_only")
	if _, err := db.ExecContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, account_id, policy_revision, clock_floor_at,
    cooldown_until, cooldown_observed_at, cooldown_reason_code
) VALUES (
    $1, 'account', 'cooldown_only', 1, 1, NOW(),
    NOW() + INTERVAL '1 minute', NOW(), 'timeout'
)`, badReasonPolicyID); err == nil {
		t.Fatal("unapproved cooldown reason was accepted")
	}

	shapePolicyID := insertRawRatePolicy(t, db, "steam", "state_shape", "platform", "", "min_interval")
	for name, columns := range map[string]string{
		"minimum interval next only": `next_allowed_at, clock_floor_at) VALUES ($1, 'platform', 'min_interval', 1, NOW() + INTERVAL '1 second', NOW())`,
		"minimum interval last only": `last_admitted_at, clock_floor_at) VALUES ($1, 'platform', 'min_interval', 1, NOW(), NOW())`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, policy_revision, `+columns, shapePolicyID); err == nil {
				t.Fatal("invalid minimum-interval state shape was accepted")
			}
		})
	}
	cooldownShapeID := insertRawRatePolicy(t, db, "steam", "cooldown_shape", "platform", "", "cooldown_only")
	if _, err := db.ExecContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, policy_revision, last_admitted_at, clock_floor_at
) VALUES ($1, 'platform', 'cooldown_only', 1, NOW(), NOW())`, cooldownShapeID); err == nil {
		t.Fatal("cooldown-only state with admission timestamp was accepted")
	}
	rollingShapeID := insertRawRatePolicy(t, db, "steam", "rolling_shape", "account", "", "rolling_window")
	for name, values := range map[string]string{
		"empty with last":  `ARRAY[]::TIMESTAMPTZ[], NOW(), NOW()`,
		"nonempty no last": `ARRAY[NOW()]::TIMESTAMPTZ[], NULL, NOW()`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, account_id, policy_revision,
    rolling_admitted_at, last_admitted_at, clock_floor_at
) VALUES ($1, 'account', 'rolling_window', 42, 1, `+values+`)`, rollingShapeID); err == nil {
				t.Fatal("invalid rolling state shape was accepted")
			}
		})
	}

	for _, statement := range []string{
		`INSERT INTO rate_limit_policies (
            platform, rule_key, scope, kind, active, changed_at, ready_at
         ) VALUES ('Steam', 'bad_platform', 'platform', 'cooldown_only', TRUE, NOW(), NOW())`,
		`INSERT INTO rate_limit_policies (
            platform, rule_key, scope, endpoint_class, kind, active, changed_at, ready_at
         ) VALUES ('steam', 'bad_endpoint', 'platform', 'summary', 'cooldown_only', TRUE, NOW(), NOW())`,
		`INSERT INTO rate_limit_policies (
            platform, rule_key, scope, kind, min_interval_microseconds,
            active, changed_at, ready_at
         ) VALUES ('steam', 'bad_shape', 'platform', 'cooldown_only', 1, TRUE, NOW(), NOW())`,
		`INSERT INTO rate_limit_policies (
            platform, rule_key, scope, kind, window_microseconds, max_requests,
            active, changed_at, ready_at
         ) VALUES ('steam', 'bad_count', 'platform', 'rolling_window', 1000, 1025, TRUE, NOW(), NOW())`,
	} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("invalid rate policy was accepted: %s", statement)
		}
	}
	if id := insertRawRatePolicy(t, db, "steam", "account_ip_endpoint", "account_ip", "summary", "cooldown_only"); id < 1 {
		t.Fatal("endpoint account-IP policy was not stored")
	}
}

func assertRateLimitDatabaseShape(t *testing.T, db *sql.DB) {
	t.Helper()
	wantColumns := map[string][]string{
		"rate_limit_policies": {
			"policy_id", "platform", "rule_key", "scope", "endpoint_class", "kind",
			"min_interval_microseconds", "window_microseconds", "max_requests",
			"default_cooldown_microseconds", "active", "revision", "changed_at", "ready_at",
		},
		"rate_limit_states": {
			"state_id", "policy_id", "scope", "kind", "account_id", "exit_address",
			"policy_revision", "next_allowed_at", "rolling_admitted_at", "last_admitted_at",
			"clock_floor_at", "cooldown_until", "cooldown_observed_at", "cooldown_reason_code",
		},
	}
	for table, want := range wantColumns {
		rows, err := db.QueryContext(t.Context(), `
SELECT column_name
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = $1
ORDER BY ordinal_position`, table)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0)
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				t.Fatal(err)
			}
			got = append(got, column)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s columns=%v want=%v", table, got, want)
		}
	}

	rows, err := db.QueryContext(t.Context(), `
SELECT source.relname, target.relname
FROM pg_constraint constraint_row
JOIN pg_class source ON source.oid = constraint_row.conrelid
JOIN pg_namespace namespace_row ON namespace_row.oid = source.relnamespace
JOIN pg_class target ON target.oid = constraint_row.confrelid
WHERE namespace_row.nspname = current_schema()
  AND source.relname IN ('rate_limit_policies', 'rate_limit_states')
  AND constraint_row.contype = 'f'`)
	if err != nil {
		t.Fatal(err)
	}
	foreignKeys := make([]string, 0)
	for rows.Next() {
		var source, target string
		if err := rows.Scan(&source, &target); err != nil {
			t.Fatal(err)
		}
		foreignKeys = append(foreignKeys, source+"->"+target)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(foreignKeys)
	wantForeignKeys := []string{"rate_limit_states->rate_limit_policies"}
	if fmt.Sprint(foreignKeys) != fmt.Sprint(wantForeignKeys) {
		t.Fatalf("rate-limit foreign keys=%v want=%v", foreignKeys, wantForeignKeys)
	}
	var indexDefinition string
	if err := db.QueryRowContext(t.Context(), `
SELECT indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND indexname = 'rate_limit_states_policy_clock_idx'`).Scan(&indexDefinition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexDefinition, "(policy_id, clock_floor_at DESC)") {
		t.Fatalf("clock index=%q", indexDefinition)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(), `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	planRows, err := tx.QueryContext(t.Context(), `
EXPLAIN (COSTS OFF)
SELECT MAX(high_water)
FROM (
    SELECT policy.changed_at AS high_water
    FROM rate_limit_policies policy
    WHERE policy.platform = $1
    UNION ALL
    SELECT (
        SELECT state.clock_floor_at
        FROM rate_limit_states state
        WHERE state.policy_id = policy.policy_id
        ORDER BY state.clock_floor_at DESC
        LIMIT 1
    ) AS high_water
    FROM rate_limit_policies policy
    WHERE policy.platform = $1
) platform_clock`, "steam")
	if err != nil {
		t.Fatal(err)
	}
	plan := ""
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	if err := planRows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := planRows.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "rate_limit_states_policy_clock_idx") {
		t.Fatalf("clock-floor plan does not use index:\n%s", plan)
	}
	assertRateLimitStateLookupPlans(t, tx)
}

func assertRateLimitStateLookupPlans(t *testing.T, tx *sql.Tx) {
	t.Helper()
	type lookupPlan struct {
		name      string
		scope     ratelimit.Scope
		subject   ratelimit.Subject
		wantIndex string
		loaded    bool
	}
	plans := []lookupPlan{
		{name: "platform", scope: ratelimit.ScopePlatform, subject: ratelimit.Subject{
			Scope: ratelimit.ScopePlatform, Platform: "steam",
		}, wantIndex: "rate_limit_states_global_key"},
		{name: "interface", scope: ratelimit.ScopeInterface, subject: ratelimit.Subject{
			Scope: ratelimit.ScopeInterface, Platform: "steam", EndpointClass: "summary",
		}, wantIndex: "rate_limit_states_global_key"},
		{name: "account", scope: ratelimit.ScopeAccount, subject: ratelimit.Subject{
			Scope: ratelimit.ScopeAccount, Platform: "steam", AccountID: 9001,
		}, wantIndex: "rate_limit_states_account_key"},
		{name: "ip", scope: ratelimit.ScopeIP, subject: ratelimit.Subject{
			Scope: ratelimit.ScopeIP, Platform: "steam", ExitAddress: netip.MustParseAddr("11.0.16.225"),
		}, wantIndex: "rate_limit_states_ip_key", loaded: true},
		{name: "account_ip", scope: ratelimit.ScopeAccountIP, subject: ratelimit.Subject{
			Scope: ratelimit.ScopeAccountIP, Platform: "steam", AccountID: 9001,
			ExitAddress: netip.MustParseAddr("11.0.16.225"),
		}, wantIndex: "rate_limit_states_account_ip_key", loaded: true},
	}
	for index := range plans {
		var policyID int64
		endpointClass := ""
		if plans[index].scope == ratelimit.ScopeInterface {
			endpointClass = "summary"
		}
		if err := tx.QueryRowContext(t.Context(), `
INSERT INTO rate_limit_policies (
    platform, rule_key, scope, endpoint_class, kind,
    active, revision, changed_at, ready_at
) VALUES ('steam', $1, $2, $3, 'cooldown_only', TRUE, 1, clock_timestamp(), clock_timestamp())
RETURNING policy_id`, "lookup_"+plans[index].name, string(plans[index].scope), endpointClass).Scan(&policyID); err != nil {
			t.Fatalf("%s lookup policy: %v", plans[index].name, err)
		}
		if plans[index].loaded {
			accountID := any(nil)
			if plans[index].scope == ratelimit.ScopeAccountIP {
				accountID = int64(plans[index].subject.AccountID)
			}
			if _, err := tx.ExecContext(t.Context(), `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, account_id, exit_address, policy_revision, clock_floor_at
)
SELECT $1, $2, 'cooldown_only', $3, '11.0.0.0'::INET + address_offset, 1, clock_timestamp()
FROM generate_series(1, 5000) AS address_offset`, policyID, string(plans[index].scope), accountID); err != nil {
				t.Fatalf("%s lookup states: %v", plans[index].name, err)
			}
		}
		policy := ratelimit.Policy{ID: ratelimit.PolicyID(policyID), Spec: ratelimit.PolicySpec{Scope: plans[index].scope}}
		query, args, err := rateLimitStateSelect(policy, plans[index].subject)
		if err != nil {
			t.Fatalf("%s production lookup: %v", plans[index].name, err)
		}
		planRows, err := tx.QueryContext(t.Context(), "EXPLAIN (ANALYZE, COSTS OFF) "+query, args...)
		if err != nil {
			t.Fatalf("%s subject plan: %v", plans[index].name, err)
		}
		plan := ""
		for planRows.Next() {
			var line string
			if err := planRows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan += line + "\n"
		}
		if err := planRows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := planRows.Close(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, plans[index].wantIndex) {
			t.Fatalf("%s subject plan does not use %s:\n%s", plans[index].name, plans[index].wantIndex, plan)
		}
		if plans[index].loaded && !strings.Contains(plan, "rows=1 loops=1") {
			t.Fatalf("%s subject plan did not select one of 5000 states:\n%s", plans[index].name, plan)
		}
	}
}

func insertRawRatePolicy(t *testing.T, db *sql.DB, platform, ruleKey, scope, endpointClass, kind string) int64 {
	t.Helper()
	var minInterval any
	var window any
	var maxRequests any
	switch kind {
	case "min_interval":
		minInterval = int64(1_000_000)
	case "rolling_window":
		window = int64(60_000_000)
		maxRequests = int64(10)
	}
	var id int64
	if err := db.QueryRowContext(t.Context(), `
INSERT INTO rate_limit_policies (
    platform, rule_key, scope, endpoint_class, kind,
    min_interval_microseconds, window_microseconds, max_requests,
    active, revision, changed_at, ready_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE, 1, NOW(), NOW())
RETURNING policy_id`, platform, ruleKey, scope, endpointClass, kind, minInterval, window, maxRequests).Scan(&id); err != nil {
		t.Fatalf("insert raw rate policy: %v", err)
	}
	return id
}

func testRateLimitStorage(t *testing.T, dsn string) {
	t.Run("policy CRUD", func(t *testing.T) { testRateLimitPolicyCRUD(t, dsn) })
	t.Run("fail closed and concurrent admission", func(t *testing.T) { testRateLimitConcurrentAdmission(t, dsn) })
	t.Run("subject lock isolation", func(t *testing.T) { testRateLimitSubjectLockIsolation(t, dsn) })
	t.Run("owner epoch fences admission only", func(t *testing.T) { testRateLimitOwnerEpochFence(t, dsn) })
	t.Run("delayed exact feedback", func(t *testing.T) { testRateLimitDelayedFeedback(t, dsn) })
	t.Run("feedback rollback retry", func(t *testing.T) { testRateLimitFeedbackRollbackRetry(t, dsn) })
	t.Run("replacement reset", func(t *testing.T) { testRateLimitReplacementReset(t, dsn) })
	t.Run("shared exit aggregation", func(t *testing.T) { testRateLimitSharedExit(t, dsn) })
	t.Run("same combination exit change", func(t *testing.T) { testRateLimitSameCombinationExitChange(t, dsn) })
	t.Run("endpoint shares platform budget", func(t *testing.T) { testRateLimitEndpointPlatformBudget(t, dsn) })
	t.Run("endpoint account-IP isolation", func(t *testing.T) { testRateLimitEndpointAccountIPIsolation(t, dsn) })
	t.Run("http 429 consecutive backoff", func(t *testing.T) { testRateLimitHTTP429EscalatesUntilSuccess(t, dsn) })
	t.Run("platforms isolate shared exit", func(t *testing.T) { testRateLimitPlatformIsolation(t, dsn) })
	t.Run("post-lock database clock", func(t *testing.T) { testRateLimitPostLockClock(t, dsn) })
	t.Run("strict rolling boundary", func(t *testing.T) { testRateLimitRollingBoundary(t, dsn) })
	t.Run("semantic integrity", func(t *testing.T) { testRateLimitSemanticIntegrity(t, dsn) })
	t.Run("closed storage errors", func(t *testing.T) { testClosedRateLimitErrors(t, dsn) })
}

func testRateLimitOwnerEpochFence(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	ctx := t.Context()
	oldLock, acquired, err := store.AcquireInstanceLock(ctx)
	if err != nil || !acquired {
		t.Fatalf("old instance lock acquired=%v err=%v", acquired, err)
	}
	old := oldLock.(*collectionInstanceLock)
	oldCtx := collection.WithOwnerEpoch(ctx, old.Epoch())

	fixture := newRateLimitRequestFixture(t, store, "owner-epoch", netip.MustParseAddr("1.1.1.1"))
	createRequiredEndpointRate(t, store, db, "steam", "summary")
	decision, err := store.AdmitRateLimit(oldCtx, fixture.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("current owner admission allowed=%v err=%v", decision.Allowed(), err)
	}
	if _, err := old.conn.ExecContext(ctx, `SELECT pg_advisory_unlock_all()`); err != nil {
		t.Fatal(err)
	}
	successor, acquired, err := store.AcquireInstanceLock(ctx)
	if err != nil || !acquired {
		t.Fatalf("successor instance lock acquired=%v err=%v", acquired, err)
	}
	defer func() { _ = successor.Release(t.Context()) }()

	if _, err := store.AdmitRateLimit(oldCtx, fixture.request); !errors.Is(err, collection.ErrOwnerFence) {
		t.Fatalf("stale owner admission error = %v, want owner fence", err)
	}
	if err := store.ApplyRateLimitFeedback(
		oldCtx, decision.Admission(), []ratelimit.Scope{ratelimit.ScopeAccountIP},
		ratelimit.ReasonHTTP429, time.Minute,
	); err != nil {
		t.Fatalf("real feedback from old admission must survive takeover: %v", err)
	}
	if err := oldLock.Release(ctx); err == nil {
		t.Fatal("lost old lock release must report failure")
	}
}

func testRateLimitPolicyCRUD(t *testing.T, dsn string) {
	store, _ := newRateLimitTestStore(t, dsn)
	ctx := t.Context()
	spec := ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_total", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindMinInterval, MinInterval: 10 * time.Second,
		DefaultCooldown: time.Minute,
	}
	policy, err := store.CreateRateLimitPolicy(ctx, spec, true)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != 1 || !policy.Enabled || policy.Spec != spec || policy.ReadyAt.IsZero() {
		t.Fatalf("created policy=%+v", policy)
	}
	read, found, err := store.RateLimitPolicy(ctx, policy.ID)
	if err != nil || !found || read != policy {
		t.Fatalf("read policy=%+v found=%v err=%v", read, found, err)
	}
	listed, err := store.ListRateLimitPolicies(ctx)
	if err != nil || len(listed) != 1 || listed[0] != policy {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	if _, err := store.CreateRateLimitPolicy(ctx, spec, true); !errors.Is(err, ErrRateLimitPolicyConflict) {
		t.Fatalf("duplicate policy error=%v", err)
	}
	unchanged, err := store.ReplaceRateLimitPolicy(ctx, policy.ID, 1, spec, true)
	if err != nil || unchanged != policy {
		t.Fatalf("no-op replacement=%+v err=%v", unchanged, err)
	}
	changedIdentity := spec
	changedIdentity.RuleKey = "other_key"
	if _, err := store.ReplaceRateLimitPolicy(ctx, policy.ID, 1, changedIdentity, true); !errors.Is(err, ErrRateLimitPolicyIdentityConflict) {
		t.Fatalf("identity replacement error=%v", err)
	}
	replacement := spec
	replacement.MinInterval = 20 * time.Second
	replaced, err := store.ReplaceRateLimitPolicy(ctx, policy.ID, 1, replacement, false)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Revision != 2 || replaced.Enabled || replaced.Spec != replacement || !replaced.ReadyAt.After(policy.ReadyAt) {
		t.Fatalf("replaced policy=%+v", replaced)
	}
	if _, err := store.ReplaceRateLimitPolicy(ctx, policy.ID, 1, replacement, true); !errors.Is(err, ErrRateLimitPolicyRevisionConflict) {
		t.Fatalf("stale replacement error=%v", err)
	}
	replaceResults := make(chan rateLimitReplaceResult, 2)
	start := make(chan struct{})
	for index := range 2 {
		candidate := replacement
		candidate.MinInterval += time.Duration(index+1) * time.Second
		go func() {
			<-start
			updated, err := store.ReplaceRateLimitPolicy(context.Background(), policy.ID, 2, candidate, true)
			replaceResults <- rateLimitReplaceResult{policy: updated, err: err}
		}()
	}
	close(start)
	succeeded, conflicted := 0, 0
	for range 2 {
		result := <-replaceResults
		switch {
		case result.err == nil && result.policy.Revision == 3:
			succeeded++
		case errors.Is(result.err, ErrRateLimitPolicyRevisionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent replacement result=%+v err=%v", result.policy, result.err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("replacement succeeded=%d conflicted=%d", succeeded, conflicted)
	}

	var policyClockHighWater time.Time
	if err := store.db.QueryRowContext(ctx, `
UPDATE rate_limit_policies policy
SET changed_at = marker.at,
    ready_at = marker.at + INTERVAL '1 minute'
FROM (SELECT clock_timestamp() + INTERVAL '5 minutes' AS at) marker
WHERE policy.policy_id = $1
RETURNING policy.changed_at`, int64(policy.ID)).Scan(&policyClockHighWater); err != nil {
		t.Fatal(err)
	}
	policyClockHighWater = policyClockHighWater.UTC()
	newPolicy := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "no_state_clock", Scope: ratelimit.ScopeAccount,
		Kind: ratelimit.KindRollingWindow, Window: 30 * time.Second, MaxRequests: 5,
	})
	if !newPolicy.ReadyAt.Equal(policyClockHighWater.Add(30 * time.Second)) {
		t.Fatalf("new policy ready_at=%s want=%s", newPolicy.ReadyAt, policyClockHighWater.Add(30*time.Second))
	}
	newSpec := newPolicy.Spec
	newSpec.Window = 45 * time.Second
	newPolicy, err = store.ReplaceRateLimitPolicy(ctx, newPolicy.ID, 1, newSpec, true)
	if err != nil {
		t.Fatal(err)
	}
	if !newPolicy.ReadyAt.Equal(policyClockHighWater.Add(45 * time.Second)) {
		t.Fatalf("replaced no-state ready_at=%s want=%s", newPolicy.ReadyAt, policyClockHighWater.Add(45*time.Second))
	}
	if _, found, err := store.RateLimitPolicy(ctx, ratelimit.PolicyID(999999)); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
}

func testRateLimitConcurrentAdmission(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	fixture := newRateLimitRequestFixture(t, store, "one", netip.MustParseAddr("1.1.1.1"))
	ctx := t.Context()
	if _, err := store.AdmitRateLimit(ctx, fixture.request); !errors.Is(err, ratelimit.ErrPolicyUnavailable) {
		t.Fatalf("no-policy admission error=%v", err)
	}
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_interval", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindMinInterval, MinInterval: 10 * time.Second,
	})
	makeRateLimitPolicyReady(t, db, platform.ID)
	if _, err := store.AdmitRateLimit(ctx, fixture.request); !errors.Is(err, ratelimit.ErrPolicyUnavailable) {
		t.Fatalf("missing interface profile error=%v", err)
	}
	profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	makeRateLimitPolicyReady(t, db, profile.ID)
	if _, err := store.AdmitRateLimit(ctx, fixture.request); !errors.Is(err, ratelimit.ErrPolicyUnavailable) {
		t.Fatalf("missing exact account-IP rate error=%v", err)
	}
	required := createRequiredEndpointRate(t, store, db, "steam", "summary")

	start := make(chan struct{})
	results := make(chan rateLimitAdmissionResult, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			decision, err := store.AdmitRateLimit(context.Background(), fixture.request)
			results <- rateLimitAdmissionResult{decision: decision, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	allowed, blocked := 0, 0
	var admission ratelimit.Admission
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent admission error=%v", result.err)
		}
		if err := result.decision.Validate(); err != nil {
			t.Fatalf("invalid decision=%v", err)
		}
		if result.decision.Allowed() {
			allowed++
			admission = result.decision.Admission()
		} else {
			blocked++
		}
	}
	if allowed != 1 || blocked != 1 {
		t.Fatalf("allowed=%d blocked=%d", allowed, blocked)
	}
	if admission.AdmittedAt().IsZero() {
		t.Fatal("allowed decision has no admission")
	}
	states, err := store.ListRateLimitStates(ctx, platform.ID)
	if err != nil || len(states) != 1 || states[0].LastAdmittedAt == nil || states[0].NextAllowedAt == nil {
		t.Fatalf("platform states=%+v err=%v", states, err)
	}
	profileStates, err := store.ListRateLimitStates(ctx, profile.ID)
	if err != nil || len(profileStates) != 1 || profileStates[0].LastAdmittedAt != nil {
		t.Fatalf("profile states=%+v err=%v", profileStates, err)
	}
	if err := store.ApplyRateLimitFeedback(ctx, admission, []ratelimit.Scope{ratelimit.ScopeInterface}, ratelimit.ReasonHTTP429, time.Minute); err != nil {
		t.Fatal(err)
	}
	profileStates, err = store.ListRateLimitStates(ctx, profile.ID)
	if err != nil || len(profileStates) != 1 || profileStates[0].CooldownUntil == nil {
		t.Fatalf("persisted profile cooldown=%+v err=%v", profileStates, err)
	}
	persistedDeadline := *profileStates[0].CooldownUntil

	restarted, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := restarted.AdmitRateLimit(ctx, fixture.request)
	if err != nil || decision.Allowed() {
		t.Fatalf("restart decision allowed=%v err=%v", decision.Allowed(), err)
	}
	foundCooldown := false
	for _, blocker := range decision.Blockers() {
		if blocker.PolicyID() == profile.ID && blocker.Scope() == ratelimit.ScopeInterface &&
			blocker.Reason() == ratelimit.BlockReasonCooldown && blocker.RetryAt().Equal(persistedDeadline) {
			foundCooldown = true
		}
	}
	if !foundCooldown {
		t.Fatalf("restart cooldown blocker missing: %+v", decision.Blockers())
	}
	if err := restarted.ApplyRateLimitFeedback(ctx, admission, []ratelimit.Scope{ratelimit.ScopeInterface}, ratelimit.ReasonHTTP429, time.Minute); !errors.Is(err, ratelimit.ErrInvalidAdmission) {
		t.Fatalf("other signer feedback error=%v", err)
	}
	profileStates, err = store.ListRateLimitStates(ctx, profile.ID)
	if err != nil || profileStates[0].CooldownUntil == nil || !profileStates[0].CooldownUntil.Equal(persistedDeadline) {
		t.Fatalf("rejected old ticket changed deadline=%+v err=%v", profileStates, err)
	}
	required, err = store.ReplaceRateLimitPolicy(ctx, required.ID, required.Revision, required.Spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdmitRateLimit(ctx, fixture.request); !errors.Is(err, ratelimit.ErrPolicyUnavailable) {
		t.Fatalf("disabled exact account-IP requirement error=%v", err)
	}
	required, err = store.ReplaceRateLimitPolicy(ctx, required.ID, required.Revision, required.Spec, true)
	if err != nil {
		t.Fatal(err)
	}
	makeRateLimitPolicyReady(t, db, required.ID)
	profile, err = store.ReplaceRateLimitPolicy(ctx, profile.ID, profile.Revision, profile.Spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AdmitRateLimit(ctx, fixture.request); err != nil {
		t.Fatalf("disabled legacy interface profile error=%v", err)
	}
}

func testRateLimitSubjectLockIsolation(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	first := newRateLimitRequestFixture(t, store, "subject-lock-a", netip.MustParseAddr("1.1.1.1"))
	second := newRateLimitRequestFixture(t, store, "subject-lock-b", netip.MustParseAddr("2.2.2.2"))
	policy := createRequiredEndpointRate(t, store, db, "steam", "summary")
	firstSubject, err := first.request.Subject(policy.Spec.Scope)
	if err != nil {
		t.Fatal(err)
	}

	gate, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Rollback() }()
	if err := lockRateLimitPolicySubject(t.Context(), gate, policy.ID, firstSubject); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstResult := make(chan rateLimitAdmissionResult, 1)
	go func() {
		decision, err := store.AdmitRateLimit(ctx, first.request)
		firstResult <- rateLimitAdmissionResult{decision: decision, err: err}
	}()
	select {
	case result := <-firstResult:
		t.Fatalf("locked identity A returned early: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}

	secondResult := make(chan rateLimitAdmissionResult, 1)
	go func() {
		decision, err := store.AdmitRateLimit(ctx, second.request)
		secondResult <- rateLimitAdmissionResult{decision: decision, err: err}
	}()
	select {
	case result := <-secondResult:
		if result.err != nil || !result.decision.Allowed() {
			t.Fatalf("independent identity B allowed=%v err=%v", result.decision.Allowed(), result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("independent identity B was serialized behind identity A")
	}
	select {
	case result := <-firstResult:
		t.Fatalf("identity A stopped blocking before its lock was released: %+v", result)
	default:
	}

	if err := gate.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-firstResult:
		if result.err != nil || !result.decision.Allowed() {
			t.Fatalf("identity A after unlock allowed=%v err=%v", result.decision.Allowed(), result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("identity A did not resume after its lock was released")
	}
}

func testRateLimitDelayedFeedback(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	fixture := newRateLimitRequestFixture(t, store, "feedback", netip.MustParseAddr("1.1.1.1"))
	createRequiredEndpointRate(t, store, db, "steam", "summary")
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_window", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindRollingWindow, Window: time.Minute, MaxRequests: 100,
	})
	profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	accountA := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "account_a", Scope: ratelimit.ScopeAccount,
		Kind: ratelimit.KindCooldownOnly, DefaultCooldown: 30 * time.Second,
	})
	for _, id := range []ratelimit.PolicyID{platform.ID, profile.ID, accountA.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}
	decision, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("initial admission allowed=%v err=%v", decision.Allowed(), err)
	}
	admission := decision.Admission()

	accountB := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "account_b", Scope: ratelimit.ScopeAccount,
		Kind: ratelimit.KindCooldownOnly, DefaultCooldown: time.Minute,
	})
	makeRateLimitPolicyReady(t, db, accountB.ID)
	replacement := accountA.Spec
	replacement.DefaultCooldown = 90 * time.Second
	accountA, err = store.ReplaceRateLimitPolicy(t.Context(), accountA.ID, accountA.Revision, replacement, true)
	if err != nil || accountA.Revision != 2 {
		t.Fatalf("account replacement=%+v err=%v", accountA, err)
	}
	var feedbackFloor time.Time
	if err := db.QueryRowContext(t.Context(), `
UPDATE rate_limit_states
SET clock_floor_at = clock_floor_at + INTERVAL '2 minutes'
WHERE policy_id = $1
RETURNING clock_floor_at`, int64(accountA.ID)).Scan(&feedbackFloor); err != nil {
		t.Fatal(err)
	}
	feedbackFloor = feedbackFloor.UTC()

	commandScopes := []ratelimit.Scope{ratelimit.ScopeAccount}
	feedbackErrors := make(chan error, 2)
	for range 2 {
		go func() {
			feedbackErrors <- store.ApplyRateLimitFeedback(context.Background(), admission, commandScopes, ratelimit.ReasonHTTP429, 0)
		}()
	}
	for range 2 {
		if err := <-feedbackErrors; err != nil {
			t.Fatalf("concurrent identical feedback=%v", err)
		}
	}
	states, err := store.ListRateLimitStates(t.Context(), accountA.ID)
	if err != nil || len(states) != 1 || states[0].PolicyRevision != 2 || states[0].CooldownUntil == nil || states[0].CooldownObservedAt == nil {
		t.Fatalf("account A states=%+v err=%v", states, err)
	}
	if got := states[0].CooldownUntil.Sub(*states[0].CooldownObservedAt); got != 30*time.Second {
		t.Fatalf("old admission fallback duration=%s want=30s", got)
	}
	if !states[0].CooldownObservedAt.Equal(feedbackFloor) {
		t.Fatalf("feedback observed_at=%s want selected state floor=%s", states[0].CooldownObservedAt, feedbackFloor)
	}
	firstUntil := *states[0].CooldownUntil
	firstObserved := *states[0].CooldownObservedAt
	if err := store.ApplyRateLimitFeedback(t.Context(), admission, commandScopes, ratelimit.ReasonHTTP429, 0); err != nil {
		t.Fatalf("identical feedback retry=%v", err)
	}
	states, err = store.ListRateLimitStates(t.Context(), accountA.ID)
	if err != nil || !states[0].CooldownUntil.Equal(firstUntil) || !states[0].CooldownObservedAt.Equal(firstObserved) {
		t.Fatalf("feedback retry changed fact states=%+v err=%v", states, err)
	}
	if err := store.ApplyRateLimitFeedback(t.Context(), admission, commandScopes, ratelimit.ReasonHTTP429, time.Second); !errors.Is(err, ratelimit.ErrFeedbackConflict) {
		t.Fatalf("conflicting feedback error=%v", err)
	}
	states, err = store.ListRateLimitStates(t.Context(), accountB.ID)
	if err != nil || len(states) != 0 {
		t.Fatalf("post-admission same-scope policy was updated states=%+v err=%v", states, err)
	}
}

func testRateLimitReplacementReset(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	fixture := newRateLimitRequestFixture(t, store, "replace", netip.MustParseAddr("1.1.1.1"))
	createRequiredEndpointRate(t, store, db, "steam", "summary")
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_interval", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindMinInterval, MinInterval: 10 * time.Second,
		DefaultCooldown: time.Minute,
	})
	profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	for _, id := range []ratelimit.PolicyID{platform.ID, profile.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}
	decision, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("initial admission allowed=%v err=%v", decision.Allowed(), err)
	}
	if err := store.ApplyRateLimitFeedback(t.Context(), decision.Admission(), []ratelimit.Scope{ratelimit.ScopePlatform}, ratelimit.ReasonRiskControl, 0); err != nil {
		t.Fatal(err)
	}
	before, err := store.ListRateLimitStates(t.Context(), platform.ID)
	if err != nil || len(before) != 1 || before[0].NextAllowedAt == nil || before[0].CooldownUntil == nil {
		t.Fatalf("before replacement=%+v err=%v", before, err)
	}
	var futureFloor time.Time
	if err := db.QueryRowContext(t.Context(), `
UPDATE rate_limit_states
SET clock_floor_at = clock_floor_at + INTERVAL '2 minutes'
WHERE policy_id = $1
RETURNING clock_floor_at`, int64(platform.ID)).Scan(&futureFloor); err != nil {
		t.Fatal(err)
	}
	futureFloor = futureFloor.UTC()
	newPolicy := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "new_account_window", Scope: ratelimit.ScopeAccount,
		Kind: ratelimit.KindRollingWindow, Window: 30 * time.Second, MaxRequests: 10,
	})
	if !newPolicy.ReadyAt.Equal(futureFloor.Add(newPolicy.Spec.Window)) {
		t.Fatalf("new policy ready_at=%s want=%s", newPolicy.ReadyAt, futureFloor.Add(newPolicy.Spec.Window))
	}
	replacement := platform.Spec
	replacement.MinInterval = 20 * time.Second
	replacement.DefaultCooldown = 2 * time.Minute
	platform, err = store.ReplaceRateLimitPolicy(t.Context(), platform.ID, 1, replacement, true)
	if err != nil {
		t.Fatal(err)
	}
	if !platform.ReadyAt.Equal(futureFloor.Add(replacement.MinInterval)) {
		t.Fatalf("replacement ready_at=%s want=%s", platform.ReadyAt, futureFloor.Add(replacement.MinInterval))
	}
	after, err := store.ListRateLimitStates(t.Context(), platform.ID)
	if err != nil || len(after) != 1 {
		t.Fatalf("after replacement=%+v err=%v", after, err)
	}
	if after[0].PolicyRevision != platform.Revision || after[0].NextAllowedAt != nil || after[0].LastAdmittedAt != nil {
		t.Fatalf("pacing state was not reset=%+v", after[0])
	}
	if after[0].CooldownUntil == nil || !after[0].CooldownUntil.Equal(*before[0].CooldownUntil) ||
		after[0].CooldownObservedAt == nil || !after[0].CooldownObservedAt.Equal(*before[0].CooldownObservedAt) {
		t.Fatalf("cooldown was not preserved before=%+v after=%+v", before[0], after[0])
	}
	decision, err = store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || decision.Allowed() {
		t.Fatalf("clock-floor warmup admission allowed=%v err=%v", decision.Allowed(), err)
	}
	foundWarmup := false
	foundNewPolicyWarmup := false
	for _, blocker := range decision.Blockers() {
		if blocker.PolicyID() == platform.ID && blocker.Reason() == ratelimit.BlockReasonWarmup && blocker.RetryAt().Equal(platform.ReadyAt) {
			foundWarmup = true
		}
		if blocker.PolicyID() == newPolicy.ID && blocker.Reason() == ratelimit.BlockReasonWarmup && blocker.RetryAt().Equal(newPolicy.ReadyAt) {
			foundNewPolicyWarmup = true
		}
	}
	if !foundWarmup || !foundNewPolicyWarmup {
		t.Fatalf("replacement warmup blocker missing: %+v", decision.Blockers())
	}
}

func testRateLimitFeedbackRollbackRetry(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	fixture := newRateLimitRequestFixture(t, store, "feedback-retry", netip.MustParseAddr("1.1.1.1"))
	createRequiredEndpointRate(t, store, db, "steam", "summary")
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_window", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindRollingWindow, Window: time.Minute, MaxRequests: 100,
	})
	profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	for _, id := range []ratelimit.PolicyID{platform.ID, profile.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}
	decision, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("initial admission allowed=%v err=%v", decision.Allowed(), err)
	}
	if _, err := db.ExecContext(t.Context(), `
CREATE FUNCTION fail_rate_limit_feedback() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.cooldown_until IS DISTINCT FROM OLD.cooldown_until THEN
        RAISE EXCEPTION 'synthetic feedback write failure';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER fail_rate_limit_feedback
BEFORE UPDATE ON rate_limit_states
FOR EACH ROW EXECUTE FUNCTION fail_rate_limit_feedback()`); err != nil {
		t.Fatal(err)
	}
	command := func() error {
		return store.ApplyRateLimitFeedback(t.Context(), decision.Admission(), []ratelimit.Scope{ratelimit.ScopePlatform}, ratelimit.ReasonHTTP429, time.Minute)
	}
	if err := command(); !errors.Is(err, ErrRateLimitStorage) || err.Error() != ErrRateLimitStorage.Error() {
		t.Fatalf("failed feedback error=%v", err)
	}
	if _, err := db.ExecContext(t.Context(), `
DROP TRIGGER fail_rate_limit_feedback ON rate_limit_states;
DROP FUNCTION fail_rate_limit_feedback()`); err != nil {
		t.Fatal(err)
	}
	var laterFloor time.Time
	if err := db.QueryRowContext(t.Context(), `
UPDATE rate_limit_states
SET clock_floor_at = clock_floor_at + INTERVAL '2 minutes'
WHERE policy_id = $1
RETURNING clock_floor_at`, int64(platform.ID)).Scan(&laterFloor); err != nil {
		t.Fatal(err)
	}
	laterFloor = laterFloor.UTC()
	if err := command(); err != nil {
		t.Fatalf("feedback retry=%v", err)
	}
	states, err := store.ListRateLimitStates(t.Context(), platform.ID)
	if err != nil || len(states) != 1 || states[0].CooldownObservedAt == nil || states[0].CooldownUntil == nil {
		t.Fatalf("feedback retry states=%+v err=%v", states, err)
	}
	if !states[0].CooldownObservedAt.Before(laterFloor) || !states[0].ClockFloorAt.Equal(laterFloor) {
		t.Fatalf("feedback observed=%s floor=%s want later floor=%s", states[0].CooldownObservedAt, states[0].ClockFloorAt, laterFloor)
	}
	if states[0].CooldownUntil.Sub(*states[0].CooldownObservedAt) != time.Minute {
		t.Fatalf("feedback retry cooldown=%s", states[0].CooldownUntil.Sub(*states[0].CooldownObservedAt))
	}
}

func testRateLimitSharedExit(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	first := newRateLimitRequestFixture(t, store, "shared-one", netip.MustParseAddr("1.1.1.1"))
	second := newRateLimitRequestFixture(t, store, "shared-two", netip.MustParseAddr("1.1.1.1"))
	other := newRateLimitRequestFixture(t, store, "other-exit", netip.MustParseAddr("8.8.8.8"))
	createRequiredEndpointRate(t, store, db, "steam", "summary")
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_window", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindRollingWindow, Window: time.Minute, MaxRequests: 100,
	})
	profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	ipPolicy := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "exit_interval", Scope: ratelimit.ScopeIP,
		Kind: ratelimit.KindMinInterval, MinInterval: time.Minute,
	})
	accountPolicy := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "account_cooldown", Scope: ratelimit.ScopeAccount,
		Kind: ratelimit.KindCooldownOnly,
	})
	accountIPPolicy := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "account_exit_cooldown", Scope: ratelimit.ScopeAccountIP,
		Kind: ratelimit.KindCooldownOnly,
	})
	for _, id := range []ratelimit.PolicyID{platform.ID, profile.ID, ipPolicy.ID, accountPolicy.ID, accountIPPolicy.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}
	decision, err := store.AdmitRateLimit(t.Context(), first.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("first admission allowed=%v err=%v", decision.Allowed(), err)
	}
	decision, err = store.AdmitRateLimit(t.Context(), second.request)
	if err != nil || decision.Allowed() {
		t.Fatalf("shared exit admission allowed=%v err=%v", decision.Allowed(), err)
	}
	decision, err = store.AdmitRateLimit(t.Context(), other.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("other exit admission allowed=%v err=%v", decision.Allowed(), err)
	}
	states, err := store.ListRateLimitStates(t.Context(), ipPolicy.ID)
	if err != nil || len(states) != 2 {
		t.Fatalf("IP states=%+v err=%v", states, err)
	}
	addresses := map[netip.Addr]bool{}
	for _, state := range states {
		addresses[state.Subject.ExitAddress] = true
	}
	if !addresses[netip.MustParseAddr("1.1.1.1")] || !addresses[netip.MustParseAddr("8.8.8.8")] {
		t.Fatalf("stored exits=%v", addresses)
	}
	accountStates, err := store.ListRateLimitStates(t.Context(), accountPolicy.ID)
	if err != nil || len(accountStates) != 2 {
		t.Fatalf("account states=%+v err=%v", accountStates, err)
	}
	accountIPStates, err := store.ListRateLimitStates(t.Context(), accountIPPolicy.ID)
	if err != nil || len(accountIPStates) != 2 {
		t.Fatalf("account-IP states=%+v err=%v", accountIPStates, err)
	}
	for _, state := range accountIPStates {
		if state.Subject.AccountID == 0 || !state.Subject.ExitAddress.IsValid() {
			t.Fatalf("invalid account-IP subject=%+v", state.Subject)
		}
	}
	platformStates, err := store.ListRateLimitStates(t.Context(), platform.ID)
	if err != nil || len(platformStates) != 1 {
		t.Fatalf("platform states=%+v err=%v", platformStates, err)
	}
	interfaceStates, err := store.ListRateLimitStates(t.Context(), profile.ID)
	if err != nil || len(interfaceStates) != 1 {
		t.Fatalf("interface states=%+v err=%v", interfaceStates, err)
	}
}

func testRateLimitSemanticIntegrity(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	fixture := newRateLimitRequestFixture(t, store, "integrity", netip.MustParseAddr("1.1.1.1"))
	createRequiredEndpointRate(t, store, db, "steam", "summary")
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_interval", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindMinInterval, MinInterval: 10 * time.Second,
	})
	profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	for _, id := range []ratelimit.PolicyID{platform.ID, profile.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}
	decision, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("initial admission allowed=%v err=%v", decision.Allowed(), err)
	}
	if _, err := db.ExecContext(t.Context(), `
UPDATE rate_limit_states
SET clock_floor_at = next_allowed_at
WHERE policy_id = $1`, int64(platform.ID)); err != nil {
		t.Fatal(err)
	}
	decision, err = store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("minimum-interval equality allowed=%v err=%v", decision.Allowed(), err)
	}
	if _, err := db.ExecContext(t.Context(), `
UPDATE rate_limit_states
SET next_allowed_at = last_admitted_at + INTERVAL '20 seconds'
WHERE policy_id = $1`, int64(platform.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListRateLimitStates(t.Context(), platform.ID); !errors.Is(err, ErrRateLimitIntegrity) {
		t.Fatalf("semantic corruption list error=%v", err)
	}
	if _, err := store.AdmitRateLimit(t.Context(), fixture.request); !errors.Is(err, ErrRateLimitIntegrity) {
		t.Fatalf("semantic corruption admit error=%v", err)
	}
}

func testRateLimitRollingBoundary(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	fixture := newRateLimitRequestFixture(t, store, "rolling", netip.MustParseAddr("1.1.1.1"))
	createRequiredEndpointRate(t, store, db, "steam", "summary")
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "strict_window", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindRollingWindow, Window: time.Minute, MaxRequests: 2,
	})
	profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	for _, id := range []ratelimit.PolicyID{platform.ID, profile.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}
	decision, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("initial admission allowed=%v err=%v", decision.Allowed(), err)
	}
	var logicalNow time.Time
	if err := db.QueryRowContext(t.Context(), `SELECT clock_timestamp() + INTERVAL '2 minutes'`).Scan(&logicalNow); err != nil {
		t.Fatal(err)
	}
	logicalNow = logicalNow.UTC()
	if _, err := db.ExecContext(t.Context(), `
UPDATE rate_limit_states
SET rolling_admitted_at = ARRAY[
        $2::TIMESTAMPTZ - INTERVAL '1 minute',
        $2::TIMESTAMPTZ - INTERVAL '1 minute' + INTERVAL '1 microsecond'
    ],
    last_admitted_at = $2::TIMESTAMPTZ - INTERVAL '1 minute' + INTERVAL '1 microsecond',
    clock_floor_at = $2
WHERE policy_id = $1`, int64(platform.ID), logicalNow); err != nil {
		t.Fatal(err)
	}
	decision, err = store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("strict rolling boundary allowed=%v err=%v", decision.Allowed(), err)
	}
	states, err := store.ListRateLimitStates(t.Context(), platform.ID)
	if err != nil || len(states) != 1 || len(states[0].RollingAdmittedAt) != 2 {
		t.Fatalf("rolling states=%+v err=%v", states, err)
	}
	if !states[0].RollingAdmittedAt[0].Equal(logicalNow.Add(-time.Minute+time.Microsecond)) ||
		!states[0].RollingAdmittedAt[1].Equal(logicalNow) {
		t.Fatalf("strict rolling timestamps=%v", states[0].RollingAdmittedAt)
	}
}

func testRateLimitEndpointPlatformBudget(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	bid := newRateLimitRequestFixtureFor(t, store, "bid", netip.MustParseAddr("1.1.1.1"), "steam", "bid")
	ask := newRateLimitRequestFixtureFor(t, store, "ask", netip.MustParseAddr("8.8.8.8"), "steam", "ask")
	createRequiredEndpointRate(t, store, db, "steam", "bid")
	createRequiredEndpointRate(t, store, db, "steam", "ask")
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_total", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindMinInterval, MinInterval: time.Minute,
	})
	bidProfile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "bid_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "bid", Kind: ratelimit.KindCooldownOnly,
	})
	askProfile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "ask_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "ask", Kind: ratelimit.KindCooldownOnly,
	})
	for _, id := range []ratelimit.PolicyID{platform.ID, bidProfile.ID, askProfile.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}
	decision, err := store.AdmitRateLimit(t.Context(), bid.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("bid admission allowed=%v err=%v", decision.Allowed(), err)
	}
	decision, err = store.AdmitRateLimit(t.Context(), ask.request)
	if err != nil || decision.Allowed() {
		t.Fatalf("ask admission allowed=%v err=%v", decision.Allowed(), err)
	}
	foundPlatformBudget := false
	for _, blocker := range decision.Blockers() {
		if blocker.PolicyID() == platform.ID && blocker.Reason() == ratelimit.BlockReasonBudget {
			foundPlatformBudget = true
		}
	}
	if !foundPlatformBudget {
		t.Fatalf("platform blocker missing: %+v", decision.Blockers())
	}
	states, err := store.ListRateLimitStates(t.Context(), askProfile.ID)
	if err != nil || len(states) != 0 {
		t.Fatalf("blocked ask consumed profile state=%+v err=%v", states, err)
	}
}

func testRateLimitEndpointAccountIPIsolation(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	first := newRateLimitRequestFixtureFor(t, store, "endpoint-first", netip.MustParseAddr("1.1.1.1"), "steam", "ask")
	second := newRateLimitRequestFixtureFor(t, store, "endpoint-second", netip.MustParseAddr("8.8.8.8"), "steam", "ask")
	firstBid, err := ratelimit.RequestFromLease(first.lease, "bid")
	if err != nil {
		t.Fatal(err)
	}
	ask := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "ask_account_exit", Scope: ratelimit.ScopeAccountIP,
		EndpointClass: "ask", Kind: ratelimit.KindMinInterval,
		MinInterval: time.Minute, DefaultCooldown: time.Minute,
	})
	bid := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "bid_account_exit", Scope: ratelimit.ScopeAccountIP,
		EndpointClass: "bid", Kind: ratelimit.KindMinInterval,
		MinInterval: time.Minute, DefaultCooldown: time.Minute,
	})
	generic := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "generic_account_exit", Scope: ratelimit.ScopeAccountIP,
		Kind: ratelimit.KindCooldownOnly,
	})
	for _, id := range []ratelimit.PolicyID{ask.ID, bid.ID, generic.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}

	firstAsk, err := store.AdmitRateLimit(t.Context(), first.request)
	if err != nil || !firstAsk.Allowed() {
		t.Fatalf("first ask allowed=%v err=%v", firstAsk.Allowed(), err)
	}
	if err := store.ApplyRateLimitFeedback(
		t.Context(), firstAsk.Admission(), []ratelimit.Scope{ratelimit.ScopeAccountIP},
		ratelimit.ReasonHTTP429, 0,
	); err != nil {
		t.Fatal(err)
	}
	genericStates, err := store.ListRateLimitStates(t.Context(), generic.ID)
	if err != nil || len(genericStates) != 1 || genericStates[0].CooldownUntil != nil {
		t.Fatalf("generic account-IP rule received endpoint feedback states=%+v err=%v", genericStates, err)
	}
	secondAsk, err := store.AdmitRateLimit(t.Context(), second.request)
	if err != nil || !secondAsk.Allowed() {
		t.Fatalf("other account-IP ask allowed=%v err=%v", secondAsk.Allowed(), err)
	}
	firstBidDecision, err := store.AdmitRateLimit(t.Context(), firstBid)
	if err != nil || !firstBidDecision.Allowed() {
		t.Fatalf("same account-IP bid allowed=%v err=%v", firstBidDecision.Allowed(), err)
	}
	blocked, err := store.AdmitRateLimit(t.Context(), first.request)
	if err != nil || blocked.Allowed() {
		t.Fatalf("cooled account-IP ask allowed=%v err=%v", blocked.Allowed(), err)
	}
	found := false
	for _, blocker := range blocked.Blockers() {
		if blocker.PolicyID() == ask.ID && blocker.Scope() == ratelimit.ScopeAccountIP &&
			blocker.Reason() == ratelimit.BlockReasonCooldown {
			found = true
		}
	}
	if !found {
		t.Fatalf("account-IP ask cooldown blocker missing: %+v", blocked.Blockers())
	}
}

func testRateLimitHTTP429EscalatesUntilSuccess(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	fixture := newRateLimitRequestFixtureFor(t, store, "strike", netip.MustParseAddr("1.1.1.1"), "steam", "ask")
	policy := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "ask_account_exit_strike", Scope: ratelimit.ScopeAccountIP,
		EndpointClass: "ask", Kind: ratelimit.KindMinInterval,
		MinInterval: time.Microsecond, DefaultCooldown: time.Minute,
	})
	makeRateLimitPolicyReady(t, db, policy.ID)

	first, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !first.Allowed() {
		t.Fatalf("first admit allowed=%v err=%v", first.Allowed(), err)
	}
	if err := store.ApplyRateLimitFeedback(
		t.Context(), first.Admission(), []ratelimit.Scope{ratelimit.ScopeAccountIP},
		ratelimit.ReasonHTTP429, 0,
	); err != nil {
		t.Fatal(err)
	}
	states, err := store.ListRateLimitStates(t.Context(), policy.ID)
	if err != nil || len(states) != 1 || states[0].CooldownUntil == nil {
		t.Fatalf("first 429 states=%+v err=%v", states, err)
	}
	if got := states[0].CooldownUntil.Sub(*states[0].CooldownObservedAt); got != time.Minute {
		t.Fatalf("first 429 window=%s", got)
	}

	expireHTTP429Cooldown(t, db, policy.ID)
	second, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !second.Allowed() {
		t.Fatalf("second admit allowed=%v err=%v", second.Allowed(), err)
	}
	if err := store.ApplyRateLimitFeedback(
		t.Context(), second.Admission(), []ratelimit.Scope{ratelimit.ScopeAccountIP},
		ratelimit.ReasonHTTP429, 0,
	); err != nil {
		t.Fatal(err)
	}
	states, err = store.ListRateLimitStates(t.Context(), policy.ID)
	if err != nil || len(states) != 1 || states[0].CooldownUntil == nil {
		t.Fatalf("second 429 states=%+v err=%v", states, err)
	}
	if got := states[0].CooldownUntil.Sub(*states[0].CooldownObservedAt); got != ratelimit.HTTP429RetryCooldown {
		t.Fatalf("second 429 window=%s", got)
	}

	expireHTTP429Cooldown(t, db, policy.ID)
	capped, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !capped.Allowed() {
		t.Fatalf("third admit allowed=%v err=%v", capped.Allowed(), err)
	}
	if err := store.ApplyRateLimitFeedback(
		t.Context(), capped.Admission(), []ratelimit.Scope{ratelimit.ScopeAccountIP},
		ratelimit.ReasonHTTP429, 0,
	); err != nil {
		t.Fatal(err)
	}
	states, err = store.ListRateLimitStates(t.Context(), policy.ID)
	if err != nil || len(states) != 1 || states[0].CooldownUntil == nil {
		t.Fatalf("third 429 states=%+v err=%v", states, err)
	}
	if got := states[0].CooldownUntil.Sub(*states[0].CooldownObservedAt); got != ratelimit.MaxHTTP429Cooldown {
		t.Fatalf("third 429 window=%s", got)
	}

	if err := store.ClearRateLimitStrike(t.Context(), capped.Admission()); err != nil {
		t.Fatal(err)
	}
	states, err = store.ListRateLimitStates(t.Context(), policy.ID)
	if err != nil || states[0].CooldownUntil == nil {
		t.Fatalf("active cooldown was cleared states=%+v err=%v", states, err)
	}

	expireHTTP429Cooldown(t, db, policy.ID)
	if err := store.ClearRateLimitStrike(t.Context(), capped.Admission()); err != nil {
		t.Fatal(err)
	}
	states, err = store.ListRateLimitStates(t.Context(), policy.ID)
	if err != nil || states[0].CooldownUntil != nil || states[0].CooldownReason != "" {
		t.Fatalf("expired 429 strike was not cleared states=%+v err=%v", states, err)
	}

	reset, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !reset.Allowed() {
		t.Fatalf("reset admit allowed=%v err=%v", reset.Allowed(), err)
	}
	if err := store.ApplyRateLimitFeedback(
		t.Context(), reset.Admission(), []ratelimit.Scope{ratelimit.ScopeAccountIP},
		ratelimit.ReasonHTTP429, 0,
	); err != nil {
		t.Fatal(err)
	}
	states, err = store.ListRateLimitStates(t.Context(), policy.ID)
	if err != nil || len(states) != 1 || states[0].CooldownUntil == nil {
		t.Fatalf("reset 429 states=%+v err=%v", states, err)
	}
	if got := states[0].CooldownUntil.Sub(*states[0].CooldownObservedAt); got != time.Minute {
		t.Fatalf("reset 429 window=%s", got)
	}
}

func expireHTTP429Cooldown(t *testing.T, db *sql.DB, id ratelimit.PolicyID) {
	t.Helper()
	// 只把窗口挪到过去，保留时长，否则下一次 429 会按错误的上一档升级。
	result, err := db.ExecContext(t.Context(), `
UPDATE rate_limit_states
SET clock_floor_at = GREATEST(clock_floor_at, NOW()),
    cooldown_observed_at = (NOW() - INTERVAL '1 second') - (cooldown_until - cooldown_observed_at),
    cooldown_until = NOW() - INTERVAL '1 second'
WHERE policy_id = $1`, int64(id))
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("expire cooldown affected=%d err=%v", affected, err)
	}
}

func testRateLimitSameCombinationExitChange(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	firstAddress := netip.MustParseAddr("1.1.1.1")
	secondAddress := netip.MustParseAddr("8.8.8.8")
	fixture := newRateLimitRequestFixture(t, store, "exit-change", firstAddress)
	createRequiredEndpointRate(t, store, db, "steam", "summary")
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_window", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindRollingWindow, Window: time.Minute, MaxRequests: 100,
	})
	profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	ipPolicy := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "exit_interval", Scope: ratelimit.ScopeIP,
		Kind: ratelimit.KindMinInterval, MinInterval: time.Hour,
	})
	for _, id := range []ratelimit.PolicyID{platform.ID, profile.ID, ipPolicy.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}
	firstDecision, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !firstDecision.Allowed() {
		t.Fatalf("first exit admission allowed=%v err=%v", firstDecision.Allowed(), err)
	}
	if firstDecision.Admission().Request().ExitAddress() != firstAddress {
		t.Fatalf("first admission exit=%s", firstDecision.Admission().Request().ExitAddress())
	}
	states, err := store.ListRateLimitStates(t.Context(), ipPolicy.ID)
	if err != nil || len(states) != 1 || states[0].Subject.ExitAddress != firstAddress {
		t.Fatalf("first IP states=%+v err=%v", states, err)
	}
	oldState := cloneRateLimitState(states[0])

	if err := fixture.coordinator.Release(fixture.lease.Token); err != nil {
		t.Fatal(err)
	}
	maintenance, err := fixture.coordinator.AcquireNodeMaintenance(t.Context(), fixture.component, fixture.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.BeginNodeRevalidation(
		t.Context(), maintenance.Token, maintenance.Snapshot.EgressRevision,
	); err != nil {
		t.Fatal(err)
	}
	verifiedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := fixture.coordinator.RecordNodeExit(
		t.Context(), maintenance.Token, secondAddress, verifiedAt, verifiedAt.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.coordinator.Release(maintenance.Token); err != nil {
		t.Fatal(err)
	}

	lease, err := fixture.coordinator.AcquireCombination(
		t.Context(), fixture.component, fixture.combinationID, fixture.targetRegion, verifiedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fixture.coordinator.Release(lease.Token); err != nil && !errors.Is(err, resource.ErrLeaseNotHeld) {
			t.Errorf("release changed-exit lease: %v", err)
		}
	}()
	snapshot, err := lease.CombinationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CombinationID != fixture.combinationID || snapshot.AccountID != fixture.accountID || snapshot.NodeID != fixture.nodeID {
		t.Fatalf("changed lease resources=%+v", snapshot)
	}
	request, err := ratelimit.RequestFromLease(lease, "summary")
	if err != nil {
		t.Fatal(err)
	}
	if request.ExitAddress() != secondAddress {
		t.Fatalf("changed request exit=%s", request.ExitAddress())
	}
	secondDecision, err := store.AdmitRateLimit(t.Context(), request)
	if err != nil || !secondDecision.Allowed() {
		t.Fatalf("changed exit admission allowed=%v err=%v", secondDecision.Allowed(), err)
	}
	if secondDecision.Admission().Request().ExitAddress() != secondAddress {
		t.Fatalf("changed admission exit=%s", secondDecision.Admission().Request().ExitAddress())
	}
	states, err = store.ListRateLimitStates(t.Context(), ipPolicy.ID)
	if err != nil || len(states) != 2 {
		t.Fatalf("changed IP states=%+v err=%v", states, err)
	}
	var storedOld, storedNew *RateLimitState
	for index := range states {
		switch states[index].Subject.ExitAddress {
		case firstAddress:
			storedOld = &states[index]
		case secondAddress:
			storedNew = &states[index]
		}
	}
	if storedOld == nil || storedNew == nil {
		t.Fatalf("changed IP subjects=%+v", states)
	}
	if !sameRateLimitStateSnapshot(*storedOld, oldState) {
		t.Fatalf("old IP state changed before=%+v after=%+v", oldState, *storedOld)
	}
	if storedNew.NextAllowedAt == nil || storedNew.LastAdmittedAt == nil {
		t.Fatalf("new IP state has no reservation=%+v", *storedNew)
	}
}

func testRateLimitPlatformIsolation(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	address := netip.MustParseAddr("1.1.1.1")
	steam := newRateLimitRequestFixtureFor(t, store, "steam-shared", address, "steam", "summary")
	buff := newRateLimitRequestFixtureFor(t, store, "buff-shared", address, "buff", "summary")
	policies := make(map[resource.Platform][]ratelimit.Policy)
	for _, platform := range []resource.Platform{"steam", "buff"} {
		createRequiredEndpointRate(t, store, db, platform, "summary")
		policies[platform] = []ratelimit.Policy{
			createRateLimitPolicy(t, store, ratelimit.PolicySpec{
				Platform: platform, RuleKey: "platform_total", Scope: ratelimit.ScopePlatform,
				Kind: ratelimit.KindRollingWindow, Window: time.Minute, MaxRequests: 100,
			}),
			createRateLimitPolicy(t, store, ratelimit.PolicySpec{
				Platform: platform, RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
				EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
			}),
			createRateLimitPolicy(t, store, ratelimit.PolicySpec{
				Platform: platform, RuleKey: "exit_limit", Scope: ratelimit.ScopeIP,
				Kind: ratelimit.KindMinInterval, MinInterval: time.Minute,
			}),
		}
		for _, policy := range policies[platform] {
			makeRateLimitPolicyReady(t, db, policy.ID)
		}
	}
	steamDecision, err := store.AdmitRateLimit(t.Context(), steam.request)
	if err != nil || !steamDecision.Allowed() {
		t.Fatalf("steam admission allowed=%v err=%v", steamDecision.Allowed(), err)
	}
	if err := store.ApplyRateLimitFeedback(t.Context(), steamDecision.Admission(), []ratelimit.Scope{ratelimit.ScopeIP}, ratelimit.ReasonHTTP429, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	buffDecision, err := store.AdmitRateLimit(t.Context(), buff.request)
	if err != nil || !buffDecision.Allowed() {
		t.Fatalf("buff shared-IP admission allowed=%v err=%v", buffDecision.Allowed(), err)
	}
	steamIP, err := store.ListRateLimitStates(t.Context(), policies["steam"][2].ID)
	if err != nil || len(steamIP) != 1 || steamIP[0].CooldownUntil == nil {
		t.Fatalf("steam IP state=%+v err=%v", steamIP, err)
	}
	buffIP, err := store.ListRateLimitStates(t.Context(), policies["buff"][2].ID)
	if err != nil || len(buffIP) != 1 || buffIP[0].CooldownUntil != nil {
		t.Fatalf("buff IP state=%+v err=%v", buffIP, err)
	}
	if steamIP[0].Subject.ExitAddress != buffIP[0].Subject.ExitAddress {
		t.Fatalf("test exits differ steam=%v buff=%v", steamIP[0].Subject, buffIP[0].Subject)
	}
}

func testRateLimitPostLockClock(t *testing.T, dsn string) {
	t.Run("expired exit is rejected", func(t *testing.T) {
		store, db := newRateLimitTestStore(t, dsn)
		required := createRequiredEndpointRate(t, store, db, "steam", "summary")
		platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
			Platform: "steam", RuleKey: "platform_total", Scope: ratelimit.ScopePlatform,
			Kind: ratelimit.KindRollingWindow, Window: time.Minute, MaxRequests: 100,
		})
		profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
			Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
			EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
		})
		for _, id := range []ratelimit.PolicyID{platform.ID, profile.ID} {
			makeRateLimitPolicyReady(t, db, id)
		}
		fixture := newRateLimitRequestFixtureWithValidity(
			t, store, "expiring", netip.MustParseAddr("1.1.1.1"), "steam", "summary", 800*time.Millisecond,
		)
		gate, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = gate.Rollback() }()
		subject, err := fixture.request.Subject(required.Spec.Scope)
		if err != nil {
			t.Fatal(err)
		}
		if err := lockRateLimitPolicySubject(t.Context(), gate, required.ID, subject); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := store.AdmitRateLimit(context.Background(), fixture.request)
			result <- err
		}()
		time.Sleep(50 * time.Millisecond)
		if wait := time.Until(fixture.request.ExitValidUntil()) + 25*time.Millisecond; wait > 0 {
			time.Sleep(wait)
		}
		if err := gate.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := <-result; err == nil || errors.Is(err, ratelimit.ErrPolicyUnavailable) || errors.Is(err, ErrRateLimitStorage) {
			t.Fatalf("post-lock expired admission error=%v", err)
		}
	})

	t.Run("replacement keeps full warmup", func(t *testing.T) {
		store, db := newRateLimitTestStore(t, dsn)
		policy := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
			Platform: "steam", RuleKey: "platform_total", Scope: ratelimit.ScopePlatform,
			Kind: ratelimit.KindMinInterval, MinInterval: 2 * time.Second,
		})
		gate, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = gate.Rollback() }()
		var locked int64
		if err := gate.QueryRowContext(t.Context(), `
SELECT policy_id FROM rate_limit_policies WHERE policy_id = $1 FOR SHARE`, int64(policy.ID)).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		replacement := policy.Spec
		replacement.DefaultCooldown = time.Minute
		result := make(chan rateLimitReplaceResult, 1)
		go func() {
			updated, err := store.ReplaceRateLimitPolicy(context.Background(), policy.ID, 1, replacement, true)
			result <- rateLimitReplaceResult{policy: updated, err: err}
		}()
		time.Sleep(400 * time.Millisecond)
		if err := gate.Commit(); err != nil {
			t.Fatal(err)
		}
		updated := <-result
		if updated.err != nil {
			t.Fatal(updated.err)
		}
		var afterUnlock time.Time
		if err := db.QueryRowContext(t.Context(), `SELECT clock_timestamp()`).Scan(&afterUnlock); err != nil {
			t.Fatal(err)
		}
		remaining := updated.policy.ReadyAt.Sub(afterUnlock.UTC())
		if remaining < 1800*time.Millisecond || remaining > 2*time.Second {
			t.Fatalf("warmup remaining after lock=%s", remaining)
		}
	})
}

func testClosedRateLimitErrors(t *testing.T, dsn string) {
	store, db := newRateLimitTestStore(t, dsn)
	fixture := newRateLimitRequestFixture(t, store, "closed", netip.MustParseAddr("1.1.1.1"))
	createRequiredEndpointRate(t, store, db, "steam", "summary")
	platform := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "platform_window", Scope: ratelimit.ScopePlatform,
		Kind: ratelimit.KindRollingWindow, Window: time.Minute, MaxRequests: 100,
	})
	profile := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: "steam", RuleKey: "summary_profile", Scope: ratelimit.ScopeInterface,
		EndpointClass: "summary", Kind: ratelimit.KindCooldownOnly,
	})
	for _, id := range []ratelimit.PolicyID{platform.ID, profile.ID} {
		makeRateLimitPolicyReady(t, db, id)
	}
	decision, err := store.AdmitRateLimit(t.Context(), fixture.request)
	if err != nil || !decision.Allowed() {
		t.Fatalf("initial admission allowed=%v err=%v", decision.Allowed(), err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	assertRateLimitStorageError(t, func() error {
		_, err := store.ListRateLimitPolicies(context.Background())
		return err
	})
	assertRateLimitStorageError(t, func() error {
		_, _, err := store.RateLimitPolicy(context.Background(), platform.ID)
		return err
	})
	assertRateLimitStorageError(t, func() error {
		_, err := store.ListRateLimitStates(context.Background(), platform.ID)
		return err
	})
	assertRateLimitStorageError(t, func() error {
		_, err := store.AdmitRateLimit(context.Background(), fixture.request)
		return err
	})
	assertRateLimitStorageError(t, func() error {
		return store.ApplyRateLimitFeedback(context.Background(), decision.Admission(), []ratelimit.Scope{ratelimit.ScopePlatform}, ratelimit.ReasonHTTP429, time.Minute)
	})
}

type rateLimitAdmissionResult struct {
	decision ratelimit.Decision
	err      error
}

type rateLimitReplaceResult struct {
	policy ratelimit.Policy
	err    error
}

type rateLimitRequestFixture struct {
	request       ratelimit.Request
	coordinator   *resource.Coordinator
	component     resource.ComponentID
	lease         resource.Lease
	targetRegion  resource.TargetRegion
	accountID     resource.AccountID
	nodeID        resource.NodeID
	combinationID resource.CombinationID
}

func newRateLimitTestStore(t *testing.T, dsn string) (*Store, *sql.DB) {
	t.Helper()
	db := newTestSchema(t, dsn)
	if err := ApplyMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return store, db
}

func newRateLimitRequestFixture(t *testing.T, store *Store, suffix string, address netip.Addr) rateLimitRequestFixture {
	t.Helper()
	return newRateLimitRequestFixtureFor(t, store, suffix, address, "steam", "summary")
}

func newRateLimitRequestFixtureFor(
	t *testing.T,
	store *Store,
	suffix string,
	address netip.Addr,
	platform resource.Platform,
	endpoint ratelimit.EndpointClass,
) rateLimitRequestFixture {
	t.Helper()
	return newRateLimitRequestFixtureWithValidity(t, store, suffix, address, platform, endpoint, time.Hour)
}

func newRateLimitRequestFixtureWithValidity(
	t *testing.T,
	store *Store,
	suffix string,
	address netip.Addr,
	platform resource.Platform,
	endpoint ratelimit.EndpointClass,
	validFor time.Duration,
) rateLimitRequestFixture {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	targetRegion, known := resource.TargetRegionForPlatform(platform)
	if !known {
		t.Fatalf("rate-limit fixture requires a known platform: %s", platform)
	}
	account, err := store.CreateAccount(ctx, platform, "rate-account-"+suffix, []byte("rate-session-"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	account, err = store.RecordAccountSessionCheck(ctx, account.ID, account.SessionRevision, resource.AccountSessionStateValid, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateNode(ctx, "rate-node-"+suffix, resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionHongKong, EgressMode: resource.EgressModeStatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	node, err = store.RecordNodeExit(ctx, node.ID, node.EgressRevision, address, now.Add(-time.Minute), now.Add(validFor))
	if err != nil {
		t.Fatal(err)
	}
	combination, err := store.CreateCombination(ctx, account.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := resource.NewCoordinator(store)
	if err != nil {
		t.Fatal(err)
	}
	component, err := coordinator.RegisterComponent()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := coordinator.AcquireCombination(ctx, component, combination.ID, targetRegion, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := coordinator.Release(lease.Token); err != nil && !errors.Is(err, resource.ErrLeaseNotHeld) {
			t.Errorf("release rate-limit lease: %v", err)
		}
	})
	request, err := ratelimit.RequestFromLease(lease, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return rateLimitRequestFixture{
		request: request, coordinator: coordinator, component: component, lease: lease,
		targetRegion: targetRegion,
		accountID:    account.ID, nodeID: node.ID, combinationID: combination.ID,
	}
}

func createRateLimitPolicy(t *testing.T, store *Store, spec ratelimit.PolicySpec) ratelimit.Policy {
	t.Helper()
	policy, err := store.CreateRateLimitPolicy(t.Context(), spec, true)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func createRequiredEndpointRate(
	t *testing.T,
	store *Store,
	db *sql.DB,
	platform resource.Platform,
	endpoint ratelimit.EndpointClass,
) ratelimit.Policy {
	t.Helper()
	policy := createRateLimitPolicy(t, store, ratelimit.PolicySpec{
		Platform: platform,
		RuleKey:  ratelimit.RuleKey(string(endpoint) + "_required_account_exit"),
		Scope:    ratelimit.ScopeAccountIP, EndpointClass: endpoint,
		Kind: ratelimit.KindMinInterval, MinInterval: time.Microsecond,
	})
	makeRateLimitPolicyReady(t, db, policy.ID)
	return policy
}

func makeRateLimitPolicyReady(t *testing.T, db *sql.DB, id ratelimit.PolicyID) {
	t.Helper()
	result, err := db.ExecContext(t.Context(), `
UPDATE rate_limit_policies
SET ready_at = GREATEST(changed_at, clock_timestamp())
WHERE policy_id = $1`, int64(id))
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("ready policy affected=%d err=%v", affected, err)
	}
}

func assertRateLimitStorageError(t *testing.T, operation func() error) {
	t.Helper()
	err := operation()
	if !errors.Is(err, ErrRateLimitStorage) || err.Error() != ErrRateLimitStorage.Error() {
		t.Fatalf("storage error=%v", err)
	}
}

func sameRateLimitStateSnapshot(left, right RateLimitState) bool {
	return left.StateID == right.StateID && left.PolicyID == right.PolicyID &&
		left.PolicyRevision == right.PolicyRevision && left.Subject == right.Subject && left.Kind == right.Kind &&
		equalRateLimitTime(left.NextAllowedAt, right.NextAllowedAt) &&
		equalRateLimitTime(left.LastAdmittedAt, right.LastAdmittedAt) &&
		left.ClockFloorAt.Equal(right.ClockFloorAt) &&
		equalRateLimitTime(left.CooldownUntil, right.CooldownUntil) &&
		equalRateLimitTime(left.CooldownObservedAt, right.CooldownObservedAt) &&
		left.CooldownReason == right.CooldownReason &&
		fmt.Sprint(left.RollingAdmittedAt) == fmt.Sprint(right.RollingAdmittedAt)
}

func equalRateLimitTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
