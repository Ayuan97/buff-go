package http

import (
	"buff-go/internal/scraper/core"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"
)

// HTTPClientManager HTTP客户端管理器实现
type HTTPClientManager struct {
	clients      map[string]*http.Client
	clientsMux   sync.RWMutex
	stats        *ClientStats
	statsMux     sync.RWMutex
	proxyMgr     IProxyManager
	errorHandler IErrorHandler
}

// NewHTTPClientManager 创建HTTP客户端管理器
func NewHTTPClientManager(proxyMgr IProxyManager, errorHandler IErrorHandler) *HTTPClientManager {
	return &HTTPClientManager{
		clients:      make(map[string]*http.Client),
		stats:        &ClientStats{},
		proxyMgr:     proxyMgr,
		errorHandler: errorHandler,
	}
}

// GetClient 获取HTTP客户端
func (m *HTTPClientManager) GetClient(config *ClientConfig) (*http.Client, error) {
	key := m.generateClientKey(config)

	m.clientsMux.RLock()
	if client, exists := m.clients[key]; exists {
		m.clientsMux.RUnlock()
		return client, nil
	}
	m.clientsMux.RUnlock()

	// 创建新客户端
	client, err := m.createClient(config)
	if err != nil {
		return nil, err
	}

	m.clientsMux.Lock()
	m.clients[key] = client
	m.stats.ActiveClients++
	m.clientsMux.Unlock()

	return client, nil
}

// DoRequest 执行HTTP请求
func (m *HTTPClientManager) DoRequest(req *http.Request, options ...RequestOption) (*http.Response, error) {
	opts := &RequestOptions{
		Timeout:    30 * time.Second,
		RetryCount: 3,
		RetryDelay: time.Second,
		UseProxy:   false,
	}

	// 应用选项
	for _, option := range options {
		option(opts)
	}

	// 获取客户端配置
	config := &ClientConfig{
		Timeout:         opts.Timeout,
		MaxIdleConns:    10,
		MaxConnsPerHost: 10,
		FollowRedirect:  true,
	}

	// 如果需要使用代理
	if opts.UseProxy && m.proxyMgr != nil {
		proxy, err := m.proxyMgr.GetProxy()
		if err == nil {
			config.ProxyURL = proxy.URL
			defer m.proxyMgr.ReleaseProxy(proxy)
		}
	}

	client, err := m.GetClient(config)
	if err != nil {
		return nil, err
	}

	// 执行请求（带重试）
	var resp *http.Response
	var lastErr error

	for i := 0; i <= opts.RetryCount; i++ {
		startTime := time.Now()
		resp, lastErr = client.Do(req)
		duration := time.Since(startTime)

		// 更新统计信息
		m.updateStats(lastErr == nil, duration)

		if lastErr == nil {
			return resp, nil
		}

		// 检查是否可重试
		if i < opts.RetryCount && m.errorHandler != nil && m.errorHandler.IsRetryable(lastErr) {
			time.Sleep(opts.RetryDelay)
			continue
		}

		break
	}

	return resp, lastErr
}

// ReleaseClient 释放客户端资源
func (m *HTTPClientManager) ReleaseClient(client *http.Client) {
	// 在实际实现中，可以考虑连接池的管理
	// 这里暂时不做特殊处理，让GC自动回收
}

// GetStats 获取客户端统计信息
func (m *HTTPClientManager) GetStats() *ClientStats {
	m.statsMux.RLock()
	defer m.statsMux.RUnlock()

	// 返回统计信息的副本
	return &ClientStats{
		ActiveClients:   m.stats.ActiveClients,
		TotalRequests:   m.stats.TotalRequests,
		SuccessRequests: m.stats.SuccessRequests,
		FailedRequests:  m.stats.FailedRequests,
		AverageLatency:  m.stats.AverageLatency,
	}
}

// createClient 创建HTTP客户端
func (m *HTTPClientManager) createClient(config *ClientConfig) (*http.Client, error) {
	// 创建传输层
	transport := &http.Transport{
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
	}

	// 设置代理
	if config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %v", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	// 创建Cookie Jar
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %v", err)
	}

	// 设置Cookie
	if len(config.Cookies) > 0 {
		cookies := make([]*http.Cookie, 0, len(config.Cookies))
		for name, value := range config.Cookies {
			cookies = append(cookies, &http.Cookie{
				Name:  name,
				Value: value,
			})
		}
		// 这里需要根据具体的URL设置Cookie，暂时省略
	}

	client := &http.Client{
		Transport: transport,
		Jar:       jar,
		Timeout:   config.Timeout,
	}

	// 设置重定向策略
	if !config.FollowRedirect {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	return client, nil
}

// generateClientKey 生成客户端键
func (m *HTTPClientManager) generateClientKey(config *ClientConfig) string {
	return fmt.Sprintf("%s_%d_%d_%s_%t",
		config.ProxyURL,
		config.Timeout,
		config.MaxIdleConns,
		config.UserAgent,
		config.FollowRedirect)
}

// updateStats 更新统计信息
func (m *HTTPClientManager) updateStats(success bool, duration time.Duration) {
	m.statsMux.Lock()
	defer m.statsMux.Unlock()

	m.stats.TotalRequests++
	if success {
		m.stats.SuccessRequests++
	} else {
		m.stats.FailedRequests++
	}

	// 计算平均延迟（简单实现）
	if m.stats.TotalRequests == 1 {
		m.stats.AverageLatency = duration
	} else {
		m.stats.AverageLatency = (m.stats.AverageLatency + duration) / 2
	}
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
