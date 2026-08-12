package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Store persists catalog items in PostgreSQL.
type Store struct {
	db *sql.DB
}

// NewStore wraps an open *sql.DB (driver must be PostgreSQL / pgx).
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// OpenStore opens a pgx connection from DSN.
func OpenStore(dsn string) (*Store, *sql.DB, error) {
	if dsn == "" {
		return nil, nil, fmt.Errorf("empty postgres dsn")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(time.Minute)
	return NewStore(db), db, nil
}

// EnsureGame upserts a games row so items FK succeeds.
func (s *Store) EnsureGame(ctx context.Context, g GameMeta) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("nil store")
	}
	if g.AppID <= 0 {
		return fmt.Errorf("appid is required")
	}
	if g.Code == "" {
		g.Code = KnownGame(g.AppID).Code
	}
	if g.Name == "" {
		g.Name = KnownGame(g.AppID).Name
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO games (appid, code, name, enabled, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (appid) DO UPDATE SET
    code = EXCLUDED.code,
    name = EXCLUDED.name,
    enabled = EXCLUDED.enabled,
    updated_at = NOW()`,
		g.AppID, g.Code, g.Name, g.Enabled,
	)
	if err != nil {
		return fmt.Errorf("ensure game %d: %w", g.AppID, err)
	}
	return nil
}

// UpsertResult summarizes a batch upsert.
type UpsertResult struct {
	// Written is number of rows submitted (after normalization/dedupe).
	Written int
	// Affected is rows reported by the driver as affected (insert+update).
	Affected int64
}

// UpsertItems inserts or updates items on UNIQUE (appid, market_hash_name).
// Empty optional fields on conflict do not wipe existing non-empty values.
func (s *Store) UpsertItems(ctx context.Context, items []Item) (UpsertResult, error) {
	var res UpsertResult
	if s == nil || s.db == nil {
		return res, fmt.Errorf("nil store")
	}
	if len(items) == 0 {
		return res, nil
	}

	// Dedupe within batch (last wins) to avoid multi-update on same key in one tx.
	byKey := make(map[string]Item, len(items))
	order := make([]string, 0, len(items))
	for i := range items {
		it := items[i]
		if err := it.Normalize(); err != nil {
			return res, fmt.Errorf("item[%d]: %w", i, err)
		}
		key := itemKey(it.AppID, it.MarketHashName)
		if _, ok := byKey[key]; !ok {
			order = append(order, key)
		}
		byKey[key] = it
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO items (appid, market_hash_name, name, icon_url, steam_item_name_id, classid, commodity, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
ON CONFLICT (appid, market_hash_name) DO UPDATE SET
    name = CASE
        WHEN EXCLUDED.name <> '' THEN EXCLUDED.name
        ELSE items.name
    END,
    icon_url = CASE
        WHEN EXCLUDED.icon_url <> '' THEN EXCLUDED.icon_url
        ELSE items.icon_url
    END,
    steam_item_name_id = CASE
        WHEN EXCLUDED.steam_item_name_id <> '' THEN EXCLUDED.steam_item_name_id
        ELSE items.steam_item_name_id
    END,
    classid = CASE
        WHEN EXCLUDED.classid <> '' THEN EXCLUDED.classid
        ELSE items.classid
    END,
    commodity = EXCLUDED.commodity OR items.commodity,
    updated_at = NOW()`)
	if err != nil {
		return res, fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, key := range order {
		it := byKey[key]
		r, err := stmt.ExecContext(ctx,
			it.AppID, it.MarketHashName, it.Name, it.IconURL, it.SteamItemNameID, it.ClassID, it.Commodity,
		)
		if err != nil {
			return res, fmt.Errorf("upsert %s@%d: %w", it.MarketHashName, it.AppID, err)
		}
		if n, err := r.RowsAffected(); err == nil {
			res.Affected += n
		}
		res.Written++
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	return res, nil
}

// CountByAppID returns how many catalog items exist for the game.
func (s *Store) CountByAppID(ctx context.Context, appid int64) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("nil store")
	}
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE appid = $1`, appid,
	).Scan(&n)
	return n, err
}

// ListByAppID returns up to limit items for appid ordered by market_hash_name.
func (s *Store) ListByAppID(ctx context.Context, appid int64, limit int) ([]Item, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("nil store")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT appid, market_hash_name, name, icon_url, steam_item_name_id
FROM items
WHERE appid = $1
ORDER BY market_hash_name
LIMIT $2`, appid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.AppID, &it.MarketHashName, &it.Name, &it.IconURL, &it.SteamItemNameID); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListMissingSteamNameID returns up to limit items where steam_item_name_id is empty.
// The value is legacy candidate evidence; its stability and endpoint role are unverified.
func (s *Store) ListMissingSteamNameID(ctx context.Context, appid int64, limit int) ([]Item, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("nil store")
	}
	if appid <= 0 {
		return nil, fmt.Errorf("appid is required")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT appid, market_hash_name, name, icon_url, steam_item_name_id
FROM items
WHERE appid = $1
  AND (steam_item_name_id IS NULL OR TRIM(steam_item_name_id) = '')
ORDER BY market_hash_name
LIMIT $2`, appid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.AppID, &it.MarketHashName, &it.Name, &it.IconURL, &it.SteamItemNameID); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdateSteamNameID sets steam_item_name_id for one catalog row.
// Empty nameid is rejected so callers cannot wipe an existing value by accident.
func (s *Store) UpdateSteamNameID(ctx context.Context, appid int64, marketHashName, nameid string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("nil store")
	}
	marketHashName = strings.TrimSpace(marketHashName)
	nameid = strings.TrimSpace(nameid)
	if appid <= 0 {
		return fmt.Errorf("appid is required")
	}
	if marketHashName == "" {
		return fmt.Errorf("market_hash_name is required")
	}
	if nameid == "" {
		return fmt.Errorf("steam_item_name_id is required")
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE items
SET steam_item_name_id = $3, updated_at = NOW()
WHERE appid = $1 AND market_hash_name = $2`,
		appid, marketHashName, nameid,
	)
	if err != nil {
		return fmt.Errorf("update steam_item_name_id %s@%d: %w", marketHashName, appid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("item not found: %s@%d", marketHashName, appid)
	}
	return nil
}

func itemKey(appid int64, hash string) string {
	return fmt.Sprintf("%d\x00%s", appid, hash)
}
