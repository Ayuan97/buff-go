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

// QuoteSort 是行情列表的排序方式。
type QuoteSort string

const (
	QuoteSortPriceDesc    QuoteSort = "price_desc"
	QuoteSortPriceAsc     QuoteSort = "price_asc"
	QuoteSortListingsDesc QuoteSort = "listings_desc"
	QuoteSortName         QuoteSort = "name"
	QuoteSortDropDesc     QuoteSort = "drop_desc"
	QuoteSortDropPctDesc  QuoteSort = "drop_pct_desc"
)

// DropWindow 是降价统计回看多久。空值按 24 小时。
type DropWindow string

const (
	DropWindow24h DropWindow = "24h"
	DropWindow7d  DropWindow = "7d"
	DropWindow30d DropWindow = "30d"
)

// MarketQuoteFilter 是行情列表的筛选条件，零值字段表示不限制。
type MarketQuoteFilter struct {
	AppID     int64
	ProductID int64
	Platform  string
	Side      market.Side
	// Keyword 按商品名包含匹配，大小写不敏感。
	Keyword   string
	ItemType  string
	ItemTypes []string
	SteamCats []string
	// 价格区间以分为单位，只筛有最近有效价的商品。
	MinCents *int64
	MaxCents *int64
	// DropWindow 决定最高价和降幅回看多久，空值按 24 小时。
	DropWindow DropWindow
	// DropsOnly 只看窗口内现价低于最高价的商品。
	DropsOnly bool
	// MinDropCents 只看窗口降价至少这么多分的商品。
	MinDropCents *int64
	Sort         QuoteSort
	Limit        int
	Offset       int
}

// MarketQuoteResult 是一页行情与命中总数，供前端分页。
type MarketQuoteResult struct {
	Quotes []MarketQuote
	Total  int64
}

// MarketFacets 是筛选器的可选项，从已采数据推导。
type MarketFacets struct {
	AppIDs    []int64
	ItemTypes []string
}

const quoteSelectColumns = `
       p.product_id, p.appid, p.name, p.icon_path, p.item_type, p.name_color,
       a.platform, a.side, a.status, a.reason_code, a.collected_at, a.source_time,
       lp.price_cny_cents, lp.order_count, lp.collected_at,
       ` + quoteDropExpr + `,
       tk.high_cents,
       ` + quoteDropPctExpr + `,
       tk.drop_count,
       tk.last_drop_at`

const quoteFromClause = `
FROM market_latest_attempts a
JOIN steam_products p ON p.product_id = a.product_id
LEFT JOIN market_last_present lp
  ON lp.product_id = a.product_id AND lp.platform = a.platform AND lp.side = a.side`

// 窗口最高价取变价点的现价和变动前价，避免窗口第一跳丢掉切入点。
const quoteTickJoin = `
LEFT JOIN (
  SELECT t.product_id, t.platform, t.side,
         GREATEST(MAX(t.price_cny_cents), MAX(t.prev_cents)) AS high_cents,
         COUNT(*) FILTER (
           WHERE t.prev_cents IS NOT NULL AND t.prev_cents > t.price_cny_cents
         ) AS drop_count,
         MAX(t.collected_at) FILTER (
           WHERE t.prev_cents IS NOT NULL AND t.prev_cents > t.price_cny_cents
         ) AS last_drop_at
  FROM market_price_ticks t
  WHERE t.collected_at >= $1
  GROUP BY t.product_id, t.platform, t.side
) tk ON tk.product_id = a.product_id AND tk.platform = a.platform AND tk.side = a.side`

const quoteDropExpr = `CASE
         WHEN lp.price_cny_cents IS NOT NULL AND tk.high_cents IS NOT NULL AND tk.high_cents > lp.price_cny_cents
         THEN tk.high_cents - lp.price_cny_cents
       END`

const quoteDropPctExpr = `CASE
         WHEN lp.price_cny_cents IS NOT NULL AND tk.high_cents > lp.price_cny_cents AND tk.high_cents > 0
         THEN (tk.high_cents - lp.price_cny_cents) * 10000 / tk.high_cents
       END`

