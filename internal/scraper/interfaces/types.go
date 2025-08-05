package interfaces

import (
	"time"
)

// ScraperStatus 抓取器状态
type ScraperStatus int

const (
	StatusStopped ScraperStatus = iota
	StatusRunning
	StatusPaused
	StatusError
)

// ScraperConfig 抓取器配置
type ScraperConfig struct {
	Name           string            `json:"name"`
	MaxConcurrency int               `json:"max_concurrency"`
	RequestDelay   time.Duration     `json:"request_delay"`
	RetryCount     int               `json:"retry_count"`
	Timeout        time.Duration     `json:"timeout"`
	UseProxy       bool              `json:"use_proxy"`
	EnableCache    bool              `json:"enable_cache"`
	CacheTTL       time.Duration     `json:"cache_ttl"`
	CustomHeaders  map[string]string `json:"custom_headers"`
	CustomCookies  map[string]string `json:"custom_cookies"`
}

// ScrapingTask 抓取任务
type ScrapingTask struct {
	ID         string                 `json:"id"`
	URL        string                 `json:"url"`
	Method     string                 `json:"method"`
	Headers    map[string]string      `json:"headers"`
	Body       []byte                 `json:"body"`
	Metadata   map[string]interface{} `json:"metadata"`
	Priority   int                    `json:"priority"`
	RetryCount int                    `json:"retry_count"`
	CreatedAt  time.Time              `json:"created_at"`
}

// ScrapingResult 抓取结果
type ScrapingResult struct {
	TaskID      string                 `json:"task_id"`
	StatusCode  int                    `json:"status_code"`
	Headers     map[string][]string    `json:"headers"`
	Body        []byte                 `json:"body"`
	Error       error                  `json:"error"`
	Duration    time.Duration          `json:"duration"`
	ProxyUsed   string                 `json:"proxy_used"`
	Metadata    map[string]interface{} `json:"metadata"`
	CompletedAt time.Time              `json:"completed_at"`
}

// ClientConfig HTTP客户端配置
type ClientConfig struct {
	Timeout         time.Duration     `json:"timeout"`
	MaxIdleConns    int               `json:"max_idle_conns"`
	MaxConnsPerHost int               `json:"max_conns_per_host"`
	ProxyURL        string            `json:"proxy_url"`
	Headers         map[string]string `json:"headers"`
	Cookies         map[string]string `json:"cookies"`
	UserAgent       string            `json:"user_agent"`
	FollowRedirect  bool              `json:"follow_redirect"`
}

// RequestOption 请求选项
type RequestOption func(*RequestOptions)

// RequestOptions 请求选项配置
type RequestOptions struct {
	Timeout    time.Duration
	RetryCount int
	RetryDelay time.Duration
	UseProxy   bool
	CacheKey   string
	CacheTTL   time.Duration
}

// ClientStats 客户端统计信息
type ClientStats struct {
	ActiveClients   int           `json:"active_clients"`
	TotalRequests   int64         `json:"total_requests"`
	SuccessRequests int64         `json:"success_requests"`
	FailedRequests  int64         `json:"failed_requests"`
	AverageLatency  time.Duration `json:"average_latency"`
}

// Platform 平台类型
type Platform string

const (
	PlatformBuff  Platform = "buff"
	PlatformSteam Platform = "steam"
	// 为未来扩展预留
	PlatformC5Game   Platform = "c5game"
	PlatformIGXE     Platform = "igxe"
	PlatformUUYouPin Platform = "uuyoupin"
)

// ProxyRegion 代理地区类型
type ProxyRegion int

const (
	ProxyRegionDomestic ProxyRegion = iota + 1 // 国内代理
	ProxyRegionHongKong                        // 香港代理
	ProxyRegionOverseas                        // 海外代理
)

// PlatformStatus 平台状态
type PlatformStatus struct {
	IsActive    bool      `json:"is_active"`    // 在该平台是否可用
	FailCount   int       `json:"fail_count"`   // 在该平台的失败次数
	LastFailed  time.Time `json:"last_failed"`  // 最后失败时间
	BannedUntil time.Time `json:"banned_until"` // 封禁到期时间
}

