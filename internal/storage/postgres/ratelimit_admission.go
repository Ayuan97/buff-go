package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/ratelimit"
)

// AdmitRateLimit atomically reserves every applicable policy or none of them.
// It returns immediately with either a signed admission or a retry decision.
func (s *Store) AdmitRateLimit(ctx context.Context, request ratelimit.Request) (ratelimit.Decision, error) {
	if err := s.validateRateLimit(); err != nil {
		return ratelimit.Decision{}, err
	}
	if err := request.Validate(); err != nil {
		return ratelimit.Decision{}, err
	}
	tx, err := s.beginRateLimitStateTx(ctx, request.Platform())
	if err != nil {
		return ratelimit.Decision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireCollectionOwner(ctx, tx); err != nil {
		if errors.Is(err, collection.ErrOwnerFence) {
			return ratelimit.Decision{}, err
		}
		return ratelimit.Decision{}, ErrRateLimitStorage
	}

	policies, err := selectApplicableRateLimitPolicies(ctx, tx, request)
	if err != nil {
		return ratelimit.Decision{}, err
	}
	if !hasRequiredRateLimitPolicies(policies) {
		return ratelimit.Decision{}, ratelimit.ErrPolicyUnavailable
	}
	states := make([]RateLimitState, len(policies))
	for index, policy := range policies {
		state, err := selectRateLimitStateForUpdate(ctx, tx, policy, request, false)
		if err != nil {
			return ratelimit.Decision{}, err
		}
		states[index] = state
	}
	effectiveNow, err := rateLimitLockedEffectiveNow(ctx, tx, states)
	if err != nil {
		return ratelimit.Decision{}, err
	}
	for index := range states {
		if states[index].StateID == 0 {
			states[index].ClockFloorAt = effectiveNow
		}
	}
	if err := request.ValidateAt(effectiveNow); err != nil {
		return ratelimit.Decision{}, err
	}

	candidates := make([]RateLimitState, len(states))
	blockers := make([]ratelimit.Blocker, 0)
	for index, policy := range policies {
		candidate, blocker, err := reserveRateLimitState(policy, states[index], effectiveNow)
		if err != nil {
			return ratelimit.Decision{}, err
		}
		candidates[index] = candidate
		if blocker != nil {
			blockers = append(blockers, *blocker)
		}
	}
	if len(blockers) != 0 {
		decision, err := ratelimit.Block(blockers)
		if err != nil {
			return ratelimit.Decision{}, ErrRateLimitIntegrity
		}
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return ratelimit.Decision{}, ErrRateLimitStorage
		}
		return decision, nil
	}

	applied := make([]ratelimit.AppliedRule, len(policies))
	for index, policy := range policies {
		if _, err := persistRateLimitState(ctx, tx, candidates[index], policy); err != nil {
			return ratelimit.Decision{}, err
		}
		rule, err := ratelimit.AppliedRuleFromPolicy(policy)
		if err != nil {
			return ratelimit.Decision{}, ErrRateLimitIntegrity
		}
		applied[index] = rule
	}
	admission, err := s.rateLimitSigner.Issue(request, applied, effectiveNow)
	if err != nil {
		return ratelimit.Decision{}, err
	}
	if err := tx.Commit(); err != nil {
		return ratelimit.Decision{}, ErrRateLimitStorage
	}
	decision, err := s.rateLimitSigner.Allow(admission)
	if err != nil {
		return ratelimit.Decision{}, ErrRateLimitIntegrity
	}
	return decision, nil
}

