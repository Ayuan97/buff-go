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

func compressPagePayload(payload []byte) ([]byte, int64, error) {
	if len(payload) == 0 {
		return nil, 0, nil
	}
	compressed, err := gzipPayload(payload)
	if err != nil {
		return nil, 0, ErrCollectionInvalidInput
	}
	return compressed, int64(len(payload)), nil
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
	// ErrCollectionFence reports a page from a disabled target or an old switch.
	ErrCollectionFence = collection.ErrFence
	// ErrCollectionPageOrder reports a skipped page or mismatched cursor.
	ErrCollectionPageOrder = collection.ErrPageOrder
	// ErrCollectionPageConflict reports a changed retry for a stored page.
	ErrCollectionPageConflict = collection.ErrPageConflict
)

// SummaryPageCommit is one explicit summary page. The claimed task and target
// switch fence are checked in storage; write_seq is assigned here.
type SummaryPageCommit = collection.SummaryPageCommit

// Compile-time proof that the store satisfies the scheduler ports.
var (
	_ collection.ScheduleStore     = (*Store)(nil)
	_ collection.RateLimitAdmitter = (*Store)(nil)
)

// CommitSummaryPage fences the target, writes the latest page and market facts,
// and advances write_seq.
func (s *Store) CommitSummaryPage(ctx context.Context, input SummaryPageCommit) (collection.Page, bool, error) {
	if err := s.validateCollectionStore(); err != nil {
		return collection.Page{}, false, err
	}
	if input.TargetID.Validate() != nil || input.TaskID.Validate() != nil || input.CombinationID.Validate() != nil ||
		input.ClaimGeneration < 1 ||
		input.ExpectedSwitch.Validate() != nil || input.CursorBefore.Validate() != nil ||
		input.CursorAfter.Validate() != nil || !validCollectionTime(input.CollectedAt) ||
		input.AskTotal < 0 {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return collection.Page{}, false, collectionStorageError(ctx)
	}
	defer func() { _ = tx.Rollback() }()

	target, found, err := queryCollectionTarget(ctx, tx, `
SELECT `+collectionTargetColumns+`
FROM collection_targets
WHERE target_id = $1
FOR UPDATE`, int64(input.TargetID))
	if err != nil {
		return collection.Page{}, false, err
	}
	if !found {
		return collection.Page{}, false, ErrCollectionNotFound
	}
	side, hasSide := target.Side()
	appID, hasApp := target.AppID()
	if target.TaskType() != collection.TaskTypeSummary || !hasSide || !hasApp {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	if side == market.SideBid && input.AskTotal != 0 {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}
	if target.Desired() != collection.DesiredEnabled || target.SwitchVersion() != input.ExpectedSwitch {
		return collection.Page{}, false, ErrCollectionFence
	}

	var claimedTaskID int64
	err = tx.QueryRowContext(ctx, `
SELECT task_id FROM collection_tasks
WHERE task_id = $1 AND target_id = $2 AND state = 'claimed' AND claimed_by = $3 AND claim_generation = $4
FOR UPDATE`, int64(input.TaskID), int64(input.TargetID), int64(input.CombinationID), input.ClaimGeneration).Scan(&claimedTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Page{}, false, ErrCollectionFence
	}
	if err != nil {
		return collection.Page{}, false, collectionStorageError(ctx)
	}
	if target.WriteSeq() == int64(^uint64(0)>>1) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	writeSeq := target.WriteSeq() + 1
	order := market.WriteOrder{
		SwitchVersion: int64(target.SwitchVersion()),
		WriteSequence: writeSeq,
	}
	resolved, err := resolveAttemptWrites(ctx, tx, string(target.Platform()), appID, input.Attempts)
	if err != nil {
		return collection.Page{}, false, err
	}
	batch := observationBatch{
		AppID:    appID,
		Platform: string(target.Platform()),
		Side:     side,
		Order:    order,
		Attempts: resolved,
	}
	snapshots, err := prepareBatch(batch, true)
	if err != nil {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}
	for _, snapshot := range snapshots {
		if snapshot.collectedAt.After(input.CollectedAt) {
			return collection.Page{}, false, ErrCollectionInvalidInput
		}
	}
	digest := summaryPageDigest(input, writeSeq, batch, snapshots)
	if digest == ([32]byte{}) {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	committedAt, err := collectionDatabaseTime(ctx, tx, input.CollectedAt)
	if err != nil {
		return collection.Page{}, false, err
	}
	page, err := collection.NewPage(collection.PageInput{
		TargetID:      input.TargetID,
		WriteSeq:      writeSeq,
		CursorBefore:  input.CursorBefore,
		CursorAfter:   input.CursorAfter,
		PayloadDigest: digest,
		CollectedAt:   input.CollectedAt,
		CommittedAt:   committedAt,
		AccountID:     input.AccountID,
		ExitAddress:   input.ExitAddress,
	})
	if err != nil {
		return collection.Page{}, false, ErrCollectionInvalidInput
	}
	compressed, payloadBytes, err := compressPagePayload(input.Payload)
	if err != nil {
		return collection.Page{}, false, err
	}
	pageDigest := page.PayloadDigest()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO collection_latest_pages (
    target_id, write_seq, cursor_before, cursor_after, payload_digest,
    collected_at, committed_at, account_id, exit_address, payload_gzip, payload_bytes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (target_id) DO UPDATE SET
    write_seq = EXCLUDED.write_seq,
    cursor_before = EXCLUDED.cursor_before,
    cursor_after = EXCLUDED.cursor_after,
    payload_digest = EXCLUDED.payload_digest,
    collected_at = EXCLUDED.collected_at,
    committed_at = EXCLUDED.committed_at,
    account_id = EXCLUDED.account_id,
    exit_address = EXCLUDED.exit_address,
    payload_gzip = EXCLUDED.payload_gzip,
    payload_bytes = EXCLUDED.payload_bytes`,
		int64(page.TargetID()), page.WriteSeq(), collectionCursorBytes(page.CursorBefore()),
		collectionCursorBytes(page.CursorAfter()), pageDigest[:], page.CollectedAt(), page.CommittedAt(),
		nullableAccountID(input.AccountID), nullableAddr(input.ExitAddress),
		nullableBytes(compressed), nullablePositiveInt64(payloadBytes),
	); err != nil {
		return collection.Page{}, false, mapCollectionWriteError(ctx, err)
	}
	marketApplied, err := savePreparedObservationsTx(ctx, tx, batch, snapshots)
	if err != nil {
		return collection.Page{}, false, mapCollectionMarketWriteError(ctx, err)
	}
	if len(snapshots) > 0 && !marketApplied {
		return collection.Page{}, false, ErrCollectionIntegrity
	}
	refillTotal := target.RefillTotal()
	refillCursor := target.RefillCursor()
	if side == market.SideAsk {
		refillTotal = input.AskTotal
		if input.AskTotal == 0 {
			refillCursor, err = collection.EncodeAskRefill(0)
			if err != nil {
				return collection.Page{}, false, ErrCollectionIntegrity
			}
			if _, err := tx.ExecContext(ctx, `
DELETE FROM collection_tasks
WHERE target_id = $1 AND task_id <> $2 AND state = 'queued'`, int64(input.TargetID), int64(input.TaskID)); err != nil {
				return collection.Page{}, false, mapCollectionWriteError(ctx, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE collection_targets
SET write_seq = $2, refill_total = $3, refill_cursor = $4
WHERE target_id = $1`, int64(input.TargetID), writeSeq, refillTotal, collectionCursorBytes(refillCursor)); err != nil {
		return collection.Page{}, false, mapCollectionWriteError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return collection.Page{}, false, collectionStorageError(ctx)
	}
	return page, true, nil
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullablePositiveInt64(value int64) any {
	if value < 1 {
		return nil
	}
	return value
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

func summaryPageDigest(input SummaryPageCommit, writeSeq int64, batch observationBatch, snapshots []attemptSnapshot) [32]byte {
	digest := sha256.New()
	digestBytes(digest, []byte("buff-go.summary-page.v2"))
	digestInt64(digest, int64(input.TargetID))
	digestInt64(digest, int64(input.TaskID))
	digestInt64(digest, writeSeq)
	digestBytes(digest, input.CursorBefore.Bytes())
	digestBytes(digest, input.CursorAfter.Bytes())
	digestTime(digest, input.CollectedAt)
	digestInt64(digest, batch.AppID)
	digestBytes(digest, []byte(batch.Platform))
	digestBytes(digest, []byte(batch.Side))
	digestInt64(digest, batch.Order.SwitchVersion)
	digestInt64(digest, batch.Order.WriteSequence)
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
