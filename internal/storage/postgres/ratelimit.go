package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

var (
	ErrRateLimitPolicyNotFound         = errors.New("rate-limit policy not found")
	ErrRateLimitPolicyConflict         = errors.New("rate-limit policy already exists")
	ErrRateLimitPolicyRevisionConflict = errors.New("rate-limit policy revision conflict")
	ErrRateLimitPolicyIdentityConflict = errors.New("rate-limit policy identity is immutable")
	ErrRateLimitIntegrity              = errors.New("stored rate-limit state is inconsistent")
	ErrRateLimitStorage                = errors.New("rate-limit storage operation failed")
)

const (
	rateLimitAdvisorySeed  int64 = 0x4252474c
	rateLimitPolicyColumns       = `
policy_id, platform, rule_key, scope, endpoint_class, kind,
min_interval_microseconds, window_microseconds, max_requests,
default_cooldown_microseconds, active, revision, changed_at, ready_at`
	rateLimitStateColumns = `
state_id, policy_id, scope, kind, account_id, host(exit_address), policy_revision,
next_allowed_at, array_to_json(rolling_admitted_at)::TEXT, last_admitted_at,
clock_floor_at, cooldown_until, cooldown_observed_at, cooldown_reason_code`
)

// RateLimitState is a credential-free diagnostic snapshot for one concrete
// persisted policy subject. It cannot be written back by callers.
type RateLimitState struct {
	StateID            int64
	PolicyID           ratelimit.PolicyID
	PolicyRevision     int64
	Subject            ratelimit.Subject
	Kind               ratelimit.Kind
	NextAllowedAt      *time.Time
	RollingAdmittedAt  []time.Time
	LastAdmittedAt     *time.Time
	ClockFloorAt       time.Time
	CooldownUntil      *time.Time
	CooldownObservedAt *time.Time
	CooldownReason     ratelimit.ReasonCode
}

// CreateRateLimitPolicy stores revision one and applies a complete warmup.
func (s *Store) CreateRateLimitPolicy(ctx context.Context, spec ratelimit.PolicySpec, enabled bool) (ratelimit.Policy, error) {
	if err := s.validateRateLimit(); err != nil {
		return ratelimit.Policy{}, err
	}
	if err := spec.Validate(); err != nil {
		return ratelimit.Policy{}, err
	}

	tx, err := s.beginRateLimitTx(ctx, spec.Platform)
	if err != nil {
		return ratelimit.Policy{}, err
	}
	defer func() { _ = tx.Rollback() }()

	now, err := rateLimitPlatformEffectiveNow(ctx, tx, spec.Platform)
	if err != nil {
		return ratelimit.Policy{}, err
	}
	readyAt, err := rateLimitReadyAt(now, spec)
	if err != nil {
		return ratelimit.Policy{}, err
	}
	policy, err := scanRateLimitPolicy(tx.QueryRowContext(ctx, `
INSERT INTO rate_limit_policies (
    platform, rule_key, scope, endpoint_class, kind,
    min_interval_microseconds, window_microseconds, max_requests,
    default_cooldown_microseconds, active, revision, changed_at, ready_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 1, $11, $12)
RETURNING `+rateLimitPolicyColumns,
		string(spec.Platform), string(spec.RuleKey), string(spec.Scope), string(spec.EndpointClass), string(spec.Kind),
		nullableDuration(spec.MinInterval), nullableDuration(spec.Window), nullablePositive(spec.MaxRequests),
		nullableDuration(spec.DefaultCooldown), enabled, now, readyAt,
	))
	if err != nil {
		return ratelimit.Policy{}, mapRateLimitPolicyWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return ratelimit.Policy{}, ErrRateLimitStorage
	}
	return policy, nil
}