// ApplyRateLimitFeedback freezes the first database observation time for an
// admission and atomically extends only its selected, exact policy states.
func (s *Store) ApplyRateLimitFeedback(
	ctx context.Context,
	admission ratelimit.Admission,
	scopes []ratelimit.Scope,
	reason ratelimit.ReasonCode,
	cooldown time.Duration,
) error {
	if err := s.validateRateLimit(); err != nil {
		return err
	}
	if err := s.rateLimitSigner.Verify(admission); err != nil {
		return err
	}
	rules, err := selectedRateLimitRules(admission, scopes)
	if err != nil {
		return err
	}
	request := admission.Request()
	tx, err := s.beginRateLimitStateTx(ctx, request.Platform())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	policies := make([]ratelimit.Policy, len(rules))
	states := make([]RateLimitState, len(rules))
	// Lock every policy row before taking policy-ordered subject locks.
	for index, rule := range rules {
		policy, err := selectRateLimitPolicyForFeedback(ctx, tx, rule)
		if err != nil {
			return err
		}
		policies[index] = policy
	}
	for index, policy := range policies {
		state, err := selectRateLimitStateForUpdate(ctx, tx, policy, request, true)
		if err != nil {
			return err
		}
		states[index] = state
	}
	effectiveNow, err := rateLimitLockedEffectiveNow(ctx, tx, states)
	if err != nil {
		return err
	}

	duration := cooldown
	if existing, ok := admission.PeekFeedback(); ok && duration == 0 {
		duration = existing.Cooldown()
	} else if duration == 0 && reason == ratelimit.ReasonHTTP429 {
		var escalated time.Duration
		for index, rule := range rules {
			previous := time.Duration(0)
			if index < len(states) {
				previous = http429CooldownFromState(states[index])
			}
			next := ratelimit.NextHTTP429Cooldown(previous, rule.FallbackCooldown())
			if next > escalated {
				escalated = next
			}
		}
		duration = escalated
	}
	feedback, err := s.rateLimitSigner.Feedback(admission, scopes, reason, effectiveNow, duration)
	if err != nil {
		return err
	}
	if err := s.rateLimitSigner.VerifyFeedback(feedback); err != nil {
		return err
	}
	if feedback.ObservedAt().After(effectiveNow) {
		// A retry after a database clock rollback reuses the first DB observation.
		effectiveNow = feedback.ObservedAt()
	}
	for index, rule := range rules {
		until, err := feedback.CooldownUntil(rule.PolicyID())
		if err != nil {
			return ErrRateLimitIntegrity
		}
		if _, err := extendRateLimitCooldown(ctx, tx, states[index], policies[index], feedback, until, effectiveNow); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrRateLimitStorage
	}
	return nil
}

// ClearRateLimitStrike 在一次成功的平台 HTTP 之后清掉过期的 HTTP 429 连击窗口。
// 仍在冷却中的窗口不能清，否则会把正在生效的长锁拆掉。
func (s *Store) ClearRateLimitStrike(ctx context.Context, admission ratelimit.Admission) error {
	if err := s.validateRateLimit(); err != nil {
		return err
	}
	if err := s.rateLimitSigner.Verify(admission); err != nil {
		return err
	}
	rules := admission.Rules()
	if len(rules) == 0 {
		return nil
	}
	request := admission.Request()
	tx, err := s.beginRateLimitStateTx(ctx, request.Platform())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireCollectionOwner(ctx, tx); err != nil {
		if errors.Is(err, collection.ErrOwnerFence) {
			return err
		}
		return ErrRateLimitStorage
	}

	policies := make([]ratelimit.Policy, len(rules))
	states := make([]RateLimitState, len(rules))
	for index, rule := range rules {
		policy, err := selectRateLimitPolicyForFeedback(ctx, tx, rule)
		if err != nil {
			return err
		}
		policies[index] = policy
	}
	for index, policy := range policies {
		state, err := selectRateLimitStateForUpdate(ctx, tx, policy, request, true)
		if err != nil {
			return err
		}
		states[index] = state
	}
	effectiveNow, err := rateLimitLockedEffectiveNow(ctx, tx, states)
	if err != nil {
		return err
	}
	for index := range states {
		if err := clearExpiredHTTP429Cooldown(ctx, tx, states[index], policies[index], effectiveNow); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrRateLimitStorage
	}
	return nil
}

func http429CooldownFromState(state RateLimitState) time.Duration {
	if state.CooldownUntil == nil || state.CooldownObservedAt == nil {
		return 0
	}
	return ratelimit.HTTP429CooldownDuration(*state.CooldownUntil, *state.CooldownObservedAt, state.CooldownReason)
}

