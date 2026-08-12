package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"hash"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
)

// Page fence errors are canonical in the collection domain package; the
// storage names keep the same values for errors.Is compatibility.
var (
	// ErrCollectionFence reports a page from a disabled target, an old switch,
	// or a run which is no longer running.
	ErrCollectionFence = collection.ErrFence
	// ErrCollectionPageOrder reports a skipped page or mismatched cursor.
	ErrCollectionPageOrder = collection.ErrPageOrder
	// ErrCollectionPageConflict reports a changed retry for a stored page.
	ErrCollectionPageConflict = collection.ErrPageConflict
)

// SummaryPageCommit is one explicit summary page. Scope and causal order are
// derived from the persisted run rather than accepted from the caller. The
// canonical shape lives in the collection domain next to its scheduler.
type SummaryPageCommit = collection.SummaryPageCommit

// CatalogPageCommit is one explicit catalog page manifest; see the canonical
// collection domain shape.
type CatalogPageCommit = collection.CatalogPageCommit

// Compile-time proof that the store satisfies the scheduler ports.
var (
	_ collection.ScheduleStore     = (*Store)(nil)
	_ collection.RateLimitAdmitter = (*Store)(nil)
)

// CommitSummaryPage atomically fences the target and run, records the page,
// publishes its explicit market attempts, and advances the run cursor.
func (s *Store) CommitSummaryPage(ctx context.Context, input SummaryPageCommit) (collection.Page, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Page{}, false, err
	}
	if input.RunID.Validate() != nil || input.PageSequence.Validate() != nil ||
		input.CursorBefore.Validate() != nil || input.CursorAfter.Validate() != nil ||
		!validCollectionTime(input.CollectedAt) {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Page{}, false, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	run, target, err := lockRunTargetForPage(ctx, tx, input.RunID)
	if err != nil {
		return collection.Page{}, false, err
	}
	switchVersion, hasSwitch := run.SwitchVersion()
	side, hasSide := run.Side()
	if run.TaskType() != collection.TaskTypeSummary || !hasSwitch || !hasSide ||
		target.TaskType() != collection.TaskTypeSummary || !runMatchesTarget(run, target) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	if target.Desired() != collection.DesiredEnabled || target.SwitchVersion() != switchVersion ||
		run.State() != collection.RunRunning {
		return collection.Page{}, false, ErrCollectionFence
	}

	order := market.WriteOrder{
		SwitchVersion: int64(switchVersion),
		RunSequence:   int64(run.RunSequence()),
		PageSequence:  int64(input.PageSequence),
	}
	batch := observationBatch{
		AppID:    run.AppID(),
		Platform: string(run.Platform()),
		Side:     side,
		Order:    order,
		Attempts: input.Attempts,
	}
	snapshots, err := prepareBatch(batch, true)
	if err != nil {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}
	startedAt, started := run.StartedAt()
	if !started || input.CollectedAt.Before(startedAt) {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}
	for _, snapshot := range snapshots {
		if snapshot.collectedAt.Before(startedAt) || snapshot.collectedAt.After(input.CollectedAt) {
			return collection.Page{}, false, ErrCollectionInvalidInput
		}
	}
	digest := summaryPageDigest(input, batch, snapshots)
	if digest == ([32]byte{}) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}

	existing, found, err := collectionPageForUpdate(ctx, tx, input.RunID, input.PageSequence)
	if err != nil {
		return collection.Page{}, false, err
	}
	if found {
		if existing.PayloadDigest() != digest ||
			!existing.CursorBefore().Equal(input.CursorBefore) ||
			!existing.CursorAfter().Equal(input.CursorAfter) ||
			!existing.CollectedAt().Equal(input.CollectedAt) {
			return collection.Page{}, false, ErrCollectionPageConflict
		}
		if err := tx.Commit(); err != nil {
			return collection.Page{}, false, collectionStorageError(ctx)
		}
		return existing, false, nil
	}
	if run.LastPageSequence() == int64(^uint64(0)>>1) ||
		int64(input.PageSequence) != run.LastPageSequence()+1 ||
		!run.CurrentCursor().Equal(input.CursorBefore) {
		return collection.Page{}, false, ErrCollectionPageOrder
	}
	committedAt, err := collectionDatabaseTime(ctx, tx, input.CollectedAt)
	if err != nil {
		return collection.Page{}, false, err
	}
	page, err := collection.NewPage(collection.PageInput{
		RunID:         input.RunID,
		PageSequence:  input.PageSequence,
		CursorBefore:  input.CursorBefore,
		CursorAfter:   input.CursorAfter,
		PayloadDigest: digest,
		CollectedAt:   input.CollectedAt,
		CommittedAt:   committedAt,
	})
	if err != nil {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}
	nextRun, err := run.CommitPage(page)
	if err != nil {
		return collection.Page{}, false, ErrCollectionPageOrder
	}

	pageDigest := page.PayloadDigest()
	storedPage, err := scanCollectionPage(tx.QueryRowContext(ctx, `
INSERT INTO collection_pages (
    run_id, page_sequence, cursor_before, cursor_after, payload_digest,
    collected_at, committed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING run_id, page_sequence, cursor_before, cursor_after, payload_digest,
          collected_at, committed_at`,
		int64(page.RunID()), int64(page.PageSequence()), collectionCursorBytes(page.CursorBefore()),
		collectionCursorBytes(page.CursorAfter()), pageDigest[:], page.CollectedAt(), page.CommittedAt(),
	))
	if err != nil {
		return collection.Page{}, false, mapCollectionWriteError(ctx, err)
	}
	if storedPage.PayloadDigest() != page.PayloadDigest() ||
		!storedPage.CursorBefore().Equal(page.CursorBefore()) ||
		!storedPage.CursorAfter().Equal(page.CursorAfter()) ||
		!storedPage.CollectedAt().Equal(page.CollectedAt()) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	marketApplied, err := savePreparedObservationsTx(ctx, tx, batch, snapshots)
	if err != nil {
		return collection.Page{}, false, mapCollectionMarketWriteError(ctx, err)
	}
	if len(snapshots) > 0 && !marketApplied {
		return collection.Page{}, false, ErrCollectionIntegrity
	}

	storedRun, err := scanCollectionRun(tx.QueryRowContext(ctx, `
UPDATE collection_runs
SET current_cursor = $4, last_page_sequence = $5
WHERE run_id = $1 AND status = 'running'
  AND current_cursor = $2 AND last_page_sequence = $3
RETURNING `+collectionRunColumns,
		int64(run.ID()), collectionCursorBytes(run.CurrentCursor()), run.LastPageSequence(),
		collectionCursorBytes(nextRun.CurrentCursor()), nextRun.LastPageSequence(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Page{}, false, ErrCollectionConflict
	}
	if err != nil {
		return collection.Page{}, false, mapCollectionWriteError(ctx, err)
	}
	if !sameRun(storedRun, nextRun) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return collection.Page{}, false, collectionStorageError(ctx)
	}
	return storedPage, true, nil
}

