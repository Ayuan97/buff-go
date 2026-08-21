package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
)

var (
	// ErrProductNotFound reports that a write references no stored Steam product.
	ErrProductNotFound = errors.New("Steam product not found")
	// errProductScopeMismatch reports a stored product outside the page appid.
	errProductScopeMismatch = errors.New("Steam product does not match market scope")
	// ErrStaleObservation reports an included identity older than stored state.
	ErrStaleObservation = errors.New("market observation is stale")
	// ErrObservationConflict reports a changed retry using an already stored write order.
	ErrObservationConflict = errors.New("market observation conflicts with stored facts")
	// ErrMarketIntegrity reports inconsistent latest-attempt and present rows.
	ErrMarketIntegrity = errors.New("market storage integrity error")
)

// MarketKey identifies one product's market direction on one platform.
type MarketKey struct {
	ProductID catalog.ProductID
	Platform  string
	Side      market.Side
}

// AttemptWrite is one explicit product observation in a collected batch. The
// canonical shape lives in the collection domain next to its scheduler.
type AttemptWrite = collection.AttemptWrite

// observationBatch is the market part of a collection page after its causal
// order has been derived from persisted collection state.
type observationBatch struct {
	AppID    int64
	Platform string
	Side     market.Side
	Order    market.WriteOrder
	Attempts []AttemptWrite
}

// LatestAttempt is the latest explicit attempt state without a price payload.
type LatestAttempt struct {
	Key         MarketKey
	Status      market.ObservationStatus
	SourceTime  *time.Time
	CollectedAt time.Time
	ReasonCode  string
	Order       market.WriteOrder
}

// LastPresent is the most recent accepted present observation.
type LastPresent struct {
	Key         MarketKey
	Observation market.Observation
	Order       market.WriteOrder
}

// MarketQuote keeps the storage API compatible with the market query model.
type MarketQuote = market.Quote

type attemptSnapshot struct {
	productID      catalog.ProductID
	platformItemID string
	exactName      string
	status         market.ObservationStatus
	sourceTime     *time.Time
	collectedAt    time.Time
	reasonCode     string
	present        *presentSnapshot
}

type presentSnapshot struct {
	priceCNYCents market.CNYCents
	orderCount    *int64
	itemCount     *int64
}

type latestRow struct {
	status            market.ObservationStatus
	sourceTime        *time.Time
	collectedAt       time.Time
	reasonCode        string
	order             market.WriteOrder
	storageConsistent bool
}

type presentRow struct {
	presentSnapshot
	sourceTime       *time.Time
	collectedAt      time.Time
	order            market.WriteOrder
	latestConsistent bool
}

type observationSaveResult struct {
	applied      int
	skippedStale int
}