func clearExpiredHTTP429Cooldown(
	ctx context.Context,
	tx *sql.Tx,
	state RateLimitState,
	policy ratelimit.Policy,
	now time.Time,
) error {
	if state.StateID == 0 || state.CooldownReason != ratelimit.ReasonHTTP429 {
		return nil
	}
	if state.CooldownUntil != nil && state.CooldownUntil.After(now) {
		return nil
	}
	_, err := scanRateLimitState(tx.QueryRowContext(ctx, `
UPDATE rate_limit_states
SET clock_floor_at = GREATEST(clock_floor_at, $3),
    cooldown_until = NULL,
    cooldown_observed_at = NULL,
    cooldown_reason_code = NULL
WHERE state_id = $1 AND policy_id = $2
  AND cooldown_reason_code = 'http_429'
  AND (cooldown_until IS NULL OR cooldown_until <= $3)
RETURNING `+rateLimitStateColumns,
		state.StateID, int64(policy.ID), now,
	), policy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapRateLimitStateWriteError(err)
	}
	return nil
}

func selectApplicableRateLimitPolicies(ctx context.Context, tx *sql.Tx, request ratelimit.Request) ([]ratelimit.Policy, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT `+rateLimitPolicyColumns+`
FROM rate_limit_policies
WHERE active = TRUE
  AND platform = $1
  AND (
      scope = 'platform' OR
      (scope = 'interface' AND endpoint_class = $2) OR
      scope IN ('account', 'ip') OR
      (scope = 'account_ip' AND (endpoint_class = '' OR endpoint_class = $2))
  )
ORDER BY policy_id
FOR SHARE`, string(request.Platform()), string(request.EndpointClass()))
	if err != nil {
		return nil, ErrRateLimitStorage
	}
	policies := make([]ratelimit.Policy, 0)
	for rows.Next() {
		policy, err := scanRateLimitPolicy(rows)
		if err != nil {
			_ = rows.Close()
			return nil, mapRateLimitReadError(err)
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, ErrRateLimitStorage
	}
	if err := rows.Close(); err != nil {
		return nil, ErrRateLimitStorage
	}
	return policies, nil
}

func hasRequiredRateLimitPolicies(policies []ratelimit.Policy) bool {
	for _, policy := range policies {
		if policy.Spec.Scope == ratelimit.ScopeAccountIP &&
			policy.Spec.EndpointClass != "" && policy.Spec.IsRate() {
			return true
		}
	}
	return false
}

func selectRateLimitStateForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	policy ratelimit.Policy,
	request ratelimit.Request,
	requireExisting bool,
) (RateLimitState, error) {
	subject, err := request.Subject(policy.Spec.Scope)
	if err != nil {
		return RateLimitState{}, err
	}
	if err := lockRateLimitPolicySubject(ctx, tx, policy.ID, subject); err != nil {
		return RateLimitState{}, err
	}
	query, args, err := rateLimitStateSelect(policy, subject)
	if err != nil {
		return RateLimitState{}, err
	}
	state, err := scanRateLimitState(tx.QueryRowContext(ctx, query, args...), policy)
	if err == nil {
		if state.Subject != subject {
			return RateLimitState{}, ErrRateLimitIntegrity
		}
		return state, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RateLimitState{}, mapRateLimitReadError(err)
	}
	if requireExisting {
		return RateLimitState{}, ErrRateLimitIntegrity
	}
	return RateLimitState{
		PolicyID:       policy.ID,
		PolicyRevision: policy.Revision,
		Subject:        subject,
		Kind:           policy.Spec.Kind,
	}, nil
}

func lockRateLimitPolicySubject(
	ctx context.Context,
	tx *sql.Tx,
	policyID ratelimit.PolicyID,
	subject ratelimit.Subject,
) error {
	if err := policyID.Validate(); err != nil {
		return ErrRateLimitIntegrity
	}
	if err := subject.Validate(); err != nil {
		return ErrRateLimitIntegrity
	}
	key := fmt.Sprintf(
		"%s|%s|%s|%d|%s",
		subject.Scope, subject.Platform, subject.EndpointClass,
		subject.AccountID, subject.ExitAddress,
	)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(
    'buffgo:rate-limit-subject:' || current_schema() || ':' || ($1::BIGINT)::TEXT || ':' || $2,
    $3
))`, int64(policyID), key, rateLimitAdvisorySeed); err != nil {
		return ErrRateLimitStorage
	}
	return nil
}

