package postgres

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"net/netip"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
)

// pagePayloadRetention 是原始响应的全局保留页数。这张表是可丢弃的诊断副本，
// 写入时按写入时间倒序淘汰，体积因此恒定。
const pagePayloadRetention = 2000

// storePagePayload 保存一页的原始响应并淘汰超出保留窗口的旧副本。
// payload 为空表示这个方向没有单一页面响应，直接跳过。
func storePagePayload(ctx context.Context, tx *sql.Tx, page collection.Page, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	compressed, err := gzipPayload(payload)
	if err != nil {
		return ErrCollectionInvalidInput
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO collection_page_payloads (run_id, page_sequence, payload_gzip, byte_size, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (run_id, page_sequence) DO NOTHING`,
		int64(page.RunID()), int64(page.PageSequence()), compressed, int64(len(payload)), page.CommittedAt(),
	); err != nil {
		return mapCollectionWriteError(ctx, err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM collection_page_payloads
WHERE (run_id, page_sequence) IN (
    SELECT run_id, page_sequence
    FROM collection_page_payloads
    ORDER BY created_at DESC, run_id DESC, page_sequence DESC
    OFFSET $1
)`, pagePayloadRetention); err != nil {
		return mapCollectionWriteError(ctx, err)
	}
	return nil
}

func gzipPayload(payload []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(payload); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func gunzipPayload(compressed []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	// 单页原始响应实测约 10 KiB。限长是防止损坏或伪造的压缩数据把内存吃光。
	const maxPayload = 16 << 20
	payload, err := io.ReadAll(io.LimitReader(reader, maxPayload+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxPayload {
		return nil, ErrCollectionIntegrity
	}
	return payload, nil
}

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
	resolved, err := resolveAttemptWrites(ctx, tx, string(run.Platform()), run.AppID(), input.Attempts)
	if err != nil {
		return collection.Page{}, false, err
	}
	batch := observationBatch{
		AppID:    run.AppID(),
		Platform: string(run.Platform()),
		Side:     side,
		Order:    order,
		Attempts: resolved,
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
    collected_at, committed_at, account_id, exit_address
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING run_id, page_sequence, cursor_before, cursor_after, payload_digest,
          collected_at, committed_at`,
		int64(page.RunID()), int64(page.PageSequence()), collectionCursorBytes(page.CursorBefore()),
		collectionCursorBytes(page.CursorAfter()), pageDigest[:], page.CollectedAt(), page.CommittedAt(),
		nullableAccountID(input.AccountID), nullableAddr(input.ExitAddress),
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
	if err := storePagePayload(ctx, tx, page, input.Payload); err != nil {
		return collection.Page{}, false, err
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

// resolveAttemptWrites 在同一事务内把 attempt 解析到 ProductID。
func resolveAttemptWrites(ctx context.Context, tx *sql.Tx, platform string, appID int64, attempts []collection.AttemptWrite) ([]collection.AttemptWrite, error) {
	resolved := make([]collection.AttemptWrite, len(attempts))
	for index, attempt := range attempts {
		productID, err := resolveAttemptProductID(ctx, tx, platform, appID, attempt)
		if err != nil {
			return nil, err
		}
		attempt.ProductID = productID
		resolved[index] = attempt
	}
	return resolved, nil
}

func resolveAttemptProductID(ctx context.Context, tx *sql.Tx, platform string, appID int64, attempt collection.AttemptWrite) (catalog.ProductID, error) {
	if attempt.ProductID > 0 {
		var storedAppID int64
		err := tx.QueryRowContext(ctx, `SELECT appid FROM steam_products WHERE product_id = $1`, int64(attempt.ProductID)).Scan(&storedAppID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && storedAppID != appID) {
			return 0, ErrCollectionInvalidInput
		}
		if err != nil {
			return 0, collectionStorageError(ctx)
		}
		return attempt.ProductID, nil
	}
	if attempt.PlatformItemID != "" {
		mapped, found, err := platformMappingProductTx(ctx, tx, platform, appID, attempt.PlatformItemID)
		if err != nil {
			return 0, err
		}
		if found {
			if err := refreshProductMediaTx(ctx, tx, mapped, attempt.Media); err != nil {
				return 0, err
			}
			return mapped, nil
		}
	}
	if attempt.ExactName == "" {
		return 0, ErrCollectionInvalidInput
	}
	ids, err := steamProductIDsByExactNameTx(ctx, tx, appID, attempt.ExactName)
	if err != nil {
		return 0, err
	}
	switch len(ids) {
	case 1:
		// 商品已存在时也刷新展示元数据，否则本迁移之前建的商品永远没有图标
		if err := refreshProductMediaTx(ctx, tx, ids[0], attempt.Media); err != nil {
			return 0, err
		}
		return ids[0], nil
	case 0:
		created, err := insertSteamProductTx(ctx, tx, appID, attempt.ExactName, attempt.Media)
		if err != nil {
			return 0, err
		}
		if attempt.PlatformItemID != "" {
			if err := putPlatformMappingTx(ctx, tx, platform, appID, attempt.PlatformItemID, created); err != nil {
				return 0, err
			}
		}
		return created, nil
	default:
		return 0, ErrCollectionInvalidInput
	}
}

func platformMappingProductTx(ctx context.Context, tx *sql.Tx, platform string, appID int64, platformItemID string) (catalog.ProductID, bool, error) {
	var productID int64
	err := tx.QueryRowContext(ctx, `
SELECT product_id FROM platform_product_mappings
WHERE platform = $1 AND appid = $2 AND platform_item_id = $3`,
		platform, appID, platformItemID).Scan(&productID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, collectionStorageError(ctx)
	}
	return catalog.ProductID(productID), true, nil
}

func steamProductIDsByExactNameTx(ctx context.Context, tx *sql.Tx, appID int64, name string) ([]catalog.ProductID, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT product_id FROM steam_products WHERE appid = $1 AND name = $2`, appID, name)
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()
	ids := make([]catalog.ProductID, 0, 2)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, collectionStorageError(ctx)
		}
		ids = append(ids, catalog.ProductID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return ids, nil
}

func insertSteamProductTx(ctx context.Context, tx *sql.Tx, appID int64, name string, media catalog.ProductMedia) (catalog.ProductID, error) {
	media = media.Normalized()
	var id int64
	err := tx.QueryRowContext(ctx, `
INSERT INTO steam_products (appid, name, icon_path, item_type, name_color) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (appid, name) DO UPDATE SET name = EXCLUDED.name
RETURNING product_id`, appID, name,
		nullableText(media.IconPath), nullableText(media.ItemType), nullableText(media.NameColor),
	).Scan(&id)
	if err != nil {
		return 0, collectionStorageError(ctx)
	}
	if id < 1 {
		return 0, ErrCollectionIntegrity
	}
	return catalog.ProductID(id), nil
}

// refreshProductMediaTx 只在有新值时覆盖，空值不清掉已存的元数据：
// 平台偶发少返回一个字段不该让商品的图标消失。
func refreshProductMediaTx(ctx context.Context, tx *sql.Tx, id catalog.ProductID, media catalog.ProductMedia) error {
	media = media.Normalized()
	if media.Empty() {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE steam_products
SET icon_path  = COALESCE($2, icon_path),
    item_type  = COALESCE($3, item_type),
    name_color = COALESCE($4, name_color)
WHERE product_id = $1
  AND (icon_path IS DISTINCT FROM COALESCE($2, icon_path)
    OR item_type IS DISTINCT FROM COALESCE($3, item_type)
    OR name_color IS DISTINCT FROM COALESCE($4, name_color))`,
		int64(id), nullableText(media.IconPath), nullableText(media.ItemType), nullableText(media.NameColor),
	); err != nil {
		return mapCollectionWriteError(ctx, err)
	}
	return nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// 库约束要求 account_id > 0，非正值一律记为未知归属
func nullableAccountID(value int64) any {
	if value < 1 {
		return nil
	}
	return value
}

func nullableAddr(value netip.Addr) any {
	if !value.IsValid() {
		return nil
	}
	return value.String()
}

func putPlatformMappingTx(ctx context.Context, tx *sql.Tx, platform string, appID int64, platformItemID string, productID catalog.ProductID) error {
	var inserted int64
	err := tx.QueryRowContext(ctx, `
INSERT INTO platform_product_mappings (platform, appid, platform_item_id, product_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (platform, appid, platform_item_id) DO NOTHING
RETURNING product_id`,
		platform, appID, platformItemID, int64(productID),
	).Scan(&inserted)
	if err == nil {
		if catalog.ProductID(inserted) != productID {
			return ErrCollectionInvalidInput
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return collectionStorageError(ctx)
	}
	existing, found, err := platformMappingProductTx(ctx, tx, platform, appID, platformItemID)
	if err != nil {
		return err
	}
	if !found || existing != productID {
		return ErrCollectionInvalidInput
	}
	return nil
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
		digestBytes(digest, []byte(snapshot.platformItemID))
		digestBytes(digest, []byte(snapshot.exactName))
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
