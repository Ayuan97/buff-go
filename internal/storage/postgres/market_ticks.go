package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/market"
)

// PriceTickRetention 是变价记录的保留期，到期由写入事务清掉。
const PriceTickRetention = 30 * 24 * time.Hour

// PriceTick 是一次人民币分变动。PrevCents 为空表示这个方向第一次入价。
type PriceTick struct {
	TickID      int64
	ProductID   catalog.ProductID
	AppID       int64
	Name        string
	Platform    string
	Side        market.Side
	PrevCents   *int64
	PriceCents  int64
	CollectedAt time.Time
}

// PriceTickFilter 筛变价记录。
type PriceTickFilter struct {
	ProductID int64
	Platform  string
	Side      market.Side
	Limit     int
}

func shouldRecordPriceTick(exists bool, oldCents, newCents market.CNYCents) bool {
	return !exists || oldCents != newCents
}

func insertPriceTick(
	ctx context.Context,
	tx *sql.Tx,
	key MarketKey,
	prev *int64,
	price market.CNYCents,
	collectedAt time.Time,
	order market.WriteOrder,
) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO market_price_ticks (
    product_id, platform, side, prev_cents, price_cny_cents, collected_at, switch_version, write_seq
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		key.ProductID, key.Platform, key.Side, nullableInt64(prev), int64(price),
		collectedAt, order.SwitchVersion, order.WriteSequence,
	)
	if err != nil {
		return fmt.Errorf("write price tick: %w", err)
	}
	return nil
}

func purgeExpiredPriceTicks(ctx context.Context, tx *sql.Tx, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM market_price_ticks
WHERE collected_at < $1`, now.Add(-PriceTickRetention))
	if err != nil {
		return fmt.Errorf("purge price ticks: %w", err)
	}
	return nil
}

func validatePriceTickFilter(filter PriceTickFilter) error {
	if filter.Limit < 1 || filter.Limit > 200 {
		return fmt.Errorf("price tick limit must be between 1 and 200")
	}
	if filter.ProductID != 0 {
		if err := validateProductID(catalog.ProductID(filter.ProductID)); err != nil {
			return err
		}
	}
	if filter.Platform != "" {
		if err := validatePlatform(filter.Platform); err != nil {
			return err
		}
	}
	if filter.Side != "" && filter.Side != market.SideBid && filter.Side != market.SideAsk {
		return fmt.Errorf("invalid quote side %q", filter.Side)
	}
	return nil
}

// ListPriceTicks 按时间倒序返回变价记录。
func (s *Store) ListPriceTicks(ctx context.Context, filter PriceTickFilter) ([]PriceTick, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if err := validatePriceTickFilter(filter); err != nil {
		return nil, err
	}
	query := `
SELECT t.tick_id, t.product_id, p.appid, p.name, t.platform, t.side,
       t.prev_cents, t.price_cny_cents, t.collected_at
FROM market_price_ticks t
JOIN steam_products p ON p.product_id = t.product_id`
	args := make([]any, 0, 4)
	conditions := make([]string, 0, 3)
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.ProductID != 0 {
		add("t.product_id = $%d", filter.ProductID)
	}
	if filter.Platform != "" {
		add("t.platform = $%d", filter.Platform)
	}
	if filter.Side != "" {
		add("t.side = $%d", string(filter.Side))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, filter.Limit)
	query += fmt.Sprintf(" ORDER BY t.collected_at DESC, t.tick_id DESC LIMIT $%d", len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, ErrCollectionStorage
	}
	defer rows.Close()
	out := make([]PriceTick, 0)
	for rows.Next() {
		var tick PriceTick
		var prev sql.NullInt64
		if err := rows.Scan(
			&tick.TickID, &tick.ProductID, &tick.AppID, &tick.Name, &tick.Platform, &tick.Side,
			&prev, &tick.PriceCents, &tick.CollectedAt,
		); err != nil {
			return nil, ErrCollectionStorage
		}
		tick.PrevCents = int64FromNull(prev)
		tick.CollectedAt = normalizePostgresTime(tick.CollectedAt)
		out = append(out, tick)
	}
	if err := rows.Err(); err != nil {
		return nil, ErrCollectionStorage
	}
	return out, nil
}