func savePreparedObservationsTx(
	ctx context.Context,
	tx *sql.Tx,
	batch observationBatch,
	snapshots []attemptSnapshot,
) (observationSaveResult, error) {
	var result observationSaveResult
	pending := make([]attemptSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		var storedAppID int64
		err := tx.QueryRowContext(ctx,
			`SELECT appid FROM steam_products WHERE product_id = $1`, snapshot.productID,
		).Scan(&storedAppID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return observationSaveResult{}, ErrProductNotFound
		case err != nil:
			return observationSaveResult{}, fmt.Errorf("read Steam product scope: %w", err)
		case storedAppID != batch.AppID:
			return observationSaveResult{}, errProductScopeMismatch
		}

		stored, exists, err := selectLatestForUpdate(ctx, tx, MarketKey{
			ProductID: snapshot.productID,
			Platform:  batch.Platform,
			Side:      batch.Side,
		})
		if err != nil {
			return observationSaveResult{}, err
		}
		if !exists {
			_, found, err := selectPresentForUpdate(ctx, tx, MarketKey{
				ProductID: snapshot.productID,
				Platform:  batch.Platform,
				Side:      batch.Side,
			})
			if err != nil {
				return observationSaveResult{}, err
			}
			if found {
				return observationSaveResult{}, ErrMarketIntegrity
			}
			pending = append(pending, snapshot)
			continue
		}
		storedPresent, presentExists, err := selectPresentForUpdate(ctx, tx, MarketKey{
			ProductID: snapshot.productID,
			Platform:  batch.Platform,
			Side:      batch.Side,
		})
		if err != nil {
			return observationSaveResult{}, err
		}
		if stored.status == market.StatusPresent {
			if !presentExists || compareObservationOrder(
				storedPresent.order, storedPresent.collectedAt, stored.order, stored.collectedAt,
			) != 0 ||
				!timesEqual(storedPresent.sourceTime, stored.sourceTime) ||
				!storedPresent.collectedAt.Equal(stored.collectedAt) {
				return observationSaveResult{}, ErrMarketIntegrity
			}
		} else if presentExists && compareObservationOrder(
			storedPresent.order, storedPresent.collectedAt, stored.order, stored.collectedAt,
		) >= 0 {
			return observationSaveResult{}, ErrMarketIntegrity
		}

		switch compareObservationOrder(batch.Order, snapshot.collectedAt, stored.order, stored.collectedAt) {
		case -1:
			// A page may finish after a newer request for the same product. The
			// task still commits, but this product must not roll market state back.
			if batch.Order.SwitchVersion < stored.order.SwitchVersion {
				return observationSaveResult{}, ErrStaleObservation
			}
			result.skippedStale++
			continue
		case 0:
			if !sameLatest(snapshot, stored) {
				return observationSaveResult{}, ErrObservationConflict
			}
			if snapshot.present != nil {
				if !presentExists || storedPresent.order.Compare(batch.Order) != 0 {
					return observationSaveResult{}, ErrMarketIntegrity
				}
				if !samePresent(snapshot, storedPresent) {
					return observationSaveResult{}, ErrObservationConflict
				}
			}
		case 1:
			if snapshot.present != nil {
				if presentExists && compareObservationOrder(
					storedPresent.order, storedPresent.collectedAt, batch.Order, snapshot.collectedAt,
				) >= 0 {
					return observationSaveResult{}, ErrMarketIntegrity
				}
			}
			pending = append(pending, snapshot)
		}
	}

	if len(pending) == 0 {
		return result, nil
	}
	for _, snapshot := range pending {
		if err := upsertLatest(ctx, tx, batch, snapshot); err != nil {
			return observationSaveResult{}, err
		}
		if snapshot.present != nil {
			if err := upsertPresent(ctx, tx, batch, snapshot); err != nil {
				return observationSaveResult{}, err
			}
		}
	}
	result.applied = len(pending)
	return result, nil
}

// compareObservationOrder keeps target configuration generations authoritative,
// then orders observations by the request observation time. write_seq is only a
// deterministic tie-breaker; commit scheduling must not make an older request win.
func compareObservationOrder(
	left market.WriteOrder,
	leftAt time.Time,
	right market.WriteOrder,
	rightAt time.Time,
) int {
	if left.SwitchVersion < right.SwitchVersion {
		return -1
	}
	if left.SwitchVersion > right.SwitchVersion {
		return 1
	}
	if leftAt.Before(rightAt) {
		return -1
	}
	if leftAt.After(rightAt) {
		return 1
	}
	if left.WriteSequence < right.WriteSequence {
		return -1
	}
	if left.WriteSequence > right.WriteSequence {
		return 1
	}
	return 0
}

// LatestAttempt reads the latest explicit state independently of historical price.
func (s *Store) LatestAttempt(ctx context.Context, key MarketKey) (LatestAttempt, bool, error) {
	if s == nil || s.db == nil {
		return LatestAttempt{}, false, fmt.Errorf("PostgreSQL store is required")
	}
	if err := validateMarketKey(key); err != nil {
		return LatestAttempt{}, false, err
	}
	row, found, err := scanLatest(s.db.QueryRowContext(ctx, `
SELECT a.status, a.source_time, a.collected_at, a.reason_code,
       a.switch_version, a.write_seq,
       CASE WHEN a.status = 'present' THEN EXISTS (
               SELECT 1
               FROM market_last_present p
               WHERE p.product_id = a.product_id
                 AND p.platform = a.platform
                 AND p.side = a.side
                 AND p.switch_version = a.switch_version
                 AND p.write_seq = a.write_seq
                 AND p.source_time IS NOT DISTINCT FROM a.source_time
                 AND p.collected_at = a.collected_at
           ) ELSE NOT EXISTS (
               SELECT 1
               FROM market_last_present p
               WHERE p.product_id = a.product_id
                 AND p.platform = a.platform
                 AND p.side = a.side
		         AND (p.switch_version, p.collected_at, p.write_seq) >=
		             (a.switch_version, a.collected_at, a.write_seq)
           )
       END
FROM market_latest_attempts a
WHERE a.product_id = $1 AND a.platform = $2 AND a.side = $3`,
		key.ProductID, key.Platform, key.Side,
	))
	if err != nil || !found {
		return LatestAttempt{}, found, err
	}
	if err := validateLatestRow(row); err != nil {
		return LatestAttempt{}, false, ErrMarketIntegrity
	}
	return LatestAttempt{
		Key:         key,
		Status:      row.status,
		SourceTime:  cloneTime(row.sourceTime),
		CollectedAt: row.collectedAt,
		ReasonCode:  row.reasonCode,
		Order:       row.order,
	}, true, nil
}

