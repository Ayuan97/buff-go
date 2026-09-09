package steam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

const defaultBidBatch = collection.BidBatchSize

// CatalogSource lists Steam products in product_id order for bid scans.
type CatalogSource interface {
	ListSteamProductsAfter(ctx context.Context, appID int64, after catalog.ProductID, limit int) ([]catalog.SteamProduct, error)
}

// SessionRecorder records the first observed session check on a live lease.
type SessionRecorder interface {
	Record(ctx context.Context, lease resource.Lease, valid bool) error
}

// Fetcher implements collection.PageFetcher for Steam summary ask/bid.
type Fetcher struct {
	baseURL   string
	pageSize  int
	bidBatch  int
	opener    SessionOpener
	catalog   CatalogSource
	sessions  SessionRecorder
	now       func() time.Time
	transport func(proxy string) (*http.Client, error)
}

// Options wires Steam HTTP collection. BaseURL empty means steamcommunity.com.
type Options struct {
	BaseURL  string
	PageSize int
	BidBatch int
	Opener   SessionOpener
	Catalog  CatalogSource
	Sessions SessionRecorder
	Clock    func() time.Time
}

// NewFetcher validates Steam collection options.
func NewFetcher(opt Options) (*Fetcher, error) {
	if opt.Opener == nil {
		return nil, fmt.Errorf("steam session opener is required")
	}
	if opt.Catalog == nil {
		return nil, fmt.Errorf("steam catalog source is required")
	}
	pageSize := opt.PageSize
	if pageSize == 0 {
		pageSize = collection.AskPageSize
	}
	if pageSize < 1 {
		return nil, fmt.Errorf("page size must be positive")
	}
	bidBatch := opt.BidBatch
	if bidBatch == 0 {
		bidBatch = defaultBidBatch
	}
	if bidBatch != collection.BidBatchSize {
		return nil, fmt.Errorf("bid batch must contain one product")
	}
	baseURL := strings.TrimRight(opt.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	clock := opt.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Fetcher{
		baseURL:   baseURL,
		pageSize:  pageSize,
		bidBatch:  bidBatch,
		opener:    opt.Opener,
		catalog:   opt.Catalog,
		sessions:  opt.Sessions,
		now:       clock,
		transport: newHTTPClient,
	}, nil
}

// FetchPage collects one Steam summary page. Credentials come from the lease.
func (f *Fetcher) FetchPage(ctx context.Context, request collection.PageFetch) (collection.FetchedPage, error) {
	if ctx == nil {
		return collection.FetchedPage{}, fmt.Errorf("nil fetch context")
	}
	if request.Platform != collection.PlatformSteam {
		return collection.FetchedPage{}, fmt.Errorf("unsupported platform")
	}
	if request.TaskType != collection.TaskTypeSummary {
		return collection.FetchedPage{}, fmt.Errorf("steam fetcher only implements summary")
	}
	if request.AppID < 1 {
		return collection.FetchedPage{}, fmt.Errorf("appid must be positive")
	}
	if request.AdmitRequest == nil {
		return collection.FetchedPage{}, fmt.Errorf("request admitter is required")
	}
	if request.RequestStarted == nil {
		return collection.FetchedPage{}, fmt.Errorf("request start recorder is required")
	}
	cookie, proxy, err := f.opener.Open(ctx, request.Lease)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	client, err := f.transport(proxy)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	if client == nil {
		return collection.FetchedPage{}, fmt.Errorf("steam HTTP client is missing")
	}
	defer client.CloseIdleConnections()
	steamClient, err := NewClient(ClientOptions{BaseURL: f.baseURL, HTTPClient: client, Cookie: cookie})
	if err != nil {
		return collection.FetchedPage{}, err
	}
	switch request.Side {
	case market.SideAsk:
		return f.fetchAsk(ctx, steamClient, request)
	case market.SideBid:
		return f.fetchBid(ctx, steamClient, request)
	default:
		return collection.FetchedPage{}, fmt.Errorf("invalid side")
	}
}

func (f *Fetcher) fetchAsk(ctx context.Context, client *Client, request collection.PageFetch) (collection.FetchedPage, error) {
	page, err := decodeAskPayload(request)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	// 顺序由采集目标决定：换了顺序补货游标归零，队列也会被清空
	sort := request.Sort
	if sort.Validate() != nil {
		sort = collection.DefaultSortOrder()
	}
	searchRequest := SearchRequest{
		AppID: request.AppID, Start: page.Start, Count: page.Count,
		SortColumn: string(sort.Column), SortDirection: string(sort.Direction), NoRender: true,
	}
	applySearchPriceRange(&searchRequest, request.PriceRange)
	applySearchSteamFacets(&searchRequest, request.AppID, request.SteamFacets)
	admission, collectedAt, err := f.admitRequest(ctx, request)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	parsed, body, err := client.Search(ctx, searchRequest)
	if err != nil {
		return collection.FetchedPage{}, f.mapClientError(ctx, request.Lease, admission, err)
	}
	for _, item := range parsed.Results {
		if isDollarPrice(item.SellPriceText) || isDollarPrice(item.SalePriceText) {
			if err := f.recordSession(ctx, request.Lease, false); err != nil {
				return collection.FetchedPage{}, fmt.Errorf("record steam session: %w", err)
			}
			return collection.FetchedPage{}, collection.ErrFetchSessionInvalid
		}
	}
	if err := f.recordSession(ctx, request.Lease, true); err != nil {
		return collection.FetchedPage{}, fmt.Errorf("record steam session: %w", err)
	}
	attempts := make([]collection.AttemptWrite, 0, len(parsed.Results))
	for _, item := range parsed.Results {
		attempt, err := searchAttempt(request.AppID, item, collectedAt)
		if err != nil {
			return collection.FetchedPage{}, err
		}
		attempts = append(attempts, attempt)
	}
	fetched := collection.FetchedPage{
		Attempts: attempts, Payload: body, TotalCount: int64(parsed.TotalCount), CollectedAt: collectedAt,
	}
	rememberAdmission(&fetched, request, admission)
	return fetched, nil
}

func (f *Fetcher) fetchBid(ctx context.Context, client *Client, request collection.PageFetch) (collection.FetchedPage, error) {
	batch, err := decodeBidPayload(request)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	products, err := f.catalog.ListSteamProductsAfter(ctx, request.AppID, batch.AfterID, batch.Limit)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	if len(products) == 0 {
		// 目录空了就不要发 HTTP，也不要伪造 CollectedAt。调度器会完成任务且不写行情。
		return collection.FetchedPage{}, nil
	}
	page := collection.FetchedPage{}
	attempts := make([]collection.AttemptWrite, 0, len(products))
	var pageCollectedAt time.Time
	for _, product := range products {
		admission, collectedAt, err := f.admitRequest(ctx, request)
		if err != nil {
			return collection.FetchedPage{}, err
		}
		book, err := client.OrderBook(ctx, product.AppID, product.Name)
		if err != nil {
			return collection.FetchedPage{}, f.mapClientError(ctx, request.Lease, admission, err)
		}
		attempt, err := orderbookAttempt(product, market.SideBid, book, collectedAt)
		if err != nil {
			if errors.Is(err, errForeignCurrency) {
				if recordErr := f.recordSession(ctx, request.Lease, false); recordErr != nil {
					return collection.FetchedPage{}, fmt.Errorf("record steam session: %w", recordErr)
				}
				return collection.FetchedPage{}, collection.ErrFetchSessionInvalid
			}
			return collection.FetchedPage{}, err
		}
		attempts = append(attempts, attempt)
		rememberAdmission(&page, request, admission)
		if pageCollectedAt.IsZero() {
			pageCollectedAt = collectedAt
		}
	}
	if err := f.recordSession(ctx, request.Lease, true); err != nil {
		return collection.FetchedPage{}, fmt.Errorf("record steam session: %w", err)
	}
	page.Attempts = attempts
	page.CollectedAt = pageCollectedAt
	return page, nil
}

func searchAttempt(appID int64, item SearchResult, collectedAt time.Time) (collection.AttemptWrite, error) {
	if item.AssetDescription.AppID != 0 && item.AssetDescription.AppID != appID {
		return collection.AttemptWrite{}, fmt.Errorf("search result appid mismatch")
	}
	hash, err := hashNameOf(item)
	if err != nil {
		return collection.AttemptWrite{}, err
	}
	media := searchMedia(appID, hash, item)
	price, err := parseYuanAsk(item)
	if err != nil {
		obs := market.Observation{Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: collectedAt}
		return collection.AttemptWrite{
			PlatformItemID: hash, ExactName: hash, Observation: obs,
			ReasonCode: "price_unverified", Media: media,
		}, nil
	}
	listings := item.SellListings
	obs, err := market.NewPresentObservation(market.PresentInput{
		Currency:    market.CurrencyCNY,
		Side:        market.SideAsk,
		PriceCents:  &price,
		OrderCount:  &listings,
		CollectedAt: collectedAt,
	})
	if err != nil {
		return collection.AttemptWrite{}, err
	}
	return collection.AttemptWrite{PlatformItemID: hash, ExactName: hash, Observation: obs, Media: media}, nil
}

// searchMedia 提取展示用元数据。这些字段不参与行情判定，取不到就留空。
func searchMedia(appID int64, name string, item SearchResult) catalog.ProductMedia {
	media := catalog.ProductMedia{
		IconPath:  item.AssetDescription.IconURL,
		ItemType:  item.AssetDescription.Type,
		NameColor: item.AssetDescription.NameColor,
	}
	// Rust 的 type 是「创意工坊物品」，用商品名对上 Steam item class 才有筛选价值
	if appID == catalog.AppIDRust && (media.ItemType == "" || catalog.IsRustWorkshopType(media.ItemType)) {
		if class, ok := catalog.MatchRustItemClass(name); ok {
			media.ItemType = class.Label
		}
	}
	return media.Normalized()
}

// applySearchSteamFacets 把配置页的 Rust 分类多选原样交给 search/render。
// 参数名来自 Steam 市场 URL：category_steamcat / category_itemclass。
func applySearchSteamFacets(request *SearchRequest, appID int64, facets collection.SteamFacets) {
	if appID != catalog.AppIDRust {
		return
	}
	if request.Filters == nil {
		request.Filters = make(map[string][]string)
	}
	for _, slug := range facets.Normalized().Cats {
		request.Filters["category_steamcat"] = append(request.Filters["category_steamcat"], slug)
	}
	for _, slug := range facets.Normalized().Classes {
		request.Filters["category_itemclass"] = append(request.Filters["category_itemclass"], slug)
	}
}

func orderbookAttempt(product catalog.SteamProduct, side market.Side, parsed OrderBookResponse, collectedAt time.Time) (collection.AttemptWrite, error) {
	write := collection.AttemptWrite{ProductID: product.ProductID, PlatformItemID: product.Name, ExactName: product.Name}
	if !parsed.Success || parsed.Data == nil {
		write.Observation = market.Observation{Side: side, Status: market.StatusUnavailable, CollectedAt: collectedAt}
		write.ReasonCode = "item_missing"
		return write, nil
	}
	cents, empty, err := orderbookBest(side, *parsed.Data)
	if err != nil {
		if errors.Is(err, errForeignCurrency) {
			return collection.AttemptWrite{}, err
		}
		write.Observation = market.Observation{Side: side, Status: market.StatusFailed, CollectedAt: collectedAt}
		write.ReasonCode = "currency_rejected"
		return write, nil
	}
	if empty {
		write.Observation = market.Observation{Side: side, Status: market.StatusEmpty, CollectedAt: collectedAt}
		return write, nil
	}
	var count *int64
	if side == market.SideBid {
		value := parsed.Data.BuyOrders
		count = &value
	} else {
		value := parsed.Data.SellOrders
		count = &value
	}
	obs, err := market.NewPresentObservation(market.PresentInput{
		Currency:    market.CurrencyCNY,
		Side:        side,
		PriceCents:  &cents,
		OrderCount:  count,
		CollectedAt: collectedAt,
	})
	if err != nil {
		return collection.AttemptWrite{}, err
	}
	write.Observation = obs
	return write, nil
}

// applySearchPriceRange 把配置页的人民币分区间原样交给 search/render。
// Steam 市场搜索用 price_min / price_max / price_currency=23，不是事后过滤。
func applySearchPriceRange(request *SearchRequest, bounds collection.PriceRange) {
	if bounds.MinCents == nil && bounds.MaxCents == nil {
		return
	}
	request.PriceCurrency = evidencedCNYCurrency
	request.PriceMin = bounds.MinCents
	request.PriceMax = bounds.MaxCents
}

func (f *Fetcher) admitRequest(
	ctx context.Context,
	request collection.PageFetch,
) (ratelimit.Admission, time.Time, error) {
	admission, err := request.AdmitRequest(ctx)
	if err != nil {
		return ratelimit.Admission{}, time.Time{}, err
	}
	collectedAt := admission.AdmittedAt()
	if collectedAt.IsZero() {
		collectedAt = f.now().UTC().Truncate(time.Microsecond)
	}
	request.RequestStarted()
	return admission, collectedAt, nil
}

func (f *Fetcher) mapClientError(
	ctx context.Context,
	lease resource.Lease,
	admission ratelimit.Admission,
	err error,
) error {
	var responseErr *ResponseError
	if errors.As(err, &responseErr) {
		if responseErr.StatusCode == http.StatusTooManyRequests {
			return &collection.RateLimitSignal{
				Admission: admission,
				Scopes:    []ratelimit.Scope{ratelimit.ScopeAccountIP},
				Reason:    ratelimit.ReasonHTTP429,
			}
		}
		if responseErr.LoginPage || (responseErr.StatusCode >= 300 && responseErr.StatusCode < 400 && looksLikeLogin(responseErr.Location)) {
			if recordErr := f.recordSession(ctx, lease, false); recordErr != nil {
				return fmt.Errorf("record steam session: %w", recordErr)
			}
			return collection.ErrFetchSessionInvalid
		}
		if responseErr.StatusCode >= 500 || responseErr.HTML {
			return fmt.Errorf("%w: steam http %d", collection.ErrFetchNetwork, responseErr.StatusCode)
		}
		return err
	}
	var networkErr *NetworkError
	if errors.As(err, &networkErr) {
		return fmt.Errorf("%w: %v", collection.ErrFetchNetwork, networkErr.Err)
	}
	return err
}

func rememberAdmission(page *collection.FetchedPage, request collection.PageFetch, admission ratelimit.Admission) {
	if page == nil || len(admission.Rules()) == 0 {
		return
	}
	page.Admissions = append(page.Admissions, admission)
	if request.ConfirmRequest != nil {
		request.ConfirmRequest(admission)
	}
}

func (f *Fetcher) recordSession(ctx context.Context, lease resource.Lease, valid bool) error {
	if f.sessions == nil {
		return nil
	}
	return f.sessions.Record(ctx, lease, valid)
}

func looksLikeLogin(location string) bool {
	lower := strings.ToLower(location)
	return strings.Contains(lower, "login")
}