// CommitCatalogPage atomically fences the catalog target and run, records the
// page manifest, and advances the run cursor. Catalog product content is
// written by the catalog-sync implementation once its interface evidence is
// verified; the page manifest still proves coverage and continuation.
func (s *Store) CommitCatalogPage(ctx context.Context, input CatalogPageCommit) (collection.Page, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Page{}, false, err
	}
	if input.RunID.Validate() != nil || input.PageSequence.Validate() != nil ||
		input.CursorBefore.Validate() != nil || input.CursorAfter.Validate() != nil ||
		!validCollectionTime(input.CollectedAt) {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Page{}, false, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	run, target, err := lockRunTargetForPage(ctx, tx, input.RunID)
	if err != nil {
		return collection.Page{}, false, err
	}
	switchVersion, hasSwitch := run.SwitchVersion()
	if run.TaskType() != collection.TaskTypeCatalog || !hasSwitch ||
		target.TaskType() != collection.TaskTypeCatalog || !runMatchesTarget(run, target) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	if target.Desired() != collection.DesiredEnabled || target.SwitchVersion() != switchVersion ||
		run.State() != collection.RunRunning {
		return collection.Page{}, false, ErrCollectionFence
	}
	startedAt, started := run.StartedAt()
	if !started || input.CollectedAt.Before(startedAt) {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}
	order := market.WriteOrder{
		SwitchVersion: int64(switchVersion),
		RunSequence:   int64(run.RunSequence()),
		PageSequence:  int64(input.PageSequence),
	}
	digest := catalogPageDigest(input, run.AppID(), string(run.Platform()), order)
	if digest == ([32]byte{}) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}

	existing, found, err := collectionPageForUpdate(ctx, tx, input.RunID, input.PageSequence)
	if err != nil {
		return collection.Page{}, false, err
	}
	if found {
		if existing.PayloadDigest() != digest ||
			!existing.CursorBefore().Equal(input.CursorBefore) ||
			!existing.CursorAfter().Equal(input.CursorAfter) ||
			!existing.CollectedAt().Equal(input.CollectedAt) {
			return collection.Page{}, false, ErrCollectionPageConflict
		}
		if err := tx.Commit(); err != nil {
			return collection.Page{}, false, collectionStorageError(ctx)
		}
		return existing, false, nil
	}
	if run.LastPageSequence() == int64(^uint64(0)>>1) ||
		int64(input.PageSequence) != run.LastPageSequence()+1 ||
		!run.CurrentCursor().Equal(input.CursorBefore) {
		return collection.Page{}, false, ErrCollectionPageOrder
	}
	committedAt, err := collectionDatabaseTime(ctx, tx, input.CollectedAt)
	if err != nil {
		return collection.Page{}, false, err
	}
	page, err := collection.NewPage(collection.PageInput{
		RunID:         input.RunID,
		PageSequence:  input.PageSequence,
		CursorBefore:  input.CursorBefore,
		CursorAfter:   input.CursorAfter,
		PayloadDigest: digest,
		CollectedAt:   input.CollectedAt,
		CommittedAt:   committedAt,
	})
	if err != nil {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}
	nextRun, err := run.CommitPage(page)
	if err != nil {
		return collection.Page{}, false, ErrCollectionPageOrder
	}

	pageDigest := page.PayloadDigest()
	storedPage, err := scanCollectionPage(tx.QueryRowContext(ctx, `
INSERT INTO collection_pages (
    run_id, page_sequence, cursor_before, cursor_after, payload_digest,
    collected_at, committed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING run_id, page_sequence, cursor_before, cursor_after, payload_digest,
          collected_at, committed_at`,
		int64(page.RunID()), int64(page.PageSequence()), collectionCursorBytes(page.CursorBefore()),
		collectionCursorBytes(page.CursorAfter()), pageDigest[:], page.CollectedAt(), page.CommittedAt(),
	))
	if err != nil {
		return collection.Page{}, false, mapCollectionWriteError(ctx, err)
	}
	if storedPage.PayloadDigest() != page.PayloadDigest() ||
		!storedPage.CursorBefore().Equal(page.CursorBefore()) ||
		!storedPage.CursorAfter().Equal(page.CursorAfter()) ||
		!storedPage.CollectedAt().Equal(page.CollectedAt()) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	storedRun, err := scanCollectionRun(tx.QueryRowContext(ctx, `
UPDATE collection_runs
SET current_cursor = $4, last_page_sequence = $5
WHERE run_id = $1 AND status = 'running'
  AND current_cursor = $2 AND last_page_sequence = $3
RETURNING `+collectionRunColumns,
		int64(run.ID()), collectionCursorBytes(run.CurrentCursor()), run.LastPageSequence(),
		collectionCursorBytes(nextRun.CurrentCursor()), nextRun.LastPageSequence(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Page{}, false, ErrCollectionConflict
	}
	if err != nil {
		return collection.Page{}, false, mapCollectionWriteError(ctx, err)
	}
	if !sameRun(storedRun, nextRun) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	if err := tx.Commit(); err != nil {
		return collection.Page{}, false, collectionStorageError(ctx)
	}
	return storedPage, true, nil
}

func catalogPageDigest(input CatalogPageCommit, appID int64, platform string, order market.WriteOrder) [32]byte {
	digest := sha256.New()
	digestBytes(digest, []byte("buff-go.catalog-page.v1"))
	digestInt64(digest, int64(input.RunID))
	digestInt64(digest, int64(input.PageSequence))
	digestBytes(digest, input.CursorBefore.Bytes())
	digestBytes(digest, input.CursorAfter.Bytes())
	digestTime(digest, input.CollectedAt)
	digestInt64(digest, appID)
	digestBytes(digest, []byte(platform))
	digestInt64(digest, order.SwitchVersion)
	digestInt64(digest, order.RunSequence)
	digestInt64(digest, order.PageSequence)
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func lockRunTargetForPage(ctx context.Context, tx *sql.Tx, id collection.RunID) (collection.Run, collection.Target, error) {
	var targetID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT target_id FROM collection_runs WHERE run_id = $1`, int64(id)).Scan(&targetID); errors.Is(err, sql.ErrNoRows) {
		return collection.Run{}, collection.Target{}, ErrCollectionNotFound
	} else if err != nil {
		return collection.Run{}, collection.Target{}, collectionStorageError(ctx)
	}
	if !targetID.Valid {
		return collection.Run{}, collection.Target{}, ErrCollectionInvalidInput
	}
	target, _, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1
FOR SHARE`, targetID.Int64)
	if err != nil {
		return collection.Run{}, collection.Target{}, err
	}
	if !found {
		return collection.Run{}, collection.Target{}, ErrCollectionIntegrity
	}
	run, err := scanCollectionRun(tx.QueryRowContext(ctx, `
SELECT `+collectionRunColumns+`
FROM collection_runs
WHERE run_id = $1
FOR UPDATE`, int64(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Run{}, collection.Target{}, ErrCollectionNotFound
	}
	if err != nil {
		return collection.Run{}, collection.Target{}, mapCollectionReadError(ctx, err)
	}
	return run, target, nil
}

func collectionPageForUpdate(ctx context.Context, tx *sql.Tx, runID collection.RunID, sequence collection.Sequence) (collection.Page, bool, error) {
	page, err := scanCollectionPage(tx.QueryRowContext(ctx, `
SELECT run_id, page_sequence, cursor_before, cursor_after, payload_digest,
       collected_at, committed_at
FROM collection_pages
WHERE run_id = $1 AND page_sequence = $2
FOR UPDATE`, int64(runID), int64(sequence)))
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Page{}, false, nil
	}
	if err != nil {
		return collection.Page{}, false, mapCollectionReadError(ctx, err)
	}
	return page, true, nil
}

func summaryPageDigest(input SummaryPageCommit, batch observationBatch, snapshots []attemptSnapshot) [32]byte {
	digest := sha256.New()
	digestBytes(digest, []byte("buff-go.summary-page.v1"))
	digestInt64(digest, int64(input.RunID))
	digestInt64(digest, int64(input.PageSequence))
	digestBytes(digest, input.CursorBefore.Bytes())
	digestBytes(digest, input.CursorAfter.Bytes())
	digestTime(digest, input.CollectedAt)
	digestInt64(digest, batch.AppID)
	digestBytes(digest, []byte(batch.Platform))
	digestBytes(digest, []byte(batch.Side))
	digestInt64(digest, batch.Order.SwitchVersion)
	digestInt64(digest, batch.Order.RunSequence)
	digestInt64(digest, batch.Order.PageSequence)
	digestInt64(digest, int64(len(snapshots)))
	for _, snapshot := range snapshots {
		digestInt64(digest, int64(snapshot.productID))
		digestBytes(digest, []byte(snapshot.status))
		digestOptionalTime(digest, snapshot.sourceTime)
		digestTime(digest, snapshot.collectedAt)
		digestBytes(digest, []byte(snapshot.reasonCode))
		if snapshot.present == nil {
			digestByte(digest, 0)
			continue
		}
		digestByte(digest, 1)
		digestInt64(digest, int64(snapshot.present.priceCNYCents))
		digestOptionalInt64(digest, snapshot.present.orderCount)
		digestOptionalInt64(digest, snapshot.present.itemCount)
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func digestBytes(digest hash.Hash, value []byte) {
	digestUint64(digest, uint64(len(value)))
	_, _ = digest.Write(value)
}

func digestByte(digest hash.Hash, value byte) {
	_, _ = digest.Write([]byte{value})
}

func digestInt64(digest hash.Hash, value int64) {
	digestUint64(digest, uint64(value))
}

func digestUint64(digest hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = digest.Write(encoded[:])
}

func digestTime(digest hash.Hash, value time.Time) {
	digestInt64(digest, value.UnixMicro())
}

func digestOptionalTime(digest hash.Hash, value *time.Time) {
	if value == nil {
		digestByte(digest, 0)
		return
	}
	digestByte(digest, 1)
	digestTime(digest, *value)
}

func digestOptionalInt64(digest hash.Hash, value *int64) {
	if value == nil {
		digestByte(digest, 0)
		return
	}
	digestByte(digest, 1)
	digestInt64(digest, *value)
}

func mapCollectionMarketWriteError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrProductNotFound), errors.Is(err, errProductScopeMismatch):
		return ErrCollectionInvalidInput
	case errors.Is(err, ErrStaleObservation), errors.Is(err, ErrObservationConflict):
		return ErrCollectionPageConflict
	case errors.Is(err, ErrMarketIntegrity):
		return ErrCollectionIntegrity
	default:
		return collectionStorageError(ctx)
	}
}
