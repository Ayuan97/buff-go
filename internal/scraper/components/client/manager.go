package client

import (
	"buff-go/internal/scraper/interfaces"
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
	stats        *interfaces.ClientStats
	statsMux     sync.RWMutex
	proxyMgr     interfaces.IProxyManager
	errorHandler interfaces.IErrorHandler
}

// NewHTTPClientManager 创建HTTP客户端管理器
func NewHTTPClientManager(proxyMgr interfaces.IProxyManager, errorHandler interfaces.IErrorHandler) *HTTPClientManager {
	return &HTTPClientManager{
		clients:      make(map[string]*http.Client),
		stats:        &interfaces.ClientStats{},
		proxyMgr:     proxyMgr,
		errorHandler: errorHandler,
	}
}

// GetClient 获取HTTP客户端
func (m *HTTPClientManager) GetClient(config *interfaces.ClientConfig) (*http.Client, error) {
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
	m.statsMux.Lock()
	m.stats.ActiveClients++
	m.statsMux.Unlock()
	m.clientsMux.Unlock()

	return client, nil
}

// DoRequest 执行HTTP请求
func (m *HTTPClientManager) DoRequest(req *http.Request, options ...interfaces.RequestOption) (*http.Response, error) {
	// 应用请求选项
	opts := &interfaces.RequestOptions{
		Timeout:    30 * time.Second,
		RetryCount: 3,
		RetryDelay: time.Second,
		UseProxy:   false,
	}

	for _, option := range options {
		option(opts)
	}

	// 获取客户端配置
	config := &interfaces.ClientConfig{
		Timeout:         opts.Timeout,
		MaxIdleConns:    100,
		MaxConnsPerHost: 10,
		FollowRedirect:  true,
	}

	// 如果需要使用代理
	if opts.UseProxy && m.proxyMgr != nil {
		// 从请求上下文中获取平台信息，如果没有则使用默认平台
		platform := interfaces.PlatformBuff // 默认平台
		if req.Header.Get("X-Platform") != "" {
			platform = interfaces.Platform(req.Header.Get("X-Platform"))
		}

		proxy, err := m.proxyMgr.GetProxyForPlatform(platform)
		if err == nil {
			config.ProxyURL = proxy.URL
			// 在请求头中记录使用的代理ID，用于后续错误处理
			req.Header.Set("X-Proxy-ID", proxy.ID)
		}
	}

	client, err := m.GetClient(config)
	if err != nil {
		return nil, err
	}

	// 执行请求（带重试）
	var resp *http.Response
	var lastErr error
	var currentProxy *interfaces.ProxyInfo

	// 获取当前使用的代理信息
	proxyID := req.Header.Get("X-Proxy-ID")
	platform := interfaces.Platform(req.Header.Get("X-Platform"))
	if platform == "" {
		platform = interfaces.PlatformBuff
	}

	// 如果使用代理，先获取代理信息
	if opts.UseProxy && m.proxyMgr != nil && config.ProxyURL != "" {
		// 通过代理URL找到对应的代理对象（这里需要改进，应该通过ID查找）
		// 暂时创建一个临时代理对象
		currentProxy = &interfaces.ProxyInfo{
			ID:  proxyID,
			URL: config.ProxyURL,
		}
	}

	for i := 0; i <= opts.RetryCount; i++ {
		startTime := time.Now()
		resp, lastErr = client.Do(req)

		m.statsMux.Lock()
		m.stats.TotalRequests++
		if lastErr == nil && resp.StatusCode < 400 {
			m.stats.SuccessRequests++
		} else {
			m.stats.FailedRequests++
		}

		// 更新平均延迟
		duration := time.Since(startTime)
		if m.stats.TotalRequests == 1 {
			m.stats.AverageLatency = duration
		} else {
			m.stats.AverageLatency = (m.stats.AverageLatency + duration) / 2
		}
		m.statsMux.Unlock()

		if lastErr == nil {
			// 请求成功，释放代理
			if opts.UseProxy && m.proxyMgr != nil && proxyID != "" {
				// 通过代理ID找到代理对象并释放
				if currentProxy != nil {
					m.proxyMgr.ReleaseProxy(currentProxy)
				}
			}
			return resp, nil
		}

		// 请求失败，标记代理失败
		if opts.UseProxy && m.proxyMgr != nil && proxyID != "" {
			if currentProxy != nil {
				m.proxyMgr.MarkProxyFailedForPlatform(currentProxy, platform, lastErr.Error())
			}
		}

		// 如果不是最后一次重试，等待后重试
		if i < opts.RetryCount {
			time.Sleep(opts.RetryDelay)

			// 重试时获取新的代理
			if opts.UseProxy && m.proxyMgr != nil {
				newProxy, err := m.proxyMgr.GetProxyForPlatform(platform)
				if err == nil {
					// 更新客户端配置使用新代理
					config.ProxyURL = newProxy.URL
					client, err = m.GetClient(config)
					if err != nil {
						continue
					}
					currentProxy = newProxy
					req.Header.Set("X-Proxy-ID", newProxy.ID)
				}
			}
		}
	}

	// 所有重试都失败，释放代理
	if opts.UseProxy && m.proxyMgr != nil && currentProxy != nil {
		m.proxyMgr.ReleaseProxy(currentProxy)
	}

	return nil, lastErr
}

// ReleaseClient 释放客户端资源
func (m *HTTPClientManager) ReleaseClient(client *http.Client) {
	// 在实际实现中，这里可以进行连接池的清理等操作
	// 目前简单实现，不做特殊处理
}

// GetStats 获取客户端统计信息
func (m *HTTPClientManager) GetStats() *interfaces.ClientStats {
	m.statsMux.RLock()
	defer m.statsMux.RUnlock()

	stats := *m.stats
	return &stats
}

// createClient 创建HTTP客户端
func (m *HTTPClientManager) createClient(config *interfaces.ClientConfig) (*http.Client, error) {
	transport := &http.Transport{
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
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

	client := &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
		Jar:       jar,
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
func (m *HTTPClientManager) generateClientKey(config *interfaces.ClientConfig) string {
	return fmt.Sprintf("%s_%d_%d_%v_%s",
		config.ProxyURL,
		config.MaxIdleConns,
		config.MaxConnsPerHost,
		config.FollowRedirect,
		config.UserAgent,
	)
}