// LastPresent reads the historical present value independently of latest status.
func (s *Store) LastPresent(ctx context.Context, key MarketKey) (LastPresent, bool, error) {
	if s == nil || s.db == nil {
		return LastPresent{}, false, fmt.Errorf("PostgreSQL store is required")
	}
	if err := validateMarketKey(key); err != nil {
		return LastPresent{}, false, err
	}
	row, found, err := scanPresent(s.db.QueryRowContext(ctx, `
SELECT p.price_cny_cents, p.order_count, p.item_count, p.source_time, p.collected_at,
       p.switch_version, p.write_seq,
       EXISTS (
           SELECT 1
           FROM market_latest_attempts a
           WHERE a.product_id = p.product_id
             AND a.platform = p.platform
             AND a.side = p.side
             AND (
                 (
		             (a.switch_version, a.collected_at, a.write_seq) =
		             (p.switch_version, p.collected_at, p.write_seq)
                     AND a.status = 'present'
                     AND a.source_time IS NOT DISTINCT FROM p.source_time
                     AND a.collected_at = p.collected_at
                 ) OR (
		             (a.switch_version, a.collected_at, a.write_seq) >
		             (p.switch_version, p.collected_at, p.write_seq)
                     AND a.status <> 'present'
                 )
             )
       )
FROM market_last_present p
WHERE p.product_id = $1 AND p.platform = $2 AND p.side = $3`,
		key.ProductID, key.Platform, key.Side,
	))
	if err != nil || !found {
		return LastPresent{}, found, err
	}
	price := row.priceCNYCents
	observation, err := market.NewPresentObservation(market.PresentInput{
		Currency:    market.CurrencyCNY,
		Side:        key.Side,
		PriceCents:  &price,
		OrderCount:  cloneInt64(row.orderCount),
		ItemCount:   cloneInt64(row.itemCount),
		SourceTime:  cloneTime(row.sourceTime),
		CollectedAt: row.collectedAt,
	})
	if err != nil || row.order.Validate() != nil || !row.latestConsistent {
		return LastPresent{}, false, ErrMarketIntegrity
	}
	return LastPresent{Key: key, Observation: observation, Order: row.order}, true, nil
}

func prepareBatch(batch observationBatch, allowEmpty bool) ([]attemptSnapshot, error) {
	if err := validateAppID(batch.AppID); err != nil {
		return nil, err
	}
	if err := validatePlatform(batch.Platform); err != nil {
		return nil, err
	}
	if batch.Side != market.SideBid && batch.Side != market.SideAsk {
		return nil, fmt.Errorf("market side must be bid or ask")
	}
	if err := batch.Order.Validate(); err != nil {
		return nil, err
	}
	if len(batch.Attempts) == 0 && !allowEmpty {
		return nil, fmt.Errorf("market batch requires explicit attempts")
	}

	seen := make(map[catalog.ProductID]struct{}, len(batch.Attempts))
	snapshots := make([]attemptSnapshot, 0, len(batch.Attempts))
	for _, attempt := range batch.Attempts {
		if attempt.ProductID <= 0 {
			return nil, fmt.Errorf("product_id must be positive")
		}
		if _, exists := seen[attempt.ProductID]; exists {
			return nil, fmt.Errorf("market batch contains duplicate product_id")
		}
		seen[attempt.ProductID] = struct{}{}
		if err := attempt.Observation.Validate(); err != nil {
			return nil, fmt.Errorf("invalid market observation: %w", err)
		}
		if attempt.Observation.Side != batch.Side {
			return nil, fmt.Errorf("observation side does not match batch")
		}
		if err := validateReasonCode(attempt.Observation.Status, attempt.ReasonCode); err != nil {
			return nil, err
		}

		snapshot := attemptSnapshot{
			productID:      attempt.ProductID,
			platformItemID: attempt.PlatformItemID,
			exactName:      attempt.ExactName,
			status:         attempt.Observation.Status,
			sourceTime:     normalizedTimePointer(attempt.Observation.SourceTime),
			collectedAt:    normalizePostgresTime(attempt.Observation.CollectedAt),
			reasonCode:     attempt.ReasonCode,
		}
		if snapshot.collectedAt.IsZero() || (snapshot.sourceTime != nil && snapshot.sourceTime.IsZero()) {
			return nil, fmt.Errorf("market observation time is outside PostgreSQL precision")
		}
		if attempt.Observation.Summary != nil {
			snapshot.present = &presentSnapshot{
				priceCNYCents: attempt.Observation.Summary.PriceCents,
				orderCount:    cloneInt64(attempt.Observation.Summary.OrderCount),
				itemCount:     cloneInt64(attempt.Observation.Summary.ItemCount),
			}
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].productID < snapshots[j].productID })
	return snapshots, nil
}

