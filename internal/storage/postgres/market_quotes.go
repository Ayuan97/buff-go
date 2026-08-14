package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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
)

// MarketQuoteFilter 是行情列表的筛选条件，零值字段表示不限制。
type MarketQuoteFilter struct {
	AppID    int64
	Platform string
	Side     market.Side
	// Keyword 按商品名包含匹配，大小写不敏感。
	Keyword  string
	ItemType string
	// 价格区间以分为单位，只筛有最近有效价的商品。
	MinCents *int64
	MaxCents *int64
	Sort     QuoteSort
	Limit    int
	Offset   int
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
       lp.price_cny_cents, lp.order_count, lp.collected_at`

const quoteFromClause = `
FROM market_latest_attempts a
JOIN steam_products p ON p.product_id = a.product_id
LEFT JOIN market_last_present lp
  ON lp.product_id = a.product_id AND lp.platform = a.platform AND lp.side = a.side`

// ListMarketQuotes lists latest attempts with catalog media and last present prices.
func (s *Store) ListMarketQuotes(ctx context.Context, filter MarketQuoteFilter) (MarketQuoteResult, error) {
	if s == nil || s.db == nil {
		return MarketQuoteResult{}, fmt.Errorf("PostgreSQL store is required")
	}
	if err := validateQuoteFilter(filter); err != nil {
		return MarketQuoteResult{}, err
	}
	where, args := quoteWhere(filter)

	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*)`+quoteFromClause+where, args...).Scan(&total); err != nil {
		return MarketQuoteResult{}, fmt.Errorf("count market quotes: %w", err)
	}

	query := `SELECT` + quoteSelectColumns + quoteFromClause + where +
		` ORDER BY ` + quoteOrderBy(filter.Sort) +
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, filter.Limit, filter.Offset)...)
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
	if filter.MinCents != nil && *filter.MinCents < 0 {
		return fmt.Errorf("quote min price must not be negative")
	}
	if filter.MaxCents != nil && *filter.MaxCents < 0 {
		return fmt.Errorf("quote max price must not be negative")
	}
	if filter.MinCents != nil && filter.MaxCents != nil && *filter.MinCents > *filter.MaxCents {
		return fmt.Errorf("quote min price exceeds max price")
	}
	switch filter.Sort {
	case "", QuoteSortPriceDesc, QuoteSortPriceAsc, QuoteSortListingsDesc, QuoteSortName:
		return nil
	default:
		return fmt.Errorf("invalid quote sort %q", filter.Sort)
	}
}

func quoteWhere(filter MarketQuoteFilter) (string, []any) {
	conditions := make([]string, 0, 7)
	args := make([]any, 0, 7)
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.AppID != 0 {
		add("p.appid = $%d", filter.AppID)
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
	if filter.ItemType != "" {
		add("p.item_type = $%d", filter.ItemType)
	}
	if filter.MinCents != nil {
		add("lp.price_cny_cents >= $%d", *filter.MinCents)
	}
	if filter.MaxCents != nil {
		add("lp.price_cny_cents <= $%d", *filter.MaxCents)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
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
	default:
		return "lp.price_cny_cents DESC NULLS LAST, " + tiebreak
	}
}

func scanMarketQuote(rows *sql.Rows) (MarketQuote, error) {
	var quote MarketQuote
	var iconPath, itemType, nameColor sql.NullString
	var sourceTime, presentAt sql.NullTime
	var presentCents, presentCount sql.NullInt64
	if err := rows.Scan(
		&quote.ProductID, &quote.AppID, &quote.Name, &iconPath, &itemType, &nameColor,
		&quote.Platform, &quote.Side, &quote.Status, &quote.ReasonCode,
		&quote.CollectedAt, &sourceTime,
		&presentCents, &presentCount, &presentAt,
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
	return quote, nil
}