func rateLimitStateSelect(policy ratelimit.Policy, subject ratelimit.Subject) (string, []any, error) {
	if policy.Spec.Scope != subject.Scope {
		return "", nil, ErrRateLimitIntegrity
	}
	switch policy.Spec.Scope {
	case ratelimit.ScopePlatform:
		return `
SELECT ` + rateLimitStateColumns + `
FROM rate_limit_states
WHERE policy_id = $1 AND scope = 'platform'
  AND account_id IS NULL AND exit_address IS NULL
FOR UPDATE`, []any{int64(policy.ID)}, nil
	case ratelimit.ScopeInterface:
		return `
SELECT ` + rateLimitStateColumns + `
FROM rate_limit_states
WHERE policy_id = $1 AND scope = 'interface'
  AND account_id IS NULL AND exit_address IS NULL
FOR UPDATE`, []any{int64(policy.ID)}, nil
	case ratelimit.ScopeAccount:
		return `
SELECT ` + rateLimitStateColumns + `
FROM rate_limit_states
WHERE policy_id = $1 AND scope = 'account'
  AND account_id = $2 AND exit_address IS NULL
FOR UPDATE`, []any{int64(policy.ID), int64(subject.AccountID)}, nil
	case ratelimit.ScopeIP:
		return `
SELECT ` + rateLimitStateColumns + `
FROM rate_limit_states
WHERE policy_id = $1 AND scope = 'ip'
  AND account_id IS NULL AND exit_address = $2::INET
FOR UPDATE`, []any{int64(policy.ID), subject.ExitAddress.String()}, nil
	case ratelimit.ScopeAccountIP:
		return `
SELECT ` + rateLimitStateColumns + `
FROM rate_limit_states
WHERE policy_id = $1 AND scope = 'account_ip'
  AND account_id = $2 AND exit_address = $3::INET
FOR UPDATE`, []any{int64(policy.ID), int64(subject.AccountID), subject.ExitAddress.String()}, nil
	default:
		return "", nil, ErrRateLimitIntegrity
	}
}

func rateLimitLockedEffectiveNow(ctx context.Context, tx *sql.Tx, states []RateLimitState) (time.Time, error) {
	var databaseNow time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return time.Time{}, ErrRateLimitStorage
	}
	effectiveNow := databaseNow.UTC()
	if !validRateLimitTime(effectiveNow) {
		return time.Time{}, ErrRateLimitIntegrity
	}
	for _, state := range states {
		if state.StateID == 0 {
			continue
		}
		if !validRateLimitTime(state.ClockFloorAt) {
			return time.Time{}, ErrRateLimitIntegrity
		}
		if state.ClockFloorAt.After(effectiveNow) {
			effectiveNow = state.ClockFloorAt
		}
	}
	return effectiveNow, nil
}

