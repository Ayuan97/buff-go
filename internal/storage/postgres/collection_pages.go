package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"buff-go/internal/collection"
)

// ErrPagePayloadNotFound 表示这一页的原始响应不在保留窗口内，或这个方向本来就不产出单页响应。
var ErrPagePayloadNotFound = errors.New("collection page payload not found")

// PageSummary 是一页的展示信息。PayloadBytes 为 0 表示原始响应副本已被淘汰或从未保存。
type PageSummary struct {
	PageSequence int64
	CursorBefore string
	CursorAfter  string
	CollectedAt  time.Time
	CommittedAt  time.Time
	PayloadBytes int64
	// 执行这一页的账号与出口。000011 迁移之前提交的页没有记录，两者都为空。
	AccountID    int64
	AccountAlias string
	ExitAddress  string
}

// PageAttempt 是某一页写入的一条行情事实，附带商品名与最近一次有效价。
type PageAttempt struct {
	ProductID   int64
	AppID       int64
	Name        string
	Platform    string
	Side        string
	Status      string
	ReasonCode  string
	CollectedAt time.Time
	// SourceTime 只有 present 状态才有，empty 与失败状态是空的
	SourceTime *time.Time
	PriceCents *int64
	OrderCount *int64
}

// PageSummaries 列出一个批次已提交的页。与 Pages 不同，这里不校验游标链，
// 只为控制台展示服务。
func (s *Store) PageSummaries(ctx context.Context, id collection.RunID) ([]PageSummary, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	if id.Validate() != nil {
		return nil, ErrCollectionInvalidInput
	}
	// 账号别名用左连接：账号被删不该让采集历史查不出来
	rows, err := s.db.QueryContext(ctx, `
SELECT pg.page_sequence, pg.cursor_before, pg.cursor_after, pg.collected_at, pg.committed_at,
       COALESCE(pl.byte_size, 0), pg.account_id, ac.alias, pg.exit_address
FROM collection_pages pg
LEFT JOIN collection_page_payloads pl
  ON pl.run_id = pg.run_id AND pl.page_sequence = pg.page_sequence
LEFT JOIN platform_accounts ac ON ac.account_id = pg.account_id
WHERE pg.run_id = $1
ORDER BY pg.page_sequence`, int64(id))
	if err != nil {
		return nil, mapCollectionReadError(ctx, err)
	}
	defer func() { _ = rows.Close() }()

	summaries := make([]PageSummary, 0, 16)
	for rows.Next() {
		var summary PageSummary
		var before, after []byte
		var accountID sql.NullInt64
		var alias, exitAddress sql.NullString
		if err := rows.Scan(&summary.PageSequence, &before, &after,
			&summary.CollectedAt, &summary.CommittedAt, &summary.PayloadBytes,
			&accountID, &alias, &exitAddress); err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		summary.CursorBefore = string(before)
		summary.CursorAfter = string(after)
		summary.AccountID = accountID.Int64
		summary.AccountAlias = alias.String
		summary.ExitAddress = exitAddress.String
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, mapCollectionReadError(ctx, err)
	}
	return summaries, nil
}

// PagePayload 返回一页的平台原始响应。副本被淘汰或从未保存时报 ErrPagePayloadNotFound。
func (s *Store) PagePayload(ctx context.Context, id collection.RunID, sequence collection.Sequence) ([]byte, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	if id.Validate() != nil || sequence.Validate() != nil {
		return nil, ErrCollectionInvalidInput
	}
	var compressed []byte
	err := s.db.QueryRowContext(ctx, `
SELECT payload_gzip
FROM collection_page_payloads
WHERE run_id = $1 AND page_sequence = $2`, int64(id), int64(sequence)).Scan(&compressed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPagePayloadNotFound
	}
	if err != nil {
		return nil, mapCollectionReadError(ctx, err)
	}
	payload, err := gunzipPayload(compressed)
	if err != nil {
		return nil, ErrCollectionIntegrity
	}
	return payload, nil
}

// PageAttempts 列出一页写入的行情事实。因为 market_latest_attempts 按商品保留
// 最新一次结果，后续批次重新采到同一商品时这里会查不到那条，返回的是当前仍
// 归属这一页的部分。
func (s *Store) PageAttempts(ctx context.Context, id collection.RunID, sequence collection.Sequence) ([]PageAttempt, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	if id.Validate() != nil || sequence.Validate() != nil {
		return nil, ErrCollectionInvalidInput
	}
	run, found, err := s.Run(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrCollectionNotFound
	}
	side, hasSide := run.Side()
	switchVersion, hasSwitch := run.SwitchVersion()
	if !hasSide || !hasSwitch {
		return []PageAttempt{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT p.product_id, p.appid, p.name, a.platform, a.side, a.status, a.reason_code,
       a.collected_at, a.source_time,
       lp.price_cny_cents, lp.order_count
FROM market_latest_attempts a
JOIN steam_products p ON p.product_id = a.product_id
LEFT JOIN market_last_present lp
  ON lp.product_id = a.product_id AND lp.platform = a.platform AND lp.side = a.side
WHERE a.platform = $1 AND a.side = $2
  AND a.switch_version = $3 AND a.run_sequence = $4 AND a.page_sequence = $5
ORDER BY p.product_id`,
		string(run.Platform()), string(side), int64(switchVersion),
		int64(run.RunSequence()), int64(sequence))
	if err != nil {
		return nil, mapCollectionReadError(ctx, err)
	}
	defer func() { _ = rows.Close() }()

	attempts := make([]PageAttempt, 0, 16)
	for rows.Next() {
		var attempt PageAttempt
		var price, count sql.NullInt64
		var sourceTime sql.NullTime
		if err := rows.Scan(&attempt.ProductID, &attempt.AppID, &attempt.Name,
			&attempt.Platform, &attempt.Side, &attempt.Status, &attempt.ReasonCode,
			&attempt.CollectedAt, &sourceTime, &price, &count); err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		if sourceTime.Valid {
			attempt.SourceTime = &sourceTime.Time
		}
		if price.Valid {
			attempt.PriceCents = &price.Int64
		}
		if count.Valid {
			attempt.OrderCount = &count.Int64
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, mapCollectionReadError(ctx, err)
	}
	return attempts, nil
}