// ReplaceRateLimitPolicy performs strict revision CAS. Identity fields cannot
// change; every real revision resets pacing state and preserves cooldown facts.
func (s *Store) ReplaceRateLimitPolicy(
	ctx context.Context,
	id ratelimit.PolicyID,
	expectedRevision int64,
	spec ratelimit.PolicySpec,
	enabled bool,
) (ratelimit.Policy, error) {
	if err := s.validateRateLimit(); err != nil {
		return ratelimit.Policy{}, err
	}
	if err := id.Validate(); err != nil {
		return ratelimit.Policy{}, err
	}
	if expectedRevision < 1 {
		return ratelimit.Policy{}, fmt.Errorf("expected revision must be at least 1")
	}
	if err := spec.Validate(); err != nil {
		return ratelimit.Policy{}, err
	}

	tx, err := s.beginRateLimitTx(ctx, spec.Platform)
	if err != nil {
		return ratelimit.Policy{}, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanRateLimitPolicy(tx.QueryRowContext(ctx, `
SELECT `+rateLimitPolicyColumns+`
FROM rate_limit_policies
WHERE policy_id = $1
FOR UPDATE`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return ratelimit.Policy{}, ErrRateLimitPolicyNotFound
	}
	if err != nil {
		return ratelimit.Policy{}, mapRateLimitReadError(err)
	}
	if current.Revision != expectedRevision {
		return ratelimit.Policy{}, ErrRateLimitPolicyRevisionConflict
	}
	if !current.Spec.SameIdentity(spec) {
		return ratelimit.Policy{}, ErrRateLimitPolicyIdentityConflict
	}
	if current.Enabled == enabled && current.Spec == spec {
		if err := tx.Commit(); err != nil {
			return ratelimit.Policy{}, ErrRateLimitStorage
		}
		return current, nil
	}
	if current.Revision == int64(^uint64(0)>>1) {
		return ratelimit.Policy{}, fmt.Errorf("rate-limit policy revision is exhausted")
	}
	if err := lockRateLimitPolicyStates(ctx, tx, id); err != nil {
		return ratelimit.Policy{}, err
	}
	now, err := rateLimitPlatformEffectiveNow(ctx, tx, spec.Platform)
	if err != nil {
		return ratelimit.Policy{}, err
	}
	nextRevision := current.Revision + 1
	readyAt, err := rateLimitReadyAt(now, spec)
	if err != nil {
		return ratelimit.Policy{}, err
	}
	updated, err := scanRateLimitPolicy(tx.QueryRowContext(ctx, `
UPDATE rate_limit_policies
SET min_interval_microseconds = $3,
    window_microseconds = $4,
    max_requests = $5,
    default_cooldown_microseconds = $6,
    active = $7,
    revision = $8,
    changed_at = $9,
    ready_at = $10
WHERE policy_id = $1 AND revision = $2
RETURNING `+rateLimitPolicyColumns,
		int64(id), expectedRevision, nullableDuration(spec.MinInterval), nullableDuration(spec.Window),
		nullablePositive(spec.MaxRequests), nullableDuration(spec.DefaultCooldown), enabled, nextRevision, now, readyAt,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ratelimit.Policy{}, ErrRateLimitPolicyRevisionConflict
	}
	if err != nil {
		return ratelimit.Policy{}, mapRateLimitPolicyWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE rate_limit_states
SET policy_revision = $2,
    next_allowed_at = NULL,
    rolling_admitted_at = ARRAY[]::TIMESTAMPTZ[],
    last_admitted_at = NULL,
    clock_floor_at = GREATEST(clock_floor_at, $3)
WHERE policy_id = $1`, int64(id), nextRevision, now); err != nil {
		return ratelimit.Policy{}, ErrRateLimitStorage
	}
	if err := tx.Commit(); err != nil {
		return ratelimit.Policy{}, ErrRateLimitStorage
	}
	return updated, nil
}

func rateLimitPlatformEffectiveNow(ctx context.Context, tx *sql.Tx, platform resource.Platform) (time.Time, error) {
	var highWater sql.NullTime
	if err := tx.QueryRowContext(ctx, `
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
) platform_clock`, string(platform)).Scan(&highWater); err != nil {
		return time.Time{}, ErrRateLimitStorage
	}
	var databaseNow time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return time.Time{}, ErrRateLimitStorage
	}
	databaseNow = databaseNow.UTC()
	if !validRateLimitTime(databaseNow) {
		return time.Time{}, ErrRateLimitIntegrity
	}
	effectiveNow := databaseNow
	if highWater.Valid {
		highWater.Time = highWater.Time.UTC()
		if !validRateLimitTime(highWater.Time) {
			return time.Time{}, ErrRateLimitIntegrity
		}
		if highWater.Time.After(effectiveNow) {
			effectiveNow = highWater.Time
		}
	}
	return effectiveNow, nil
}

func lockRateLimitPolicyStates(ctx context.Context, tx *sql.Tx, id ratelimit.PolicyID) error {
	rows, err := tx.QueryContext(ctx, `
SELECT state_id
FROM rate_limit_states
WHERE policy_id = $1
ORDER BY state_id
FOR UPDATE`, int64(id))
	if err != nil {
		return ErrRateLimitStorage
	}
	for rows.Next() {
		var stateID int64
		if err := rows.Scan(&stateID); err != nil {
			_ = rows.Close()
			return ErrRateLimitStorage
		}
		if stateID < 1 {
			_ = rows.Close()
			return ErrRateLimitIntegrity
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ErrRateLimitStorage
	}
	if err := rows.Close(); err != nil {
		return ErrRateLimitStorage
	}
	return nil
}

// RateLimitPolicy returns one persisted policy revision.
func (s *Store) RateLimitPolicy(ctx context.Context, id ratelimit.PolicyID) (ratelimit.Policy, bool, error) {
	if err := s.validateRateLimit(); err != nil {
		return ratelimit.Policy{}, false, err
	}
	if err := id.Validate(); err != nil {
		return ratelimit.Policy{}, false, err
	}
	policy, err := scanRateLimitPolicy(s.db.QueryRowContext(ctx, `
SELECT `+rateLimitPolicyColumns+`
FROM rate_limit_policies
WHERE policy_id = $1`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return ratelimit.Policy{}, false, nil
	}
	if err != nil {
		return ratelimit.Policy{}, false, mapRateLimitReadError(err)
	}
	return policy, true, nil
}

// ListRateLimitPolicies returns all policies in stable platform and identity order.
func (s *Store) ListRateLimitPolicies(ctx context.Context) ([]ratelimit.Policy, error) {
	if err := s.validateRateLimit(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+rateLimitPolicyColumns+`
FROM rate_limit_policies
ORDER BY platform, policy_id`)
	if err != nil {
		return nil, ErrRateLimitStorage
	}
	defer rows.Close()
	policies := make([]ratelimit.Policy, 0)
	for rows.Next() {
		policy, err := scanRateLimitPolicy(rows)
		if err != nil {
			return nil, mapRateLimitReadError(err)
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrRateLimitStorage
	}
	return policies, nil
}

// ListRateLimitStates returns safe concrete state snapshots for one policy.
func (s *Store) ListRateLimitStates(ctx context.Context, id ratelimit.PolicyID) ([]RateLimitState, error) {
	if err := s.validateRateLimit(); err != nil {
		return nil, err
	}
	if err := id.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrRateLimitStorage
	}
	defer func() { _ = tx.Rollback() }()
	policy, err := scanRateLimitPolicy(tx.QueryRowContext(ctx, `
SELECT `+rateLimitPolicyColumns+`
FROM rate_limit_policies
WHERE policy_id = $1
FOR SHARE`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRateLimitPolicyNotFound
	}
	if err != nil {
		return nil, mapRateLimitReadError(err)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT `+rateLimitStateColumns+`
FROM rate_limit_states
WHERE policy_id = $1
ORDER BY state_id
FOR SHARE`, int64(id))
	if err != nil {
		return nil, ErrRateLimitStorage
	}
	states := make([]RateLimitState, 0)
	for rows.Next() {
		state, err := scanRateLimitState(rows, policy)
		if err != nil {
			_ = rows.Close()
			return nil, mapRateLimitReadError(err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, ErrRateLimitStorage
	}
	if err := rows.Close(); err != nil {
		return nil, ErrRateLimitStorage
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrRateLimitStorage
	}
	return states, nil
}

func (s *Store) validateRateLimit() error {
	if err := s.validate(); err != nil {
		return err
	}
	if s.rateLimitSigner == nil {
		return ErrRateLimitStorage
	}
	return nil
}

func (s *Store) beginRateLimitTx(ctx context.Context, platform resource.Platform) (*sql.Tx, error) {
	return s.beginRateLimitLockedTx(ctx, platform, false)
}

// State transactions share the platform gate but exclude policy mutations.
func (s *Store) beginRateLimitStateTx(ctx context.Context, platform resource.Platform) (*sql.Tx, error) {
	return s.beginRateLimitLockedTx(ctx, platform, true)
}

func (s *Store) beginRateLimitLockedTx(ctx context.Context, platform resource.Platform, shared bool) (*sql.Tx, error) {
	if err := platform.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrRateLimitStorage
	}
	lockQuery := `SELECT pg_advisory_xact_lock(hashtextextended(
    'buffgo:rate-limit-platform:' || current_schema() || ':' || $1, $2
))`
	if shared {
		lockQuery = `SELECT pg_advisory_xact_lock_shared(hashtextextended(
    'buffgo:rate-limit-platform:' || current_schema() || ':' || $1, $2
))`
	}
	if _, err := tx.ExecContext(ctx, lockQuery, string(platform), rateLimitAdvisorySeed); err != nil {
		_ = tx.Rollback()
		return nil, ErrRateLimitStorage
	}
	return tx, nil
}

func rateLimitReadyAt(now time.Time, spec ratelimit.PolicySpec) (time.Time, error) {
	warmup, err := spec.WarmupDuration()
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(warmup), nil
}

func nullableDuration(value time.Duration) any {
	if value == 0 {
		return nil
	}
	return int64(value / time.Microsecond)
}

func nullablePositive(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

type rateLimitPolicyScan struct {
	id            int64
	platform      string
	ruleKey       string
	scope         string
	endpointClass string
	kind          string
	minIntervalUS sql.NullInt64
	windowUS      sql.NullInt64
	maxRequests   sql.NullInt64
	defaultCoolUS sql.NullInt64
	enabled       bool
	revision      int64
	changedAt     time.Time
	readyAt       time.Time
}

func (data *rateLimitPolicyScan) destinations() []any {
	return []any{
		&data.id, &data.platform, &data.ruleKey, &data.scope, &data.endpointClass, &data.kind,
		&data.minIntervalUS, &data.windowUS, &data.maxRequests, &data.defaultCoolUS,
		&data.enabled, &data.revision, &data.changedAt, &data.readyAt,
	}
}

func (data *rateLimitPolicyScan) result() (ratelimit.Policy, error) {
	policy := ratelimit.Policy{
		ID:       ratelimit.PolicyID(data.id),
		Revision: data.revision,
		Enabled:  data.enabled,
		ReadyAt:  data.readyAt.UTC(),
		Spec: ratelimit.PolicySpec{
			Platform:        resource.Platform(data.platform),
			RuleKey:         ratelimit.RuleKey(data.ruleKey),
			Scope:           ratelimit.Scope(data.scope),
			EndpointClass:   ratelimit.EndpointClass(data.endpointClass),
			Kind:            ratelimit.Kind(data.kind),
			MinInterval:     durationFromNull(data.minIntervalUS),
			Window:          durationFromNull(data.windowUS),
			MaxRequests:     intFromNull(data.maxRequests),
			DefaultCooldown: durationFromNull(data.defaultCoolUS),
		},
	}
	if data.changedAt.IsZero() || data.changedAt.Nanosecond()%int(time.Microsecond) != 0 || data.readyAt.Before(data.changedAt) {
		return ratelimit.Policy{}, ErrRateLimitIntegrity
	}
	if err := policy.Validate(); err != nil {
		return ratelimit.Policy{}, ErrRateLimitIntegrity
	}
	return policy, nil
}

func scanRateLimitPolicy(row rowScanner) (ratelimit.Policy, error) {
	var data rateLimitPolicyScan
	if err := row.Scan(data.destinations()...); err != nil {
		return ratelimit.Policy{}, err
	}
	return data.result()
}

func durationFromNull(value sql.NullInt64) time.Duration {
	if !value.Valid {
		return 0
	}
	return time.Duration(value.Int64) * time.Microsecond
}

func intFromNull(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

type rateLimitStateScan struct {
	stateID          int64
	policyID         int64
	scope            string
	kind             string
	accountID        sql.NullInt64
	exitAddress      sql.NullString
	policyRevision   int64
	nextAllowed      sql.NullTime
	rollingJSON      string
	lastAdmitted     sql.NullTime
	clockFloor       time.Time
	cooldownUntil    sql.NullTime
	cooldownObserved sql.NullTime
	cooldownReason   sql.NullString
}

func (data *rateLimitStateScan) destinations() []any {
	return []any{
		&data.stateID, &data.policyID, &data.scope, &data.kind, &data.accountID, &data.exitAddress,
		&data.policyRevision, &data.nextAllowed, &data.rollingJSON, &data.lastAdmitted,
		&data.clockFloor, &data.cooldownUntil, &data.cooldownObserved, &data.cooldownReason,
	}
}

func (data *rateLimitStateScan) result(policy ratelimit.Policy) (RateLimitState, error) {
	state := RateLimitState{
		StateID:        data.stateID,
		PolicyID:       ratelimit.PolicyID(data.policyID),
		PolicyRevision: data.policyRevision,
		Kind:           ratelimit.Kind(data.kind),
		ClockFloorAt:   data.clockFloor.UTC(),
		CooldownReason: ratelimit.ReasonCode(data.cooldownReason.String),
		Subject: ratelimit.Subject{
			Scope:    ratelimit.Scope(data.scope),
			Platform: policy.Spec.Platform,
		},
	}
	if state.Subject.Scope == ratelimit.ScopeInterface {
		state.Subject.EndpointClass = policy.Spec.EndpointClass
	}
	if data.accountID.Valid {
		state.Subject.AccountID = resource.AccountID(data.accountID.Int64)
	}
	if data.exitAddress.Valid {
		address, err := netip.ParseAddr(data.exitAddress.String)
		if err != nil {
			return RateLimitState{}, ErrRateLimitIntegrity
		}
		state.Subject.ExitAddress = address
	}
	rolling, err := parseRateLimitTimes(data.rollingJSON)
	if err != nil {
		return RateLimitState{}, ErrRateLimitIntegrity
	}
	state.RollingAdmittedAt = rolling
	state.NextAllowedAt = nullTimePointer(data.nextAllowed)
	state.LastAdmittedAt = nullTimePointer(data.lastAdmitted)
	state.CooldownUntil = nullTimePointer(data.cooldownUntil)
	state.CooldownObservedAt = nullTimePointer(data.cooldownObserved)
	if err := validateRateLimitState(state, policy); err != nil {
		return RateLimitState{}, ErrRateLimitIntegrity
	}
	return state, nil
}

func scanRateLimitState(row rowScanner, policy ratelimit.Policy) (RateLimitState, error) {
	var data rateLimitStateScan
	if err := row.Scan(data.destinations()...); err != nil {
		return RateLimitState{}, err
	}
	return data.result(policy)
}

func validateRateLimitState(state RateLimitState, policy ratelimit.Policy) error {
	if state.StateID < 1 || state.PolicyID != policy.ID || state.PolicyRevision != policy.Revision {
		return ErrRateLimitIntegrity
	}
	if state.Kind != policy.Spec.Kind || state.Subject.Scope != policy.Spec.Scope {
		return ErrRateLimitIntegrity
	}
	if err := state.Subject.Validate(); err != nil {
		return ErrRateLimitIntegrity
	}
	if !validRateLimitTime(state.ClockFloorAt) {
		return ErrRateLimitIntegrity
	}
	for index, admittedAt := range state.RollingAdmittedAt {
		if !validRateLimitTime(admittedAt) || admittedAt.After(state.ClockFloorAt) ||
			(index > 0 && admittedAt.Before(state.RollingAdmittedAt[index-1])) {
			return ErrRateLimitIntegrity
		}
	}
	if len(state.RollingAdmittedAt) > int(ratelimit.MaxRollingRequests) {
		return ErrRateLimitIntegrity
	}
	switch state.Kind {
	case ratelimit.KindCooldownOnly:
		if state.NextAllowedAt != nil || state.LastAdmittedAt != nil || len(state.RollingAdmittedAt) != 0 {
			return ErrRateLimitIntegrity
		}
	case ratelimit.KindMinInterval:
		if len(state.RollingAdmittedAt) != 0 || (state.NextAllowedAt == nil) != (state.LastAdmittedAt == nil) {
			return ErrRateLimitIntegrity
		}
		if state.NextAllowedAt != nil {
			if !validRateLimitTime(*state.NextAllowedAt) || !validRateLimitTime(*state.LastAdmittedAt) ||
				state.LastAdmittedAt.After(state.ClockFloorAt) ||
				!state.NextAllowedAt.Equal(state.LastAdmittedAt.Add(policy.Spec.MinInterval)) {
				return ErrRateLimitIntegrity
			}
		}
	case ratelimit.KindRollingWindow:
		if state.NextAllowedAt != nil || (len(state.RollingAdmittedAt) == 0) != (state.LastAdmittedAt == nil) {
			return ErrRateLimitIntegrity
		}
		if int64(len(state.RollingAdmittedAt)) > policy.Spec.MaxRequests {
			return ErrRateLimitIntegrity
		}
		if state.LastAdmittedAt != nil && (!validRateLimitTime(*state.LastAdmittedAt) ||
			!state.LastAdmittedAt.Equal(state.RollingAdmittedAt[len(state.RollingAdmittedAt)-1])) {
			return ErrRateLimitIntegrity
		}
	default:
		return ErrRateLimitIntegrity
	}
	if (state.CooldownUntil == nil) != (state.CooldownObservedAt == nil) {
		return ErrRateLimitIntegrity
	}
	if state.CooldownUntil == nil {
		if state.CooldownReason != "" {
			return ErrRateLimitIntegrity
		}
	} else {
		if err := state.CooldownReason.Validate(); err != nil {
			return ErrRateLimitIntegrity
		}
		if !validRateLimitTime(*state.CooldownUntil) || !validRateLimitTime(*state.CooldownObservedAt) ||
			!state.CooldownUntil.After(*state.CooldownObservedAt) || state.CooldownObservedAt.After(state.ClockFloorAt) {
			return ErrRateLimitIntegrity
		}
	}
	return nil
}

func parseRateLimitTimes(value string) ([]time.Time, error) {
	var encoded []string
	if err := json.Unmarshal([]byte(value), &encoded); err != nil {
		return nil, err
	}
	times := make([]time.Time, len(encoded))
	for index, item := range encoded {
		parsed, err := time.Parse(time.RFC3339Nano, item)
		if err != nil {
			return nil, err
		}
		times[index] = parsed.UTC()
	}
	return times, nil
}

func encodeRateLimitTimes(values []time.Time) (string, error) {
	encoded := make([]string, len(values))
	for index, value := range values {
		if !validRateLimitTime(value) {
			return "", ErrRateLimitIntegrity
		}
		encoded[index] = value.UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(encoded)
	if err != nil {
		return "", ErrRateLimitIntegrity
	}
	return string(data), nil
}

func validRateLimitTime(value time.Time) bool {
	return !value.IsZero() && value.Nanosecond()%int(time.Microsecond) == 0
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func mapRateLimitPolicyWriteError(err error) error {
	switch postgresErrorCode(err) {
	case "23505":
		return ErrRateLimitPolicyConflict
	case "23503", "23514", "22003":
		return ErrRateLimitIntegrity
	default:
		return ErrRateLimitStorage
	}
}

func mapRateLimitReadError(err error) error {
	if errors.Is(err, ErrRateLimitIntegrity) {
		return ErrRateLimitIntegrity
	}
	return ErrRateLimitStorage
}

func rateLimitStateAddress(subject ratelimit.Subject) any {
	if !subject.ExitAddress.IsValid() {
		return nil
	}
	return subject.ExitAddress.String()
}

func rateLimitStateAccount(subject ratelimit.Subject) any {
	if subject.AccountID == 0 {
		return nil
	}
	return int64(subject.AccountID)
}
