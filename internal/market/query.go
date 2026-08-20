package market

import (
	"errors"
	"time"

	"buff-go/internal/catalog"
)

// ErrStorage marks a market query failure caused by persistence or infrastructure.
var ErrStorage = errors.New("market storage operation failed")

// QuoteSort is the server-side quote ordering mode.
type QuoteSort string

const (
	QuoteSortPriceDesc    QuoteSort = "price_desc"
	QuoteSortPriceAsc     QuoteSort = "price_asc"
	QuoteSortListingsDesc QuoteSort = "listings_desc"
	QuoteSortName         QuoteSort = "name"
	QuoteSortDropDesc     QuoteSort = "drop_desc"
	QuoteSortDropPctDesc  QuoteSort = "drop_pct_desc"
)

// DropWindow is the lookback window for price-drop statistics.
type DropWindow string

const (
	DropWindow24h DropWindow = "24h"
	DropWindow7d  DropWindow = "7d"
	DropWindow30d DropWindow = "30d"
)

// QuoteFilter contains server-side quote filters and pagination.
type QuoteFilter struct {
	AppID        int64
	ProductID    int64
	Platform     string
	Side         Side
	Keyword      string
	ItemType     string
	ItemTypes    []string
	SteamCats    []string
	MinCents     *int64
	MaxCents     *int64
	DropWindow   DropWindow
	DropsOnly    bool
	MinDropCents *int64
	Sort         QuoteSort
	Limit        int
	Offset       int
}

// Quote is one latest market attempt joined with catalog and price history.
type Quote struct {
	ProductID          catalog.ProductID
	AppID              int64
	Name               string
	Media              catalog.ProductMedia
	Platform           string
	Side               Side
	Status             ObservationStatus
	ReasonCode         string
	CollectedAt        time.Time
	SourceTime         *time.Time
	PresentCents       *CNYCents
	PresentOrderCount  *int64
	PresentCollectedAt *time.Time
	DropCents          *int64
	HighCents          *int64
	DropPctBP          *int64
	DropCount          *int64
	LastDropAt         *time.Time
}

// QuoteResult is one page of quotes and the total match count.
type QuoteResult struct {
	Quotes []Quote
	Total  int64
}

// QuoteFacets contains filter values derived from collected data.
type QuoteFacets struct {
	AppIDs    []int64
	ItemTypes []string
}

// PriceTick is one CNY-cent price change.
type PriceTick struct {
	TickID      int64
	ProductID   catalog.ProductID
	AppID       int64
	Name        string
	Platform    string
	Side        Side
	PrevCents   *int64
	PriceCents  int64
	CollectedAt time.Time
}

// PriceTickFilter limits recent price changes.
type PriceTickFilter struct {
	ProductID int64
	Platform  string
	Side      Side
	Limit     int
}
