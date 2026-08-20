package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
)

// Collection contract errors are canonical in the collection domain package so
// that the scheduler can classify storage results; the storage names remain the
// same values, and errors.Is keeps working across both packages.
var (
	ErrCollectionInvalidInput   = collection.ErrInvalidInput
	ErrCollectionNotFound       = collection.ErrNotFound
	ErrCollectionTargetDisabled = collection.ErrTargetDisabled
	ErrCollectionConflict       = collection.ErrConflict
	ErrCollectionIntegrity      = collection.ErrIntegrity
	ErrCollectionStorage        = collection.ErrStorage
)

// 删除目标只是控制面撤销手段，采集历史一律保留，所以下面两个条件是唯一的拒绝理由。
var (
	ErrTargetNotStopped = errors.New("collection target is not stopped")
	ErrTargetInUse      = errors.New("collection target already has collection history")
)

const collectionTargetColumns = `
target_id, kind, platform, appid, side, desired_state, actual_state,
reason_code, recovery_mode, next_check_at, period_microseconds, revision,
switch_version, write_seq, refill_total, refill_cursor, changed_at, sort_column, sort_dir,
price_min_cents, price_max_cents, to_json(steam_cats), to_json(item_classes)`

// TargetTransition is one explicit scheduler-owned actual-state transition.
type TargetTransition = collection.TargetTransition

type targetCreate struct {
	platform collection.Platform
	appID    int64
	side     market.Side
	desired  collection.DesiredState
}

// CreateSummaryTarget creates or reads the natural (platform, appid, side) target.
func (s *Store) CreateSummaryTarget(ctx context.Context, platform collection.Platform, appID int64, side market.Side, desired collection.DesiredState) (collection.Target, error) {
	if platform.Validate() != nil || appID < 1 || !validCollectionSide(side) || desired.Validate() != nil {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	return s.createCollectionTarget(ctx, targetCreate{
		platform: platform,
		appID:    appID,
		side:     side,
		desired:  desired,
	})
}

func (s *Store) createCollectionTarget(ctx context.Context, input targetCreate) (collection.Target, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Target{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	identity := collectionTargetLockKey(input)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, identity); err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}

	existing, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE kind = 'summary' AND platform = $1 AND appid = $2 AND side = $3
FOR UPDATE`, string(input.platform), input.appID, string(input.side))
	if err != nil {
		return collection.Target{}, err
	}
	if found {
		if !sameTargetCreate(existing, input) {
			return collection.Target{}, ErrCollectionConflict
		}
		if err := tx.Commit(); err != nil {
			return collection.Target{}, collectionStorageError(ctx)
		}
		return existing, nil
	}

	actual := collection.ActualStarting
	if input.desired == collection.DesiredDisabled {
		actual = collection.ActualStopped
	}
	created, err := scanCollectionTarget(tx.QueryRowContext(ctx, `
INSERT INTO collection_targets (
    kind, platform, appid, side, desired_state, actual_state, changed_at
) VALUES ('summary', $1, $2, $3, $4, $5,
          date_trunc('microseconds', clock_timestamp()))
RETURNING `+collectionTargetColumns,
		string(input.platform), input.appID, string(input.side),
		string(input.desired), string(actual),
	))
	if err != nil {
		return collection.Target{}, mapCollectionWriteError(ctx, err)
	}
	if !sameTargetCreate(created, input) {
		return collection.Target{}, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}
	return created, nil
}

// Target returns one target by its stable identity.
func (s *Store) Target(ctx context.Context, id collection.TargetID) (collection.Target, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Target{}, false, err
	}
	if id.Validate() != nil {
		return collection.Target{}, false, ErrCollectionInvalidInput
	}
	target, err := scanCollectionTarget(s.db.QueryRowContext(ctx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Target{}, false, nil
	}
	if err != nil {
		return collection.Target{}, false, mapCollectionReadError(ctx, err)
	}
	return target, true, nil
}

// Targets returns all persisted targets in stable identity order.
func (s *Store) Targets(ctx context.Context) ([]collection.Target, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
ORDER BY target_id`)
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	targets := make([]collection.Target, 0)
	for rows.Next() {
		target, err := scanCollectionTarget(rows)
		if err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return targets, nil
}