func reserveRateLimitState(policy ratelimit.Policy, state RateLimitState, now time.Time) (RateLimitState, *ratelimit.Blocker, error) {
	if !validRateLimitTime(now) || now.Before(state.ClockFloorAt) {
		return RateLimitState{}, nil, ErrRateLimitIntegrity
	}
	candidate := cloneRateLimitState(state)
	candidate.ClockFloorAt = now
	var blocker *ratelimit.Blocker
	addBlocker := func(reason ratelimit.BlockReason, retryAt time.Time) error {
		if blocker != nil && !retryAt.After(blocker.RetryAt()) {
			return nil
		}
		value, err := ratelimit.NewBlocker(policy.ID, policy.Spec.Scope, reason, retryAt)
		if err != nil {
			return err
		}
		blocker = &value
		return nil
	}
	if policy.ReadyAt.After(now) {
		if err := addBlocker(ratelimit.BlockReasonWarmup, policy.ReadyAt); err != nil {
			return RateLimitState{}, nil, ErrRateLimitIntegrity
		}
	}
	if state.CooldownUntil != nil && state.CooldownUntil.After(now) {
		if err := addBlocker(ratelimit.BlockReasonCooldown, *state.CooldownUntil); err != nil {
			return RateLimitState{}, nil, ErrRateLimitIntegrity
		}
	}

	switch policy.Spec.Kind {
	case ratelimit.KindCooldownOnly:
	case ratelimit.KindMinInterval:
		if state.NextAllowedAt != nil && state.NextAllowedAt.After(now) {
			if err := addBlocker(ratelimit.BlockReasonBudget, *state.NextAllowedAt); err != nil {
				return RateLimitState{}, nil, ErrRateLimitIntegrity
			}
		}
	case ratelimit.KindRollingWindow:
		threshold := now.Add(-policy.Spec.Window)
		first := sort.Search(len(state.RollingAdmittedAt), func(index int) bool {
			return state.RollingAdmittedAt[index].After(threshold)
		})
		candidate.RollingAdmittedAt = append([]time.Time(nil), state.RollingAdmittedAt[first:]...)
		if len(candidate.RollingAdmittedAt) == 0 {
			candidate.LastAdmittedAt = nil
		} else {
			candidate.LastAdmittedAt = timePointer(candidate.RollingAdmittedAt[len(candidate.RollingAdmittedAt)-1])
		}
		if int64(len(candidate.RollingAdmittedAt)) >= policy.Spec.MaxRequests {
			retryAt := candidate.RollingAdmittedAt[0].Add(policy.Spec.Window)
			if err := addBlocker(ratelimit.BlockReasonBudget, retryAt); err != nil {
				return RateLimitState{}, nil, ErrRateLimitIntegrity
			}
		}
	default:
		return RateLimitState{}, nil, ErrRateLimitIntegrity
	}
	if blocker != nil {
		return candidate, blocker, nil
	}

	switch policy.Spec.Kind {
	case ratelimit.KindMinInterval:
		candidate.LastAdmittedAt = timePointer(now)
		candidate.NextAllowedAt = timePointer(now.Add(policy.Spec.MinInterval))
	case ratelimit.KindRollingWindow:
		candidate.RollingAdmittedAt = append(candidate.RollingAdmittedAt, now)
		candidate.LastAdmittedAt = timePointer(now)
	}
	return candidate, nil, nil
}

func persistRateLimitState(ctx context.Context, tx *sql.Tx, state RateLimitState, policy ratelimit.Policy) (RateLimitState, error) {
	rolling, err := encodeRateLimitTimes(state.RollingAdmittedAt)
	if err != nil {
		return RateLimitState{}, err
	}
	if state.StateID == 0 {
		inserted, err := scanRateLimitState(tx.QueryRowContext(ctx, `
INSERT INTO rate_limit_states (
    policy_id, scope, kind, account_id, exit_address, policy_revision,
    next_allowed_at, rolling_admitted_at, last_admitted_at, clock_floor_at,
    cooldown_until, cooldown_observed_at, cooldown_reason_code
) VALUES (
    $1, $2, $3, $4, $5::INET, $6, $7,
    ARRAY(SELECT value::TIMESTAMPTZ FROM jsonb_array_elements_text($8::JSONB) AS value),
    $9, $10, $11, $12, $13
)
RETURNING `+rateLimitStateColumns,
			int64(policy.ID), string(policy.Spec.Scope), string(policy.Spec.Kind),
			rateLimitStateAccount(state.Subject), rateLimitStateAddress(state.Subject), policy.Revision,
			state.NextAllowedAt, rolling, state.LastAdmittedAt, state.ClockFloorAt,
			state.CooldownUntil, state.CooldownObservedAt, nullableCooldownReason(state.CooldownReason),
		), policy)
		if err != nil {
			return RateLimitState{}, mapRateLimitStateWriteError(err)
		}
		return inserted, nil
	}
	if err := validateRateLimitState(state, policy); err != nil {
		return RateLimitState{}, ErrRateLimitIntegrity
	}
	updated, err := scanRateLimitState(tx.QueryRowContext(ctx, `
UPDATE rate_limit_states
SET policy_revision = $3,
    next_allowed_at = $4,
    rolling_admitted_at = ARRAY(
        SELECT value::TIMESTAMPTZ FROM jsonb_array_elements_text($5::JSONB) AS value
    ),
    last_admitted_at = $6,
    clock_floor_at = $7,
    cooldown_until = $8,
    cooldown_observed_at = $9,
    cooldown_reason_code = $10
WHERE state_id = $1 AND policy_id = $2
RETURNING `+rateLimitStateColumns,
		state.StateID, int64(policy.ID), policy.Revision, state.NextAllowedAt, rolling,
		state.LastAdmittedAt, state.ClockFloorAt, state.CooldownUntil,
		state.CooldownObservedAt, nullableCooldownReason(state.CooldownReason),
	), policy)
	if errors.Is(err, sql.ErrNoRows) {
		return RateLimitState{}, ErrRateLimitIntegrity
	}
	if err != nil {
		return RateLimitState{}, mapRateLimitStateWriteError(err)
	}
	return updated, nil
}