const quoteDropPredicate = `lp.price_cny_cents IS NOT NULL AND tk.high_cents IS NOT NULL AND tk.high_cents > lp.price_cny_cents`

// ListMarketQuotes lists latest attempts with catalog media and last present prices.
func (s *Store) ListMarketQuotes(ctx context.Context, filter MarketQuoteFilter) (MarketQuoteResult, error) {
	if s == nil || s.db == nil {
		return MarketQuoteResult{}, fmt.Errorf("PostgreSQL store is required")
	}
	if err := validateQuoteFilter(filter); err != nil {
		return MarketQuoteResult{}, err
	}
	since := dropSince(filter.DropWindow, time.Now().UTC())
	countFrom, countWhere, countArgs := quoteFrom(filter, quoteNeedsTickJoin(filter), since)
	listFrom, listWhere, listArgs := quoteFrom(filter, true, since)

	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*)`+countFrom+countWhere, countArgs...).Scan(&total); err != nil {
		return MarketQuoteResult{}, fmt.Errorf("count market quotes: %w", err)
	}

	query := `SELECT` + quoteSelectColumns + listFrom + listWhere +
		` ORDER BY ` + quoteOrderBy(filter.Sort) +
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(listArgs)+1, len(listArgs)+2)
	rows, err := s.db.QueryContext(ctx, query, append(listArgs, filter.Limit, filter.Offset)...)
	if err != nil {
		return MarketQuoteResult{}, fmt.Errorf("list market quotes: %w", err)
	}
	defer rows.Close()

	quotes := make([]MarketQuote, 0, filter.Limit)
	for rows.Next() {
		quote, err := scanMarketQuote(rows)
		if err != nil {
			return MarketQuoteResult{}, err
		}
		quotes = append(quotes, quote)
	}
	if err := rows.Err(); err != nil {
		return MarketQuoteResult{}, fmt.Errorf("list market quotes: %w", err)
	}
	return MarketQuoteResult{Quotes: quotes, Total: total}, nil
}

// QuoteFacets 列出已采数据里出现过的游戏与分类，供筛选器使用。
// appid 为 0 时分类跨全部游戏。
func (s *Store) QuoteFacets(ctx context.Context, appid int64) (MarketFacets, error) {
	if s == nil || s.db == nil {
		return MarketFacets{}, fmt.Errorf("PostgreSQL store is required")
	}
	if appid != 0 {
		if err := validateAppID(appid); err != nil {
			return MarketFacets{}, err
		}
	}
	facets := MarketFacets{AppIDs: make([]int64, 0, 4), ItemTypes: make([]string, 0, 8)}

	appRows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT p.appid
FROM market_latest_attempts a
JOIN steam_products p ON p.product_id = a.product_id
ORDER BY p.appid`)
	if err != nil {
		return MarketFacets{}, fmt.Errorf("list quote appids: %w", err)
	}
	defer appRows.Close()
	for appRows.Next() {
		var value int64
		if err := appRows.Scan(&value); err != nil {
			return MarketFacets{}, fmt.Errorf("scan quote appid: %w", err)
		}
		facets.AppIDs = append(facets.AppIDs, value)
	}
	if err := appRows.Err(); err != nil {
		return MarketFacets{}, fmt.Errorf("list quote appids: %w", err)
	}

	typeRows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT p.item_type
