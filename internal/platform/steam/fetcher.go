package steam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/ratelimit"
	"buff-go/internal/resource"
)

const (
	defaultPageSize = 10
	defaultBidBatch = collection.BidBatchSize
	maxBodyBytes    = 1 << 20
	searchPath      = "/market/search/render/"
	orderbookPath   = "/market/orderbook"
)

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
		pageSize = defaultPageSize
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
		baseURL = "https://steamcommunity.com"
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
	switch request.Side {
	case market.SideAsk:
		return f.fetchAsk(ctx, client, cookie, request)
	case market.SideBid:
		return f.fetchBid(ctx, client, cookie, request)
	default:
		return collection.FetchedPage{}, fmt.Errorf("invalid side")
	}
}

func (f *Fetcher) fetchAsk(ctx context.Context, client *http.Client, cookie string, request collection.PageFetch) (collection.FetchedPage, error) {
	page, err := decodeAskPayload(request)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	query := url.Values{}
	query.Set("query", "")
	query.Set("start", strconv.Itoa(page.Start))
	query.Set("count", strconv.Itoa(page.Count))
	query.Set("search_descriptions", "0")
	// 顺序由采集目标决定：换了顺序补货游标归零，队列也会被清空
	sort := request.Sort
	if sort.Validate() != nil {
		sort = collection.DefaultSortOrder()
	}
	query.Set("sort_column", string(sort.Column))
	query.Set("sort_dir", string(sort.Direction))
	query.Set("appid", strconv.FormatInt(request.AppID, 10))
	query.Set("norender", "1")
	applySearchPriceRange(query, request.PriceRange)
	applySearchSteamFacets(query, request.AppID, request.SteamFacets)
	var collectedAt time.Time
	admit := func(ctx context.Context) (ratelimit.Admission, error) {
		admission, err := request.AdmitRequest(ctx)
		if err == nil {
			collectedAt = admission.AdmittedAt()
			if collectedAt.IsZero() {
				collectedAt = f.now().UTC().Truncate(time.Microsecond)
			}
		}
		return admission, err
	}
	body, err := f.get(
		ctx, client, cookie, request.Lease, admit, request.RequestStarted,
		searchPath+"?"+query.Encode(), false,
	)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	parsed, err := parseSearchRender(body)
	if err != nil {
		return collection.FetchedPage{}, err
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
	return collection.FetchedPage{
		Attempts: attempts, Payload: body, TotalCount: int64(parsed.TotalCount), CollectedAt: collectedAt,
	}, nil
}

func (f *Fetcher) fetchBid(ctx context.Context, client *http.Client, cookie string, request collection.PageFetch) (collection.FetchedPage, error) {
	batch, err := decodeBidPayload(request)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	products, err := f.catalog.ListSteamProductsAfter(ctx, request.AppID, batch.AfterID, batch.Limit)
	if err != nil {
		return collection.FetchedPage{}, err
	}
	if len(products) == 0 {
		return collection.FetchedPage{}, nil
	}
	attempts := make([]collection.AttemptWrite, 0, len(products))
	var pageCollectedAt time.Time
	for _, product := range products {
		var collectedAt time.Time
		admit := func(ctx context.Context) (ratelimit.Admission, error) {
			admission, err := request.AdmitRequest(ctx)
			if err == nil {
				collectedAt = admission.AdmittedAt()
				if collectedAt.IsZero() {
					collectedAt = f.now().UTC().Truncate(time.Microsecond)
				}
			}
			return admission, err
		}
		body, err := f.get(
			ctx, client, cookie, request.Lease, admit, request.RequestStarted,
			orderbookQuery(product.AppID, product.Name), false,
		)
		if err != nil {
			return collection.FetchedPage{}, err
		}
		attempt, err := orderbookAttempt(product, market.SideBid, body, collectedAt)
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
		if pageCollectedAt.IsZero() {
			pageCollectedAt = collectedAt
		}
	}
	if err := f.recordSession(ctx, request.Lease, true); err != nil {
		return collection.FetchedPage{}, fmt.Errorf("record steam session: %w", err)
	}
	return collection.FetchedPage{Attempts: attempts, CollectedAt: pageCollectedAt}, nil
}

func searchAttempt(appID int64, item searchResult, collectedAt time.Time) (collection.AttemptWrite, error) {
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
func searchMedia(appID int64, name string, item searchResult) catalog.ProductMedia {
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
func applySearchSteamFacets(query url.Values, appID int64, facets collection.SteamFacets) {
	if appID != catalog.AppIDRust {
		return
	}
	for _, slug := range facets.Normalized().Cats {
		query.Add("category_steamcat", slug)
	}
	for _, slug := range facets.Normalized().Classes {
		query.Add("category_itemclass", slug)
	}
}

func orderbookAttempt(product catalog.SteamProduct, side market.Side, body []byte, collectedAt time.Time) (collection.AttemptWrite, error) {
	parsed, err := parseOrderbook(body)
	if err != nil {
		return collection.AttemptWrite{}, err
	}
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
		value := parsed.Data.CBuyOrders
		count = &value
	} else {
		value := parsed.Data.CSellOrders
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
func applySearchPriceRange(query url.Values, bounds collection.PriceRange) {
	if bounds.MinCents == nil && bounds.MaxCents == nil {
		return
	}
	query.Set("price_currency", strconv.Itoa(evidencedCNYCurrency))
	if bounds.MinCents != nil {
		query.Set("price_min", strconv.FormatInt(*bounds.MinCents, 10))
	}
	if bounds.MaxCents != nil {
		query.Set("price_max", strconv.FormatInt(*bounds.MaxCents, 10))
	}
}

func orderbookQuery(appID int64, name string) string {
	qp, _ := json.Marshal([]any{appID, name})
	query := url.Values{}
	query.Set("q", "Load")
	query.Set("qp", string(qp))
	return orderbookPath + "?" + query.Encode()
}

// browserUserAgent 需要跟着主流浏览器版本更新：停留在过旧的版本号本身就是一个
// 可识别特征。这里不带任何自定义标识。
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"

// setBrowserHeaders 让请求头和浏览器里的同源 XHR 一致。
// 不设 Accept-Encoding：Go 的 transport 会自己带 gzip 并透明解压，手动指定会拿到未解压的响应体。
func setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://steamcommunity.com/market/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="139", "Not(A:Brand";v="24", "Google Chrome";v="139"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
}

func (f *Fetcher) get(
	ctx context.Context,
	client *http.Client,
	cookie string,
	lease resource.Lease,
	admit func(context.Context) (ratelimit.Admission, error),
	requestStarted func(),
	pathQuery string,
	recordValid bool,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.baseURL+pathQuery, nil)
	if err != nil {
		return nil, err
	}
	setBrowserHeaders(req)
	req.Header.Set("Cookie", cookie)
	admission, err := admit(ctx)
	if err != nil {
		return nil, err
	}
	requestStarted()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", collection.ErrFetchNetwork, err)
	}
	defer resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode >= 300 && resp.StatusCode < 400 && looksLikeLogin(loc) {
		if err := f.recordSession(ctx, lease, false); err != nil {
			return nil, fmt.Errorf("record steam session: %w", err)
		}
		return nil, collection.ErrFetchSessionInvalid
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &collection.RateLimitSignal{
			Admission: admission,
			Scopes:    []ratelimit.Scope{ratelimit.ScopeAccountIP},
			Reason:    ratelimit.ReasonHTTP429,
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("steam http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", collection.ErrFetchNetwork, err)
	}
	if recordValid {
		if err := f.recordSession(ctx, lease, true); err != nil {
			return nil, fmt.Errorf("record steam session: %w", err)
		}
	}
	return body, nil
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