func selectedRateLimitRules(admission ratelimit.Admission, scopes []ratelimit.Scope) ([]ratelimit.AppliedRule, error) {
	rules, err := admission.FeedbackRules(scopes)
	if err != nil {
		return nil, err
	}
	sort.Slice(rules, func(left, right int) bool {
		return rules[left].PolicyID() < rules[right].PolicyID()
	})
	return rules, nil
}

func selectRateLimitPolicyForFeedback(ctx context.Context, tx *sql.Tx, rule ratelimit.AppliedRule) (ratelimit.Policy, error) {
	policy, err := scanRateLimitPolicy(tx.QueryRowContext(ctx, `
SELECT `+rateLimitPolicyColumns+`
FROM rate_limit_policies
WHERE policy_id = $1
FOR SHARE`, int64(rule.PolicyID())))
	if errors.Is(err, sql.ErrNoRows) {
		return ratelimit.Policy{}, ErrRateLimitIntegrity
	}
	if err != nil {
		return ratelimit.Policy{}, mapRateLimitReadError(err)
	}
	if policy.Spec.Platform != rule.Platform() || policy.Spec.Scope != rule.Scope() ||
		policy.Spec.EndpointClass != rule.EndpointClass() {
		return ratelimit.Policy{}, ErrRateLimitIntegrity
	}
	return policy, nil
}

func extendRateLimitCooldown(
	ctx context.Context,
	tx *sql.Tx,
	state RateLimitState,
	policy ratelimit.Policy,
	feedback ratelimit.Feedback,
	until time.Time,
	clockFloor time.Time,
) (RateLimitState, error) {
	updated, err := scanRateLimitState(tx.QueryRowContext(ctx, `
UPDATE rate_limit_states
SET clock_floor_at = GREATEST(clock_floor_at, $3),
    cooldown_until = GREATEST(cooldown_until, $4),
    cooldown_observed_at = CASE
        WHEN cooldown_until IS NULL OR cooldown_until < $4 THEN $5
        ELSE cooldown_observed_at
    END,
    cooldown_reason_code = CASE
        WHEN cooldown_until IS NULL OR cooldown_until < $4 THEN $6
        ELSE cooldown_reason_code
    END
WHERE state_id = $1 AND policy_id = $2
RETURNING `+rateLimitStateColumns,
		state.StateID, int64(policy.ID), clockFloor, until, feedback.ObservedAt(), string(feedback.Reason()),
	), policy)
	if errors.Is(err, sql.ErrNoRows) {
		return RateLimitState{}, ErrRateLimitIntegrity
	}
	if err != nil {
		return RateLimitState{}, mapRateLimitStateWriteError(err)
	}
	return updated, nil
}

func cloneRateLimitState(state RateLimitState) RateLimitState {
	state.RollingAdmittedAt = append([]time.Time(nil), state.RollingAdmittedAt...)
	state.NextAllowedAt = cloneRateLimitTime(state.NextAllowedAt)
	state.LastAdmittedAt = cloneRateLimitTime(state.LastAdmittedAt)
	state.CooldownUntil = cloneRateLimitTime(state.CooldownUntil)
	state.CooldownObservedAt = cloneRateLimitTime(state.CooldownObservedAt)
	return state
}

func cloneRateLimitTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func nullableCooldownReason(reason ratelimit.ReasonCode) any {
	if reason == "" {
		return nil
	}
	return string(reason)
}

func mapRateLimitStateWriteError(err error) error {
	switch postgresErrorCode(err) {
	case "23503", "23505", "23514", "22003", "22007", "22023":
		return ErrRateLimitIntegrity
	default:
		return ErrRateLimitStorage
	}
}