func validateMarketKey(key MarketKey) error {
	if key.ProductID <= 0 {
		return fmt.Errorf("product_id must be positive")
	}
	if err := validatePlatform(key.Platform); err != nil {
		return err
	}
	if key.Side != market.SideBid && key.Side != market.SideAsk {
		return fmt.Errorf("market side must be bid or ask")
	}
	return nil
}

func validateReasonCode(status market.ObservationStatus, reason string) error {
	switch status {
	case market.StatusPresent, market.StatusEmpty:
		if reason != "" {
			return fmt.Errorf("present and empty attempts cannot have reason_code")
		}
	case market.StatusUnavailable, market.StatusFailed:
		if !reasonCodePattern.MatchString(reason) {
			return fmt.Errorf("unavailable and failed attempts require a safe reason_code")
		}
	default:
		return fmt.Errorf("invalid market attempt status")
	}
	return nil
}

func selectLatestForUpdate(ctx context.Context, tx *sql.Tx, key MarketKey) (latestRow, bool, error) {
	return scanLatest(tx.QueryRowContext(ctx, `
SELECT status, source_time, collected_at, reason_code,
       switch_version, write_seq, TRUE
FROM market_latest_attempts
WHERE product_id = $1 AND platform = $2 AND side = $3
FOR UPDATE`, key.ProductID, key.Platform, key.Side))
}

func scanLatest(row *sql.Row) (latestRow, bool, error) {
	var result latestRow
	var sourceTime sql.NullTime
	err := row.Scan(
		&result.status,
		&sourceTime,
		&result.collectedAt,
		&result.reasonCode,
		&result.order.SwitchVersion,
		&result.order.WriteSequence,
		&result.storageConsistent,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return latestRow{}, false, nil
	}
	if err != nil {
		return latestRow{}, false, fmt.Errorf("read latest market attempt: %w", err)
	}
	result.sourceTime = timeFromNull(sourceTime)
	result.collectedAt = normalizePostgresTime(result.collectedAt)
	return result, true, nil
}

func selectPresentForUpdate(ctx context.Context, tx *sql.Tx, key MarketKey) (presentRow, bool, error) {
	return scanPresent(tx.QueryRowContext(ctx, `
SELECT price_cny_cents, order_count, item_count, source_time, collected_at,
       switch_version, write_seq, TRUE
FROM market_last_present
WHERE product_id = $1 AND platform = $2 AND side = $3
FOR UPDATE`, key.ProductID, key.Platform, key.Side))
}

func scanPresent(row *sql.Row) (presentRow, bool, error) {
	var result presentRow
	var orderCount, itemCount sql.NullInt64
	var sourceTime sql.NullTime
	err := row.Scan(
		&result.priceCNYCents,
		&orderCount,
		&itemCount,
		&sourceTime,
		&result.collectedAt,
		&result.order.SwitchVersion,
		&result.order.WriteSequence,
		&result.latestConsistent,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return presentRow{}, false, nil
	}
	if err != nil {
		return presentRow{}, false, fmt.Errorf("read last present market value: %w", err)
	}
	result.orderCount = int64FromNull(orderCount)
	result.itemCount = int64FromNull(itemCount)
	result.sourceTime = timeFromNull(sourceTime)
	result.collectedAt = normalizePostgresTime(result.collectedAt)
	return result, true, nil
}