// ProxyInfo 代理信息
type ProxyInfo struct {
	ID                 string                       `json:"id"`
	URL                string                       `json:"url"`
	Type               string                       `json:"type"` // http, https, socks5
	Username           string                       `json:"username"`
	Password           string                       `json:"password"`
	Country            string                       `json:"country"`
	Speed              int                          `json:"speed"`
	LastUsed           time.Time                    `json:"last_used"`
	FailCount          int                          `json:"fail_count"`
	IsActive           bool                         `json:"is_active"`
	Region             ProxyRegion                  `json:"region"`              // 代理地区
	SupportedPlatforms []Platform                   `json:"supported_platforms"` // 支持的平台列表
	PlatformStatuses   map[Platform]*PlatformStatus `json:"platform_statuses"`   // 各平台状态
}

// ProxyPoolStatus 代理池状态
type ProxyPoolStatus struct {
	TotalProxies     int `json:"total_proxies"`
	ActiveProxies    int `json:"active_proxies"`
	FailedProxies    int `json:"failed_proxies"`
	AvailableProxies int `json:"available_proxies"`
}

// ConfigItem 配置项
type ConfigItem struct {
	Key         string      `json:"key"`
	Value       interface{} `json:"value"`
	Type        string      `json:"type"`
	Description string      `json:"description"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// CacheItem 缓存项
type CacheItem struct {
	Key       string        `json:"key"`
	Value     interface{}   `json:"value"`
	TTL       time.Duration `json:"ttl"`
	CreatedAt time.Time     `json:"created_at"`
	ExpiresAt time.Time     `json:"expires_at"`
}

// CacheStats 缓存统计信息
type CacheStats struct {
	TotalKeys   int64   `json:"total_keys"`
	HitCount    int64   `json:"hit_count"`
	MissCount   int64   `json:"miss_count"`
	HitRate     float64 `json:"hit_rate"`
	MemoryUsage int64   `json:"memory_usage"`
}

// ErrorType 错误类型
type ErrorType int

const (
	ErrorTypeNetwork ErrorType = iota
	ErrorTypeTimeout
	ErrorTypeProxy
	ErrorTypeAuth
	ErrorTypeRateLimit
	ErrorTypeParsing
	ErrorTypeValidation
	ErrorTypeSystem
)

// ScrapingError 抓取错误
type ScrapingError struct {
	Type      ErrorType `json:"type"`
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Details   string    `json:"details"`
	Timestamp time.Time `json:"timestamp"`
	Retryable bool      `json:"retryable"`
}

func (e *ScrapingError) Error() string {
	return e.Message
}

// TaskStatus 任务状态
type TaskStatus int

const (
	TaskStatusPending TaskStatus = iota
	TaskStatusRunning
	TaskStatusCompleted
	TaskStatusFailed
	TaskStatusRetrying
	TaskStatusCancelled
)

// Steam数据结构定义

// SteamBuyItemData Steam买入商品数据
type SteamBuyItemData struct {
	Name           string  `json:"name"`
	HashName       string  `json:"hash_name"`
	MarketHashName string  `json:"market_hash_name"`
	SellPrice      float64 `json:"sell_price"`
	SellPriceText  string  `json:"sell_price_text"`
	SellListings   int     `json:"sell_listings"`
	Appid          int     `json:"appid"`
	IconURL        string  `json:"icon_url"`
}

// SteamSellItemData Steam卖出商品数据
type SteamSellItemData struct {
	Name           string  `json:"name"`
	HashName       string  `json:"hash_name"`
	MarketHashName string  `json:"market_hash_name"`
	SellPrice      float64 `json:"sell_price"`
	SellPriceText  string  `json:"sell_price_text"`
	SellListings   int     `json:"sell_listings"`
	Appid          int     `json:"appid"`
	IconURL        string  `json:"icon_url"`
}

// ProcessingResult 数据处理结果
type ProcessingResult struct {
	Success     bool                   `json:"success"`
	ProcessedAt time.Time              `json:"processed_at"`
	ItemCount   int                    `json:"item_count"`
	Errors      []string               `json:"errors,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// 请求选项函数
func WithTimeout(timeout time.Duration) RequestOption {
	return func(opts *RequestOptions) {
		opts.Timeout = timeout
	}
}

func WithRetry(count int, delay time.Duration) RequestOption {
	return func(opts *RequestOptions) {
		opts.RetryCount = count
		opts.RetryDelay = delay
	}
}

func WithProxy(useProxy bool) RequestOption {
	return func(opts *RequestOptions) {
		opts.UseProxy = useProxy
	}
}

func WithCache(key string, ttl time.Duration) RequestOption {
	return func(opts *RequestOptions) {
		opts.CacheKey = key
		opts.CacheTTL = ttl
	}
}