// SetTargetDesired applies an enable or disable request with strict revision CAS.
func (s *Store) SetTargetDesired(ctx context.Context, id collection.TargetID, expected collection.Revision, desired collection.DesiredState) (collection.Target, error) {
	if desired.Validate() != nil {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	return s.mutateCollectionTarget(ctx, id, expected, nil, func(current collection.Target, at time.Time) (collection.Target, error) {
		if desired == collection.DesiredEnabled {
			return current.Enable(at)
		}
		return current.Disable(at)
	})
}

// SetTargetSortOrder changes the platform ordering this target collects with.
func (s *Store) SetTargetSortOrder(ctx context.Context, id collection.TargetID, expected collection.Revision, order collection.SortOrder) (collection.Target, error) {
	if order.Validate() != nil {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	return s.mutateCollectionTarget(ctx, id, expected, nil, func(current collection.Target, at time.Time) (collection.Target, error) {
		return current.SetSortOrder(order, at)
	})
}

// SetTargetPriceRange 把出售搜索的价格区间交给 Steam。求购没有对应参数。
func (s *Store) SetTargetPriceRange(ctx context.Context, id collection.TargetID, expected collection.Revision, bounds collection.PriceRange) (collection.Target, error) {
	if bounds.Validate() != nil {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	return s.mutateCollectionTarget(ctx, id, expected, nil, func(current collection.Target, at time.Time) (collection.Target, error) {
		return current.SetPriceRange(bounds, at)
	})
}

// SetTargetSteamFacets 把 Rust 分类多选交给 Steam 搜索。求购没有对应参数。
func (s *Store) SetTargetSteamFacets(ctx context.Context, id collection.TargetID, expected collection.Revision, facets collection.SteamFacets) (collection.Target, error) {
	if facets.Validate() != nil {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	return s.mutateCollectionTarget(ctx, id, expected, nil, func(current collection.Target, at time.Time) (collection.Target, error) {
		return current.SetSteamFacets(facets, at)
	})
}

// DeleteTarget removes a target that never produced collection history.
func (s *Store) DeleteTarget(ctx context.Context, id collection.TargetID) error {
	if err := s.validateCollectionStore(); err != nil {
		return err
	}
	if id.Validate() != nil {
		return ErrCollectionInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	target, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1
FOR UPDATE`, int64(id))
	if err != nil {
		return err
	}
	if !found {
		return ErrCollectionNotFound
	}
	if target.Desired() != collection.DesiredDisabled || target.Actual() != collection.ActualStopped {
		return ErrTargetNotStopped
	}

	var hasHistory bool
	if err := tx.QueryRowContext(ctx, `
SELECT $2::bigint > 0 OR EXISTS (SELECT 1 FROM collection_latest_pages WHERE target_id = $1)`,
		int64(id), target.WriteSeq()).Scan(&hasHistory); err != nil {
		return collectionStorageError(ctx)
	}
	if hasHistory {
		return ErrTargetInUse
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM collection_tasks WHERE target_id = $1`, int64(id)); err != nil {
		return mapCollectionWriteError(ctx, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM collection_targets WHERE target_id = $1`, int64(id)); err != nil {
		if postgresErrorCode(err) == "23503" {
			return ErrTargetInUse
		}
		return mapCollectionWriteError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return collectionStorageError(ctx)
	}
	return nil
}

// TransitionTarget changes only scheduler-owned actual state.
func (s *Store) TransitionTarget(ctx context.Context, id collection.TargetID, expected collection.Revision, expectedSwitch collection.Revision, transition TargetTransition) (collection.Target, error) {
	if transition.RecheckAt != nil {
		value := *transition.RecheckAt
		transition.RecheckAt = &value
	}
	if !validTargetTransition(transition) {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	return s.mutateCollectionTarget(ctx, id, expected, &expectedSwitch, func(current collection.Target, at time.Time) (collection.Target, error) {
		if targetHasTransition(current, transition) {
			return current, nil
		}
		switch transition.State {
		case collection.ActualWaiting:
			return current.MarkWaiting(transition.Reason, *transition.RecheckAt, at)
		case collection.ActualBlocked:
			return current.MarkBlocked(transition.Reason, transition.RecheckAt, at)
		case collection.ActualRunning:
			return current.MarkRunning(at)
		case collection.ActualStopped:
			return current.MarkStopped(at)
		case collection.ActualError:
			return current.MarkError(transition.Reason, at)
		default:
			return collection.Target{}, ErrCollectionInvalidInput
		}
	})
}

// RecoverTarget records an explicit operator recovery without changing the
// enable-cycle switch fence.
func (s *Store) RecoverTarget(ctx context.Context, id collection.TargetID, expected collection.Revision, expectedSwitch collection.Revision) (collection.Target, error) {
	return s.mutateCollectionTarget(ctx, id, expected, &expectedSwitch, func(current collection.Target, at time.Time) (collection.Target, error) {
		return current.Recover(at)
	})
}

func (s *Store) mutateCollectionTarget(
	ctx context.Context,
	id collection.TargetID,
	expected collection.Revision,
	expectedSwitch *collection.Revision,
	mutate func(collection.Target, time.Time) (collection.Target, error),
) (collection.Target, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Target{}, err
	}
	if id.Validate() != nil || expected.Validate() != nil || (expectedSwitch != nil && expectedSwitch.Validate() != nil) {
		return collection.Target{}, ErrCollectionInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	current, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1
FOR UPDATE`, int64(id))
	if err != nil {
		return collection.Target{}, err
	}
	if !found {
		return collection.Target{}, ErrCollectionNotFound
	}
	if current.Revision() != expected || (expectedSwitch != nil && current.SwitchVersion() != *expectedSwitch) {
		return collection.Target{}, ErrCollectionConflict
	}
	at, err := collectionDatabaseTime(ctx, tx, current.ChangedAt())
	if err != nil {
		return collection.Target{}, err
	}
	next, err := mutate(current, at)
	if err != nil {
		if errors.Is(err, ErrCollectionInvalidInput) {
			return collection.Target{}, err
		}
		return collection.Target{}, ErrCollectionConflict
	}
	if next.Revision() == current.Revision() {
		if err := tx.Commit(); err != nil {
			return collection.Target{}, collectionStorageError(ctx)
		}
		return current, nil
	}
	if !validTargetSuccessor(current, next) {
		return collection.Target{}, ErrCollectionIntegrity
	}

	stored, err := scanCollectionTarget(tx.QueryRowContext(ctx, `
UPDATE collection_targets
SET desired_state = $3, actual_state = $4, reason_code = $5,
    recovery_mode = $6, next_check_at = $7, period_microseconds = $8,
    revision = $9, switch_version = $10, changed_at = $11,
    sort_column = $12, sort_dir = $13,
    refill_cursor = $14, write_seq = $15, refill_total = $16,
    price_min_cents = $17, price_max_cents = $18,
    steam_cats = $19, item_classes = $20
WHERE target_id = $1 AND revision = $2
RETURNING `+collectionTargetColumns,
		int64(id), int64(expected), string(next.Desired()), string(next.Actual()), string(next.Reason()),
		string(next.Recovery()), targetRecheckValue(next), targetPeriodValue(next),
		int64(next.Revision()), int64(next.SwitchVersion()), next.ChangedAt(),
		string(next.Sort().Column), string(next.Sort().Direction),
		collectionCursorBytes(next.RefillCursor()), next.WriteSeq(), next.RefillTotal(),
		optionalInt64(next.PriceRange().MinCents), optionalInt64(next.PriceRange().MaxCents),
		textArrayValue(next.SteamFacets().Cats), textArrayValue(next.SteamFacets().Classes),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Target{}, ErrCollectionConflict
	}
	if err != nil {
		return collection.Target{}, mapCollectionWriteError(ctx, err)
	}
	if !sameTargetState(stored, next) {
		return collection.Target{}, ErrCollectionIntegrity
	}
	if current.SwitchVersion() != stored.SwitchVersion() {
		if _, err := tx.ExecContext(ctx, `DELETE FROM collection_tasks WHERE target_id = $1`, int64(id)); err != nil {
			return collection.Target{}, mapCollectionWriteError(ctx, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return collection.Target{}, collectionStorageError(ctx)
	}
	return stored, nil
}

// ListCombinationsFor returns pairings for one platform.
func (s *Store) ListCombinationsFor(ctx context.Context, platform collection.Platform) ([]resource.AccountNodeCombination, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	if platform.Validate() != nil {
		return nil, ErrCollectionInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.combination_id, c.platform, c.account_id, c.node_id
FROM account_node_combinations c
WHERE c.platform = $1
ORDER BY c.combination_id`, string(platform))
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	combinations := make([]resource.AccountNodeCombination, 0)
	for rows.Next() {
		combination, err := scanCombination(rows)
		if err != nil {
			return nil, ErrCollectionIntegrity
		}
		combinations = append(combinations, combination)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return combinations, nil
}

// ListCombinationResourcesFor returns one platform's complete worker resources in one query.
func (s *Store) ListCombinationResourcesFor(ctx context.Context, platform collection.Platform) ([]resource.CombinationResources, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	if platform.Validate() != nil {
		return nil, ErrCollectionInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+combinationResourceReadColumns+`
FROM account_node_combinations c
JOIN platform_accounts a ON a.account_id = c.account_id AND a.platform = c.platform
JOIN access_nodes n ON n.node_id = c.node_id
WHERE c.platform = $1
ORDER BY c.combination_id`, string(platform))
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	resources := make([]resource.CombinationResources, 0)
	for rows.Next() {
		current, err := scanCombinationResources(rows)
		if err != nil {
			if errors.Is(err, ErrResourceIntegrity) {
				return nil, ErrCollectionIntegrity
			}
			return nil, collectionStorageError(ctx)
		}
		resources = append(resources, current)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return resources, nil
}

const collectionInstanceLockKeySQL = `hashtextextended('collection-daemon:' || current_schema(), 0)`

// AcquireInstanceLock takes a session-scoped advisory lock so that only one
// resident scheduler owns this schema's collection state.
func (s *Store) AcquireInstanceLock(ctx context.Context) (collection.InstanceLock, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, false, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, collectionStorageError(ctx)
	}
	var key int64
	var acquired bool
	query := `SELECT lock_key, pg_try_advisory_lock(lock_key) FROM (SELECT ` +
		collectionInstanceLockKeySQL + ` AS lock_key) resolved`
	if err := conn.QueryRowContext(ctx, query).Scan(&key, &acquired); err != nil {
		_ = conn.Close()
		return nil, false, collectionStorageError(ctx)
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return &collectionInstanceLock{conn: conn, key: key}, true, nil
}

type collectionInstanceLock struct {
	mu   sync.Mutex
	conn *sql.Conn
	key  int64
}

const collectionInstanceLockHeldSQL = `
SELECT EXISTS (
    SELECT 1
    FROM pg_locks
    WHERE locktype = 'advisory'
      AND granted
      AND pid = pg_backend_pid()
      AND objsubid = 1
      AND classid = $1::oid
      AND objid = $2::oid
)`

func (lock *collectionInstanceLock) Verify(ctx context.Context) error {
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.conn == nil {
		return ErrCollectionStorage
	}
	var held bool
	classID, objID := advisoryLockParts(lock.key)
	if err := lock.conn.QueryRowContext(ctx, collectionInstanceLockHeldSQL, classID, objID).Scan(&held); err != nil {
		return collectionStorageError(ctx)
	}
	if !held {
		return ErrCollectionStorage
	}
	return nil
}

func advisoryLockParts(key int64) (int64, int64) {
	return int64(uint32(uint64(key) >> 32)), int64(uint32(uint64(key)))
}

func (lock *collectionInstanceLock) Release(ctx context.Context) error {
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.conn == nil {
		return nil
	}
	conn := lock.conn
	lock.conn = nil
	var released bool
	unlockErr := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, lock.key).Scan(&released)
	closeErr := conn.Close()
	if unlockErr != nil || !released {
		return ErrCollectionStorage
	}
	if closeErr != nil {
		return ErrCollectionStorage
	}
	return nil
}

func collectionDatabaseTime(ctx context.Context, tx *sql.Tx, floor time.Time) (time.Time, error) {
	var at time.Time
	if err := tx.QueryRowContext(ctx, `
SELECT date_trunc('microseconds', GREATEST(clock_timestamp(), $1::timestamptz))`, floor).Scan(&at); err != nil {
		return time.Time{}, collectionStorageError(ctx)
	}
	return normalizePostgresTime(at), nil
}

func (s *Store) validateCollectionStore() error {
	if s == nil || s.db == nil {
		return ErrCollectionStorage
	}
	return nil
}

func collectionStorageError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrCollectionStorage
}

func mapCollectionReadError(ctx context.Context, err error) error {
	if errors.Is(err, ErrCollectionIntegrity) {
		return ErrCollectionIntegrity
	}
	return collectionStorageError(ctx)
}

func mapCollectionWriteError(ctx context.Context, err error) error {
	if errors.Is(err, ErrCollectionIntegrity) {
		return ErrCollectionIntegrity
	}
	if postgresErrorCode(err) == "23505" {
		return ErrCollectionConflict
	}
	return collectionStorageError(ctx)
}

func validCollectionSide(side market.Side) bool {
	return side == market.SideBid || side == market.SideAsk
}

func validTargetTransition(transition TargetTransition) bool {
	if transition.State.Validate() != nil || transition.Reason.Validate() != nil {
		return false
	}
	automaticTime := func() bool {
		return transition.RecheckAt != nil && validCollectionTime(*transition.RecheckAt)
	}
	manual := func() bool { return transition.RecheckAt == nil }
	switch transition.State {
	case collection.ActualWaiting:
		return (transition.Reason == collection.TargetReasonNextCycle ||
			transition.Reason == collection.TargetReasonSchedulerOpportunity ||
			transition.Reason == collection.TargetReasonTransientFailure) && automaticTime()
	case collection.ActualBlocked:
		switch transition.Reason {
		case collection.TargetReasonNoCombination, collection.TargetReasonCooldown, collection.TargetReasonEgressUnavailable:
			return automaticTime()
		case collection.TargetReasonMissingRatePolicy, collection.TargetReasonSessionInvalid,
			collection.TargetReasonInvalidConfig, collection.TargetReasonInterfaceUnverified:
			return manual()
		default:
			return false
		}
	case collection.ActualRunning, collection.ActualStopped:
		return transition.Reason == collection.TargetReasonNone && manual()
	case collection.ActualError:
		return (transition.Reason == collection.TargetReasonSchedulerFailure ||
			transition.Reason == collection.TargetReasonStateIntegrity) && manual()
	default:
		return false
	}
}

func targetHasTransition(target collection.Target, transition TargetTransition) bool {
	if target.Actual() != transition.State || target.Reason() != transition.Reason {
		return false
	}
	stored, present := target.RecheckAt()
	if transition.RecheckAt == nil {
		return !present
	}
	return present && stored.Equal(*transition.RecheckAt)
}

func validCollectionTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Year() >= 1 && value.Year() <= 9999 &&
		value.Nanosecond()%int(time.Microsecond) == 0
}

func collectionTargetLockKey(input targetCreate) string {
	return "collection-target:summary:" + string(input.platform) + ":" +
		strconv.FormatInt(input.appID, 10) + ":" + string(input.side)
}

func sameTargetCreate(target collection.Target, input targetCreate) bool {
	if target.TaskType() != collection.TaskTypeSummary || target.Platform() != input.platform || target.Desired() != input.desired {
		return false
	}
	appID, hasApp := target.AppID()
	side, hasSide := target.Side()
	return hasApp && hasSide && appID == input.appID && side == input.side
}

func validTargetSuccessor(current, next collection.Target) bool {
	if current.ID() != next.ID() || current.TaskType() != next.TaskType() || current.Platform() != next.Platform() ||
		next.Revision() != current.Revision()+1 || next.ChangedAt().Before(current.ChangedAt()) {
		return false
	}
	currentApp, currentHasApp := current.AppID()
	nextApp, nextHasApp := next.AppID()
	currentSide, currentHasSide := current.Side()
	nextSide, nextHasSide := next.Side()
	if currentHasApp != nextHasApp || currentApp != nextApp || currentHasSide != nextHasSide || currentSide != nextSide {
		return false
	}
	if current.Desired() != next.Desired() || current.Sort() != next.Sort() || !current.PriceRange().Equal(next.PriceRange()) || !current.SteamFacets().Equal(next.SteamFacets()) {
		return current.SwitchVersion() != collection.Revision(math.MaxInt64) && next.SwitchVersion() == current.SwitchVersion()+1
	}
	return next.SwitchVersion() == current.SwitchVersion()
}

func sameTargetState(left, right collection.Target) bool {
	if left.ID() != right.ID() || left.Revision() != right.Revision() || left.TaskType() != right.TaskType() ||
		left.Platform() != right.Platform() || left.Desired() != right.Desired() || left.Actual() != right.Actual() ||
		left.SwitchVersion() != right.SwitchVersion() || left.Reason() != right.Reason() || left.Recovery() != right.Recovery() ||
		left.Sort() != right.Sort() || !left.PriceRange().Equal(right.PriceRange()) || !left.SteamFacets().Equal(right.SteamFacets()) ||
		left.WriteSeq() != right.WriteSeq() || left.RefillTotal() != right.RefillTotal() ||
		!left.RefillCursor().Equal(right.RefillCursor()) || !left.ChangedAt().Equal(right.ChangedAt()) {
		return false
	}
	leftApp, leftHasApp := left.AppID()
	rightApp, rightHasApp := right.AppID()
	leftSide, leftHasSide := left.Side()
	rightSide, rightHasSide := right.Side()
	leftRecheck, leftHasRecheck := left.RecheckAt()
	rightRecheck, rightHasRecheck := right.RecheckAt()
	return leftHasApp == rightHasApp && leftApp == rightApp && leftHasSide == rightHasSide && leftSide == rightSide &&
		leftHasRecheck == rightHasRecheck && leftRecheck.Equal(rightRecheck)
}

func targetRecheckValue(target collection.Target) any {
	value, present := target.RecheckAt()
	if !present {
		return nil
	}
	return value
}

func targetPeriodValue(target collection.Target) any {
	return nil
}

func collectionCursorBytes(cursor collection.Cursor) []byte {
	value := cursor.Bytes()
	if value == nil {
		return []byte{}
	}
	return value
}

type collectionTargetScan struct {
	id            int64
	kind          string
	platform      string
	appID         sql.NullInt64
	side          sql.NullString
	desired       string
	actual        string
	reason        string
	recovery      string
	recheckAt     sql.NullTime
	periodMicros  sql.NullInt64
	revision      int64
	switchVersion int64
	writeSeq      int64
	refillTotal   int64
	refillCursor  []byte
	changedAt     time.Time
	sortColumn    string
	sortDirection string
	priceMin      sql.NullInt64
	priceMax      sql.NullInt64
	steamCats     []byte
	itemClasses   []byte
}

func (data *collectionTargetScan) destinations() []any {
	return []any{
		&data.id, &data.kind, &data.platform, &data.appID, &data.side, &data.desired, &data.actual,
		&data.reason, &data.recovery, &data.recheckAt, &data.periodMicros, &data.revision,
		&data.switchVersion, &data.writeSeq, &data.refillTotal, &data.refillCursor, &data.changedAt,
		&data.sortColumn, &data.sortDirection, &data.priceMin, &data.priceMax,
		&data.steamCats, &data.itemClasses,
	}
}

func textArrayValue(values []string) any {
	if values == nil {
		return []string{}
	}
	return values
}

func scanJSONStringArray(raw []byte) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return []string{}, nil
	}
	return out, nil
}

func optionalInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func scanPriceRange(minValue, maxValue sql.NullInt64) collection.PriceRange {
	var bounds collection.PriceRange
	if minValue.Valid {
		cents := minValue.Int64
		bounds.MinCents = &cents
	}
	if maxValue.Valid {
		cents := maxValue.Int64
		bounds.MaxCents = &cents
	}
	return bounds
}

func scanCollectionTarget(scanner rowScanner) (collection.Target, error) {
	var data collectionTargetScan
	if err := scanner.Scan(data.destinations()...); err != nil {
		return collection.Target{}, err
	}
	if data.writeSeq < 0 || data.refillTotal < 0 {
		return collection.Target{}, ErrCollectionIntegrity
	}
	changedAt := normalizePostgresTime(data.changedAt)
	var recheckAt *time.Time
	if data.recheckAt.Valid {
		value := normalizePostgresTime(data.recheckAt.Time)
		recheckAt = &value
	}
	cursor, err := collection.NewCursor(data.refillCursor)
	if err != nil {
		return collection.Target{}, ErrCollectionIntegrity
	}
	if collection.TaskType(data.kind) != collection.TaskTypeSummary {
		return collection.Target{}, ErrCollectionIntegrity
	}
	if !data.appID.Valid || data.appID.Int64 < 1 || !data.side.Valid || data.periodMicros.Valid {
		return collection.Target{}, ErrCollectionIntegrity
	}
	cats, err := scanJSONStringArray(data.steamCats)
	if err != nil {
		return collection.Target{}, ErrCollectionIntegrity
	}
	classes, err := scanJSONStringArray(data.itemClasses)
	if err != nil {
		return collection.Target{}, ErrCollectionIntegrity
	}
	target, err := collection.NewSummaryTarget(collection.SummaryTargetInput{
		ID: collection.TargetID(data.id), Revision: collection.Revision(data.revision),
		Platform: collection.Platform(data.platform), AppID: data.appID.Int64, Side: market.Side(data.side.String),
		Desired: collection.DesiredState(data.desired), Actual: collection.ActualState(data.actual),
		SwitchVersion: collection.Revision(data.switchVersion),
		Reason:        collection.TargetReason(data.reason), Recovery: collection.RecoveryMode(data.recovery),
		RecheckAt: recheckAt, ChangedAt: changedAt,
		Sort: collection.SortOrder{
			Column:    collection.SortColumn(data.sortColumn),
			Direction: collection.SortDirection(data.sortDirection),
		},
		PriceRange:   scanPriceRange(data.priceMin, data.priceMax),
		SteamFacets:  collection.SteamFacets{Cats: cats, Classes: classes},
		RefillCursor: cursor, WriteSeq: data.writeSeq, RefillTotal: data.refillTotal,
	})
	if err != nil || target.Platform() != collection.Platform(data.platform) {
		return collection.Target{}, ErrCollectionIntegrity
	}
	return target, nil
}

func queryCollectionTarget(ctx context.Context, tx *sql.Tx, query string, args ...any) (collection.Target, bool, error) {
	target, err := scanCollectionTarget(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Target{}, false, nil
	}
	if err != nil {
		return collection.Target{}, false, mapCollectionReadError(ctx, err)
	}
	return target, true, nil
}