FROM market_latest_attempts a
JOIN steam_products p ON p.product_id = a.product_id
WHERE p.item_type IS NOT NULL AND ($1 = 0 OR p.appid = $1)
ORDER BY p.item_type`, appid)
	if err != nil {
		return MarketFacets{}, fmt.Errorf("list quote item types: %w", err)
	}
	defer typeRows.Close()
	for typeRows.Next() {
		var value string
		if err := typeRows.Scan(&value); err != nil {
			return MarketFacets{}, fmt.Errorf("scan quote item type: %w", err)
		}
		facets.ItemTypes = append(facets.ItemTypes, value)
	}
	if err := typeRows.Err(); err != nil {
		return MarketFacets{}, fmt.Errorf("list quote item types: %w", err)
	}
	return facets, nil
}

func validateQuoteFilter(filter MarketQuoteFilter) error {
	if filter.Limit < 1 || filter.Limit > 200 {
		return fmt.Errorf("quote limit must be between 1 and 200")
	}
	if filter.Offset < 0 || filter.Offset > 1_000_000 {
		return fmt.Errorf("quote offset out of range")
	}
	if filter.AppID != 0 {
		if err := validateAppID(filter.AppID); err != nil {
			return err
		}
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
	if len(filter.Keyword) > 128 || len(filter.ItemType) > 128 {
		return fmt.Errorf("quote filter text is too long")
	}
	if len(filter.ItemTypes) > 128 || len(filter.SteamCats) > 8 {
		return fmt.Errorf("quote filter selection is too large")
	}
	for _, value := range filter.ItemTypes {
		if len(value) > 128 {
			return fmt.Errorf("quote filter text is too long")
		}
	}
	for _, slug := range filter.SteamCats {
		if !catalog.ValidRustCategory(slug) {
			return fmt.Errorf("unknown steam category %q", slug)
		}
	}
	if filter.MinCents != nil && *filter.MinCents < 0 {
		return fmt.Errorf("quote min price must not be negative")
	}
	if filter.MaxCents != nil && *filter.MaxCents < 0 {
		return fmt.Errorf("quote max price must not be negative")
	}
	if filter.MinCents != nil && filter.MaxCents != nil && *filter.MinCents > *filter.MaxCents {
		return fmt.Errorf("quote min price exceeds max price")
	}
	if filter.MinDropCents != nil && (*filter.MinDropCents < 0 || *filter.MinDropCents > 1_000_000_000) {
		return fmt.Errorf("quote min drop is out of range")
	}
	switch filter.DropWindow {
	case "", DropWindow24h, DropWindow7d, DropWindow30d:
	default:
		return fmt.Errorf("invalid drop window %q", filter.DropWindow)
	}
	switch filter.Sort {
	case "", QuoteSortPriceDesc, QuoteSortPriceAsc, QuoteSortListingsDesc, QuoteSortName, QuoteSortDropDesc, QuoteSortDropPctDesc:
		return nil
	default:
		return fmt.Errorf("invalid quote sort %q", filter.Sort)
	}
}

func quoteNeedsTickJoin(filter MarketQuoteFilter) bool {
	return filter.DropsOnly || filter.MinDropCents != nil
}

func dropSince(window DropWindow, now time.Time) time.Time {
	switch window {
	case DropWindow7d:
		return now.Add(-7 * 24 * time.Hour)
	case DropWindow30d:
		return now.Add(-30 * 24 * time.Hour)
	default:
		return now.Add(-24 * time.Hour)
	}
}

func quoteFrom(filter MarketQuoteFilter, withTicks bool, since time.Time) (from, where string, args []any) {
	from = quoteFromClause
	argBase := 0
	if withTicks {
		from += quoteTickJoin
		args = append(args, since)
		argBase = 1
	}
	where, rest := quoteWhere(filter, argBase)
	return from, where, append(args, rest...)
}

func quoteWhere(filter MarketQuoteFilter, argBase int) (string, []any) {
	conditions := make([]string, 0, 8)
	args := make([]any, 0, 8)
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, argBase+len(args)))
	}
	if filter.AppID != 0 {
		add("p.appid = $%d", filter.AppID)
	}
	if filter.ProductID != 0 {
		add("p.product_id = $%d", filter.ProductID)
	}
	if filter.Platform != "" {
		add("a.platform = $%d", filter.Platform)
	}
	if filter.Side != "" {
		add("a.side = $%d", string(filter.Side))
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		// 转义 LIKE 元字符，否则用户输入的 % 会匹配全部商品
		add(`p.name ILIKE '%%' || $%d || '%%'`, escapeLikePattern(keyword))
		conditions[len(conditions)-1] += ` ESCAPE '\'`
	}
	if types, apply := quoteItemTypeFilter(filter); apply {
		if len(types) == 0 {
			conditions = append(conditions, "FALSE")
		} else {
			add("p.item_type = ANY($%d)", textArrayValue(types))
		}
	}
	if filter.MinCents != nil {
		add("lp.price_cny_cents >= $%d", *filter.MinCents)
	}
	if filter.MaxCents != nil {
		add("lp.price_cny_cents <= $%d", *filter.MaxCents)
	}
	if filter.MinDropCents != nil {
		add("("+quoteDropExpr+") >= $%d", *filter.MinDropCents)
	} else if filter.DropsOnly {
		conditions = append(conditions, quoteDropPredicate)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func quoteItemTypeFilter(filter MarketQuoteFilter) ([]string, bool) {
	types := uniqueNonEmpty(append(append([]string{}, filter.ItemTypes...), filter.ItemType))
	if len(filter.SteamCats) == 0 {
		return types, len(types) > 0
	}
	labels := catalog.RustLabelsForCategories(filter.SteamCats)
	if len(types) == 0 {
		return labels, true
	}
	wanted := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		wanted[label] = struct{}{}
	}
	out := make([]string, 0, len(types))
	for _, value := range types {
		if _, ok := wanted[value]; ok {
			out = append(out, value)
		}
	}
	return out, true
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func escapeLikePattern(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	return strings.ReplaceAll(value, "_", `\_`)
}

// 排序末尾统一补主键，保证分页时行序稳定，不会漏行或重复。
func quoteOrderBy(sort QuoteSort) string {
	const tiebreak = "p.product_id, a.platform, a.side"
	switch sort {
	case QuoteSortPriceAsc:
		return "lp.price_cny_cents ASC NULLS LAST, " + tiebreak
	case QuoteSortListingsDesc:
		return "lp.order_count DESC NULLS LAST, " + tiebreak
	case QuoteSortName:
		return "p.name ASC, " + tiebreak
	case QuoteSortDropDesc:
		return quoteDropExpr + " DESC NULLS LAST, " + tiebreak
	case QuoteSortDropPctDesc:
		return quoteDropPctExpr + " DESC NULLS LAST, " + tiebreak
	default:
		return "lp.price_cny_cents DESC NULLS LAST, " + tiebreak
	}
}

func scanMarketQuote(rows *sql.Rows) (MarketQuote, error) {
	var quote MarketQuote
	var iconPath, itemType, nameColor sql.NullString
	var sourceTime, presentAt sql.NullTime
	var presentCents, presentCount, dropCents, highCents, dropPctBP, dropCount sql.NullInt64
	var lastDropAt sql.NullTime
	if err := rows.Scan(
		&quote.ProductID, &quote.AppID, &quote.Name, &iconPath, &itemType, &nameColor,
		&quote.Platform, &quote.Side, &quote.Status, &quote.ReasonCode,
		&quote.CollectedAt, &sourceTime,
		&presentCents, &presentCount, &presentAt, &dropCents,
		&highCents, &dropPctBP, &dropCount, &lastDropAt,
	); err != nil {
		return MarketQuote{}, fmt.Errorf("scan market quote: %w", err)
	}
	quote.Media = catalog.ProductMedia{
		IconPath:  iconPath.String,
		ItemType:  itemType.String,
		NameColor: nameColor.String,
	}
	quote.SourceTime = cloneTime(timeFromNull(sourceTime))
	if presentCents.Valid {
		cents := market.CNYCents(presentCents.Int64)
		quote.PresentCents = &cents
	}
	quote.PresentOrderCount = int64FromNull(presentCount)
	quote.PresentCollectedAt = cloneTime(timeFromNull(presentAt))
	quote.DropCents = int64FromNull(dropCents)
	quote.HighCents = int64FromNull(highCents)
	quote.DropPctBP = int64FromNull(dropPctBP)
	if dropCount.Valid && dropCount.Int64 > 0 {
		quote.DropCount = int64FromNull(dropCount)
	}
	quote.LastDropAt = cloneTime(timeFromNull(lastDropAt))
	return quote, nil
}