func upsertLatest(ctx context.Context, tx *sql.Tx, batch observationBatch, snapshot attemptSnapshot) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO market_latest_attempts (
    product_id, platform, side, status, source_time, collected_at, reason_code,
    switch_version, write_seq
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (product_id, platform, side) DO UPDATE SET
    status = EXCLUDED.status,
    source_time = EXCLUDED.source_time,
    collected_at = EXCLUDED.collected_at,
    reason_code = EXCLUDED.reason_code,
    switch_version = EXCLUDED.switch_version,
    write_seq = EXCLUDED.write_seq`,
		snapshot.productID,
		batch.Platform,
		batch.Side,
		snapshot.status,
		nullableTime(snapshot.sourceTime),
		snapshot.collectedAt,
		snapshot.reasonCode,
		batch.Order.SwitchVersion,
		batch.Order.WriteSequence,
	)
	if err != nil {
		return fmt.Errorf("write latest market attempt: %w", err)
	}
	return nil
}

func upsertPresent(ctx context.Context, tx *sql.Tx, batch observationBatch, snapshot attemptSnapshot) error {
	key := MarketKey{ProductID: snapshot.productID, Platform: batch.Platform, Side: batch.Side}
	stored, exists, err := selectPresentForUpdate(ctx, tx, key)
	if err != nil {
		return err
	}
	if shouldRecordPriceTick(exists, stored.priceCNYCents, snapshot.present.priceCNYCents) {
		var prev *int64
		if exists {
			cents := int64(stored.priceCNYCents)
			prev = &cents
		}
		if err := insertPriceTick(ctx, tx, key, prev, snapshot.present.priceCNYCents, snapshot.collectedAt, batch.Order); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO market_last_present (
    product_id, platform, side, price_cny_cents, order_count, item_count,
    source_time, collected_at, switch_version, write_seq
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (product_id, platform, side) DO UPDATE SET
    price_cny_cents = EXCLUDED.price_cny_cents,
    order_count = EXCLUDED.order_count,
    item_count = EXCLUDED.item_count,
    source_time = EXCLUDED.source_time,
    collected_at = EXCLUDED.collected_at,
    switch_version = EXCLUDED.switch_version,
    write_seq = EXCLUDED.write_seq`,
		snapshot.productID,
		batch.Platform,
		batch.Side,
		snapshot.present.priceCNYCents,
		nullableInt64(snapshot.present.orderCount),
		nullableInt64(snapshot.present.itemCount),
		nullableTime(snapshot.sourceTime),
		snapshot.collectedAt,
		batch.Order.SwitchVersion,
		batch.Order.WriteSequence,
	)
	if err != nil {
		return fmt.Errorf("write last present market value: %w", err)
	}
	return nil
}

func validateLatestRow(row latestRow) error {
	if err := row.order.Validate(); err != nil {
		return err
	}
	if !row.storageConsistent {
		return fmt.Errorf("latest attempt conflicts with stored present value")
	}
	observation := market.Observation{
		Side:        market.SideAsk,
		Status:      row.status,
		SourceTime:  cloneTime(row.sourceTime),
		CollectedAt: row.collectedAt,
	}
	if row.status == market.StatusPresent {
		if row.collectedAt.IsZero() {
			return fmt.Errorf("present latest attempt has no collected_at")
		}
		if row.sourceTime != nil && row.sourceTime.IsZero() {
			return fmt.Errorf("present latest attempt has invalid source_time")
		}
	} else if err := observation.Validate(); err != nil {
		return err
	}
	return validateReasonCode(row.status, row.reasonCode)
}

func sameLatest(snapshot attemptSnapshot, stored latestRow) bool {
	return snapshot.status == stored.status &&
		snapshot.reasonCode == stored.reasonCode &&
		timesEqual(snapshot.sourceTime, stored.sourceTime) &&
		snapshot.collectedAt.Equal(stored.collectedAt)
}

func samePresent(snapshot attemptSnapshot, stored presentRow) bool {
	return snapshot.present.priceCNYCents == stored.priceCNYCents &&
		int64PointersEqual(snapshot.present.orderCount, stored.orderCount) &&
		int64PointersEqual(snapshot.present.itemCount, stored.itemCount) &&
		timesEqual(snapshot.sourceTime, stored.sourceTime) &&
		snapshot.collectedAt.Equal(stored.collectedAt)
}

func normalizePostgresTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func normalizedTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := normalizePostgresTime(*value)
	return &normalized
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func timeFromNull(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	normalized := normalizePostgresTime(value.Time)
	return &normalized
}

func int64FromNull(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func timesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func int64PointersEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
