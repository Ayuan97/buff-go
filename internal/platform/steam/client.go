package steam

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultBaseURL        = "https://steamcommunity.com"
	maxBodyBytes          = 1 << 20
	searchRenderPath      = "/market/search/render/"
	orderbookPath         = "/market/orderbook"
	priceOverviewPath     = "/market/priceoverview/"
	marketActionsPath     = "/market/actions"
	queryActionHeader     = "x-valve-request-type"
	queryActionHeaderVal  = "queryAction"
	orderBookAction       = "Load"
	priceHistoryAction    = "QueryPriceHistory"
	itemDescriptionAction = "QueryDescription"
	itemListingsAction    = "QueryListingsForItem"
)

// ClientOptions wires one Steam Community HTTP client and its optional session.
type ClientOptions struct {
	BaseURL    string
	HTTPClient *http.Client
	Cookie     string
}

// Client owns Steam Community Market request construction and wire parsing.
// Rate limiting and retries remain the caller's responsibility.
type Client struct {
	baseURL    string
	httpClient *http.Client
	cookie     string
}

// NewClient validates a reusable Steam Community Market client.
func NewClient(opt ClientOptions) (*Client, error) {
	if opt.HTTPClient == nil {
		return nil, fmt.Errorf("steam HTTP client is required")
	}
	baseURL := strings.TrimRight(opt.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("steam base URL is invalid")
	}
	return &Client{baseURL: baseURL, httpClient: opt.HTTPClient, cookie: strings.TrimSpace(opt.Cookie)}, nil
}

// ResponseError reports a non-200 Steam response without retaining its body.
type ResponseError struct {
	StatusCode int
	Location   string
}

func (err *ResponseError) Error() string {
	return fmt.Sprintf("steam http %d", err.StatusCode)
}

// NetworkError separates transport failures from response and payload errors.
type NetworkError struct {
	Err error
}

func (err *NetworkError) Error() string { return fmt.Sprintf("steam request: %v", err.Err) }
func (err *NetworkError) Unwrap() error { return err.Err }

func (c *Client) queryAction(ctx context.Context, action string, params ...any) ([]byte, error) {
	encoded, err := encodeQueryActionParams(params...)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("q", action)
	query.Set("qp", encoded)
	return c.get(ctx, marketActionsPath, query, true)
}

func (c *Client) get(ctx context.Context, path string, query url.Values, queryAction bool) ([]byte, error) {
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("steam request URL: %w", err)
	}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	setBrowserHeaders(req)
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if queryAction {
		req.Header.Set(queryActionHeader, queryActionHeaderVal)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &NetworkError{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &ResponseError{StatusCode: resp.StatusCode, Location: resp.Header.Get("Location")}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, &NetworkError{Err: err}
	}
	return body, nil
}

// browserUserAgent follows the browser profile used by the verified market requests.
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"

func setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", defaultBaseURL+"/market/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="139", "Not(A:Brand";v="24", "Google Chrome";v="139"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
}
