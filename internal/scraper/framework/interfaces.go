package framework

import (
	"context"
	"net/http"
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

// IScraper 抓取器接口
type IScraper interface {
	// Initialize 初始化抓取器
	Initialize(config *ScraperConfig) error

	// Start 开始抓取
	Start(ctx context.Context) error

	// Stop 停止抓取
	Stop() error

	// GetStatus 获取抓取状态
	GetStatus() ScraperStatus

	// ProcessTask 处理单个抓取任务
	ProcessTask(task *ScrapingTask) (*ScrapingResult, error)

	// GetName 获取抓取器名称
	GetName() string
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

// IHTTPClientManager HTTP客户端管理器接口
type IHTTPClientManager interface {
	// GetClient 获取HTTP客户端
	GetClient(config *ClientConfig) (*http.Client, error)

	// DoRequest 执行HTTP请求
	DoRequest(req *http.Request, options ...RequestOption) (*http.Response, error)

	// ReleaseClient 释放客户端资源
	ReleaseClient(client *http.Client)

	// GetStats 获取客户端统计信息
	GetStats() *ClientStats
}

// ClientStats 客户端统计信息
type ClientStats struct {
	ActiveClients   int           `json:"active_clients"`
	TotalRequests   int64         `json:"total_requests"`
	SuccessRequests int64         `json:"success_requests"`
	FailedRequests  int64         `json:"failed_requests"`
	AverageLatency  time.Duration `json:"average_latency"`
}

// ProxyInfo 代理信息
type ProxyInfo struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Type      string    `json:"type"` // http, https, socks5
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	Country   string    `json:"country"`
	Speed     int       `json:"speed"`
	LastUsed  time.Time `json:"last_used"`
	FailCount int       `json:"fail_count"`
	IsActive  bool      `json:"is_active"`
}

// ProxyPoolStatus 代理池状态
type ProxyPoolStatus struct {
	TotalProxies     int `json:"total_proxies"`
	ActiveProxies    int `json:"active_proxies"`
	FailedProxies    int `json:"failed_proxies"`
	AvailableProxies int `json:"available_proxies"`
}

// IProxyManager 代理管理器接口
type IProxyManager interface {
	// GetProxy 获取可用代理
	GetProxy() (*ProxyInfo, error)

	// ReleaseProxy 释放代理
	ReleaseProxy(proxy *ProxyInfo)

	// MarkProxyFailed 标记代理失效
	MarkProxyFailed(proxy *ProxyInfo, reason string)

	// GetPoolStatus 获取代理池状态
	GetPoolStatus() *ProxyPoolStatus

	// RefreshProxies 刷新代理池
	RefreshProxies() error
}

// ConfigItem 配置项
type ConfigItem struct {
	Key         string      `json:"key"`
	Value       interface{} `json:"value"`
	Type        string      `json:"type"`
	Description string      `json:"description"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// IConfigManager 配置管理器接口
type IConfigManager interface {
	// Get 获取配置值
	Get(key string) (interface{}, error)

	// GetString 获取字符串配置
	GetString(key string) (string, error)

	// GetInt 获取整数配置
	GetInt(key string) (int, error)

	// GetBool 获取布尔配置
	GetBool(key string) (bool, error)

	// GetFloat64 获取浮点数配置
	GetFloat64(key string) (float64, error)

	// GetDuration 获取时间间隔配置
	GetDuration(key string) (time.Duration, error)

	// Set 设置配置值
	Set(key string, value interface{}) error

	// Watch 监听配置变化
	Watch(key string, callback func(oldValue, newValue interface{})) error

	// Reload 重新加载配置
	Reload() error
}

// CacheItem 缓存项
type CacheItem struct {
	Key       string        `json:"key"`
	Value     interface{}   `json:"value"`
	TTL       time.Duration `json:"ttl"`
	CreatedAt time.Time     `json:"created_at"`
	ExpiresAt time.Time     `json:"expires_at"`
}

// ICacheManager 缓存管理器接口
type ICacheManager interface {
	// Get 获取缓存值
	Get(key string) (interface{}, error)

	// Set 设置缓存值
	Set(key string, value interface{}, ttl time.Duration) error

	// Delete 删除缓存
	Delete(key string) error

	// Exists 检查缓存是否存在
	Exists(key string) bool

	// Clear 清空缓存
	Clear() error

	// GetStats 获取缓存统计信息
	GetStats() *CacheStats
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

// IErrorHandler 错误处理器接口
type IErrorHandler interface {
	// HandleError 处理错误
	HandleError(err error, context map[string]interface{}) *ScrapingError

	// IsRetryable 判断错误是否可重试
	IsRetryable(err error) bool

	// GetRetryDelay 获取重试延迟
	GetRetryDelay(retryCount int) time.Duration

	// LogError 记录错误
	LogError(err *ScrapingError)
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

// ITaskManager 任务管理器接口
type ITaskManager interface {
	// AddTask 添加任务
	AddTask(task *ScrapingTask) error

	// GetTask 获取任务
	GetTask() (*ScrapingTask, error)

	// CompleteTask 完成任务
	CompleteTask(taskID string, result *ScrapingResult) error

	// FailTask 任务失败
	FailTask(taskID string, err error) error

	// GetTaskStatus 获取任务状态
	GetTaskStatus(taskID string) (TaskStatus, error)

	// GetQueueSize 获取队列大小
	GetQueueSize() int

	// Clear 清空任务队列
	Clear() error
}

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
