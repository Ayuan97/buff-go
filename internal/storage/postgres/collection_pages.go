package postgres

import (
	"context"
	"database/sql"
	"time"

	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
)

// ListWorkers returns one row per combination for the run page.
func (s *Store) ListWorkers(ctx context.Context) ([]collection.WorkerSnapshot, error) {
	if err := s.validateCollectionStore(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.combination_id, c.platform, c.account_id, c.node_id,
       a.alias, a.session_state, n.name, n.region, host(n.exit_address),
       t.target_id, t.appid, t.side, t.platform
FROM account_node_combinations c
JOIN platform_accounts a ON a.account_id = c.account_id AND a.platform = c.platform
JOIN access_nodes n ON n.node_id = c.node_id
LEFT JOIN collection_tasks task
  ON task.claimed_by = c.combination_id AND task.state = 'claimed'
LEFT JOIN collection_targets t ON t.target_id = task.target_id
ORDER BY c.combination_id`)
	if err != nil {
		return nil, collectionStorageError(ctx)
	}
	defer rows.Close()

	workers := make([]collection.WorkerSnapshot, 0)
	type claimed struct {
		index    int
		targetID collection.TargetID
		platform collection.Platform
		side     market.Side
	}
	claims := make([]claimed, 0)
	for rows.Next() {
		var (
			combinationID, accountID, nodeID       int64
			platform, alias, session, name, region string
			exit                                   sql.NullString
			targetID, appID                        sql.NullInt64
			side, targetPlatform                   sql.NullString
		)
		if err := rows.Scan(&combinationID, &platform, &accountID, &nodeID,
			&alias, &session, &name, &region, &exit,
			&targetID, &appID, &side, &targetPlatform); err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		worker := collection.WorkerSnapshot{
			Combination: resource.AccountNodeCombination{
				ID:        resource.CombinationID(combinationID),
				Platform:  resource.Platform(platform),
				AccountID: resource.AccountID(accountID),
				NodeID:    resource.NodeID(nodeID),
			},
			AccountAlias: alias,
			SessionState: resource.AccountSessionState(session),
			NodeName:     name,
			ExitAddress:  exit.String,
			Region:       resource.NodeRegion(region),
			Idle:         !targetID.Valid,
		}
		if targetID.Valid {
			claim := &collection.WorkerClaim{
				TargetID: collection.TargetID(targetID.Int64),
				AppID:    appID.Int64,
				Side:     market.Side(side.String),
				Platform: collection.Platform(targetPlatform.String),
			}
			worker.Claim = claim
			claims = append(claims, claimed{
				index:    len(workers),
				targetID: claim.TargetID,
				platform: claim.Platform,
				side:     claim.Side,
			})
		}
		workers = append(workers, worker)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	pages, err := s.latestPagesByWorker(ctx)
	if err != nil {
		return nil, err
	}
	loaded := make(map[collection.TargetID][]collection.WorkerItem)
	for _, item := range claims {
		items, err := s.latestPageItems(ctx, item.targetID, item.platform, item.side)
		if err != nil {
			return nil, err
		}
		loaded[item.targetID] = items
		if workers[item.index].Claim != nil {
			workers[item.index].Claim.Items = items
		}
	}
	for index := range workers {
		page, ok := matchWorkerPage(workers[index], pages)
		if !ok {
			continue
		}
		items, ok := loaded[page.TargetID]
		if !ok {
			items, err = s.latestPageItems(ctx, page.TargetID, page.Platform, page.Side)
			if err != nil {
				return nil, err
			}
			loaded[page.TargetID] = items
		}
		page.Items = items
		workers[index].LastPage = &page
	}
	return workers, nil
}

type workerPageRow struct {
	accountID   int64
	exit        string
	targetID    collection.TargetID
	appID       int64
	side        market.Side
	platform    collection.Platform
	committedAt time.Time
}

func (s *Store) latestPagesByWorker(ctx context.Context) ([]workerPageRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT ON (pg.account_id, host(pg.exit_address))
       pg.account_id, COALESCE(host(pg.exit_address), ''),
       pg.target_id, pg.committed_at, t.appid, t.side, t.platform
FROM collection_latest_pages pg
JOIN collection_targets t ON t.target_id = pg.target_id
WHERE pg.account_id IS NOT NULL
ORDER BY pg.account_id, host(pg.exit_address), pg.committed_at DESC`)
	if err != nil {
		return nil, mapCollectionReadError(ctx, err)
	}
	defer rows.Close()

	pages := make([]workerPageRow, 0)
	for rows.Next() {
		var page workerPageRow
		var side, platform string
		if err := rows.Scan(&page.accountID, &page.exit, &page.targetID, &page.committedAt, &page.appID, &side, &platform); err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		page.side = market.Side(side)
		page.platform = collection.Platform(platform)
		pages = append(pages, page)
	}
	if err := rows.Err(); err != nil {
		return nil, collectionStorageError(ctx)
	}
	return pages, nil
}

func matchWorkerPage(worker collection.WorkerSnapshot, pages []workerPageRow) (collection.WorkerPage, bool) {
	accountID := int64(worker.Combination.AccountID)
	var fallback workerPageRow
	var hasFallback bool
	for _, page := range pages {
		if page.accountID != accountID {
			continue
		}
		if worker.ExitAddress != "" && page.exit != "" && worker.ExitAddress == page.exit {
			return toWorkerPage(page), true
		}
		if page.exit == "" || worker.ExitAddress == "" {
			fallback, hasFallback = page, true
		}
	}
	if hasFallback {
		return toWorkerPage(fallback), true
	}
	return collection.WorkerPage{}, false
}

func toWorkerPage(page workerPageRow) collection.WorkerPage {
	return collection.WorkerPage{
		TargetID:    page.targetID,
		AppID:       page.appID,
		Side:        page.side,
		Platform:    page.platform,
		CommittedAt: page.committedAt,
	}
}

func (s *Store) latestPageItems(
	ctx context.Context,
	targetID collection.TargetID,
	platform collection.Platform,
	side market.Side,
) ([]collection.WorkerItem, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT p.product_id, p.name, a.status, lp.price_cny_cents
FROM collection_latest_pages pg
JOIN collection_targets t ON t.target_id = pg.target_id
JOIN market_latest_attempts a
  ON a.platform = t.platform AND a.side = t.side
 AND a.switch_version = pg.switch_version AND a.write_seq = pg.write_seq
JOIN steam_products p ON p.product_id = a.product_id AND p.appid = t.appid
LEFT JOIN market_last_present lp
  ON lp.product_id = a.product_id AND lp.platform = a.platform AND lp.side = a.side
WHERE pg.target_id = $1 AND t.platform = $2 AND t.side = $3
ORDER BY p.product_id`, int64(targetID), string(platform), string(side))
	if err != nil {
		return nil, mapCollectionReadError(ctx, err)
	}
	defer rows.Close()

	items := make([]collection.WorkerItem, 0)
	for rows.Next() {
		var item collection.WorkerItem
		var price sql.NullInt64
		if err := rows.Scan(&item.ProductID, &item.Name, &item.Status, &price); err != nil {
			return nil, mapCollectionReadError(ctx, err)
		}
		if price.Valid {
			item.PriceCents = &price.Int64
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, mapCollectionReadError(ctx, err)
	}
	return items, nil
}
