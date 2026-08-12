// Package steam is a legacy wire client for candidate Steam Community Market
// endpoints. Goal 0B has not verified their field semantics, currency IDs,
// authentication rules, or bid/ask interpretations. Historical convenience
// names in these DTOs are not approved market-domain evidence.
package steam

import "time"

const legacyPriceOverviewDefaultCurrencyID = 1

// SearchParams configures market/search/render.
type SearchParams struct {
	AppID              int64
	Query              string
	Start              int
	Count              int    // Legacy client clamps to 1..100; platform limit is unverified.
	SortColumn         string // popular|price|quantity|name
	SortDir            string // asc|desc
	SearchDescriptions bool
	// Category filters: map facet id (e.g. "252490_steamcat") → tag keys without "tag_" prefix
	// (e.g. "steamcat.weapon"). Client sends category_{facet}[]=tag_{tag}.
	Categories map[string][]string
}

// SearchResult is one row from search/render (list page).
type SearchResult struct {
	Name           string
	HashName       string
	SellListings   int   // Raw sell_listings; count and zero semantics are unverified.
	SellPriceCents int64 // Legacy name for raw sell_price; unit and zero semantics are unverified.
	SellPriceText  string
	SalePriceText  string
	AppID          int64
	AppName        string
	ClassID        string
	Commodity      bool
	IconURL        string
	Type           string
	MarketHashName string
}

// SearchPage is one search/render response.
type SearchPage struct {
	Success    bool
	Start      int
	PageSize   int
	TotalCount int
	Results    []SearchResult
}

// PriceOverview is market/priceoverview for one item.
type PriceOverview struct {
	Success     bool
	LowestPrice string // Raw lowest_price text; market meaning is unverified.
	MedianPrice string
	Volume      string // Raw volume text; time range and unit are unverified.
	Currency    int
	AppID       int64
	MarketHash  string
}

// DepthLevel preserves one raw graph row using the legacy interpretation.
// Price units and cumulative quantity semantics remain unverified.
type DepthLevel struct {
	Price      float64
	Cumulative int
	Text       string
}

// CompactLevel preserves one legacy price/quantity pair from a compact array.
type CompactLevel struct {
	PriceMinor int64 // Legacy name; price unit is unverified.
	Quantity   int
}

// OrderBook is the legacy interpretation of one histogram or orderbook response.
// Direction, units, and count semantics remain unverified.
type OrderBook struct {
	AppID          int64
	MarketHashName string
	ItemNameID     string // Legacy candidate parameter; endpoint role is unverified.
	Currency       int
	Country        string
	Language       string

	LowestSell  int64 // Candidate field; ask and unit semantics are unverified.
	HighestBuy  int64 // Candidate field; bid and unit semantics are unverified.
	SellOrders  int   // Candidate count; order/item semantics are unverified.
	BuyOrders   int   // Candidate count; order/item semantics are unverified.
	PricePrefix string
	PriceSuffix string

	// Ladder from classic histogram (prefer for graphs).
	SellGraph []DepthLevel
	BuyGraph  []DepthLevel

	// Compact ladder from beta /market/orderbook (price,qty pairs).
	SellCompact []CompactLevel
	BuyCompact  []CompactLevel

	Source string // "histogram" | "orderbook"
}

// PriceHistoryPoint preserves one raw pricehistory row using legacy field names.
type PriceHistoryPoint struct {
	TimeRaw string
	Price   float64
	Volume  string
}

// PriceHistory is the legacy DTO for market/pricehistory.
type PriceHistory struct {
	Success     bool
	PricePrefix string
	PriceSuffix string
	Points      []PriceHistoryPoint
	AppID       int64
	MarketHash  string
}

// FacetTag is one filter option under a facet.
type FacetTag struct {
	Tag           string
	LocalizedName string
	Matches       int
}

// Facet is one filter group (Category / Item Type, …).
type Facet struct {
	ID            string // e.g. 252490_itemclass
	Name          string // itemclass / steamcat
	LocalizedName string
	Tags          []FacetTag
}

// AppFilters is market/appfilters/{appid}.
type AppFilters struct {
	Success bool
	AppID   int64
	Facets  []Facet
}

// Listing is the legacy DTO for one listings/.../render row.
type Listing struct {
	ListingID      string
	PriceMinor     int64
	FeeMinor       int64
	ConvertedPrice int64
	ConvertedFee   int64
	CurrencyID     int
	SteamFee       int64
	PublisherFee   int64
	AssetID        string
	Amount         string
}

// ListingsPage is listings/{appid}/{name}/render.
type ListingsPage struct {
	Success    bool
	Start      int
	PageSize   int
	TotalCount int
	Listings   []Listing
}

// RecentCompleted is a slice of global market completions (all apps).
type RecentCompleted struct {
	Success     bool
	More        bool
	LastTime    int64
	LastListing string
	// Raw maps kept small-use; full HTML omitted.
	ListingIDs  []string
	PurchaseIDs []string
}

// Observed is a helper timestamp for callers that persist quotes.
func Observed() time.Time { return time.Now().UTC() }
