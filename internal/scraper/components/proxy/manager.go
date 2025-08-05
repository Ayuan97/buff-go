package proxy

import (
	"buff-go/internal/scraper/interfaces"
	"errors"
	"math/rand"
	"sync"
	"time"
)

// ProxyManager 代理管理器实现
type ProxyManager struct {
	proxies     []*interfaces.ProxyInfo
	proxiesMux  sync.RWMutex
	usedProxies map[string]*interfaces.ProxyInfo
	usedMux     sync.RWMutex
	config      *ProxyManagerConfig
}

// ProxyManagerConfig 代理管理器配置
type ProxyManagerConfig struct {
	MaxFailCount    int           `json:"max_fail_count"`
	HealthCheckURL  string        `json:"health_check_url"`
	CheckInterval   time.Duration `json:"check_interval"`
	RefreshInterval time.Duration `json:"refresh_interval"`
	MaxUsageTime    time.Duration `json:"max_usage_time"`
}

// NewProxyManager 创建代理管理器
func NewProxyManager(config *ProxyManagerConfig) *ProxyManager {
	if config == nil {
		config = &ProxyManagerConfig{
			MaxFailCount:    5,
			CheckInterval:   5 * time.Minute,
			RefreshInterval: 30 * time.Minute,
			MaxUsageTime:    10 * time.Minute,
		}
	}

	pm := &ProxyManager{
		proxies:     make([]*interfaces.ProxyInfo, 0),
		usedProxies: make(map[string]*interfaces.ProxyInfo),
		config:      config,
	}

	// 启动后台任务
	go pm.startBackgroundTasks()

	return pm
}

// GetProxy 获取可用代理
func (pm *ProxyManager) GetProxy() (*interfaces.ProxyInfo, error) {
	pm.proxiesMux.RLock()
	defer pm.proxiesMux.RUnlock()

	if len(pm.proxies) == 0 {
		return nil, errors.New("no proxies available")
	}

	// 过滤可用代理
	availableProxies := make([]*interfaces.ProxyInfo, 0)
	for _, proxy := range pm.proxies {
		if pm.isProxyAvailable(proxy) {
			availableProxies = append(availableProxies, proxy)
		}
	}

	if len(availableProxies) == 0 {
		return nil, errors.New("no available proxies")
	}

	// 随机选择一个代理
	selectedProxy := availableProxies[rand.Intn(len(availableProxies))]

	// 标记为使用中
	pm.usedMux.Lock()
	pm.usedProxies[selectedProxy.ID] = selectedProxy
	selectedProxy.LastUsed = time.Now()
	pm.usedMux.Unlock()

	return selectedProxy, nil
}

// ReleaseProxy 释放代理
func (pm *ProxyManager) ReleaseProxy(proxy *interfaces.ProxyInfo) {
	if proxy == nil {
		return
	}

	pm.usedMux.Lock()
	delete(pm.usedProxies, proxy.ID)
	pm.usedMux.Unlock()
}

// MarkProxyFailed 标记代理失效
func (pm *ProxyManager) MarkProxyFailed(proxy *interfaces.ProxyInfo, reason string) {
	if proxy == nil {
		return
	}

	pm.proxiesMux.Lock()
	defer pm.proxiesMux.Unlock()

	for _, p := range pm.proxies {
		if p.ID == proxy.ID {
			p.FailCount++
			if p.FailCount >= pm.config.MaxFailCount {
				p.IsActive = false
			}
			break
		}
	}

	// 从使用中列表移除
	pm.ReleaseProxy(proxy)
}

// GetPoolStatus 获取代理池状态
func (pm *ProxyManager) GetPoolStatus() *interfaces.ProxyPoolStatus {
	pm.proxiesMux.RLock()
	defer pm.proxiesMux.RUnlock()

	status := &interfaces.ProxyPoolStatus{
		TotalProxies: len(pm.proxies),
	}

	for _, proxy := range pm.proxies {
		if proxy.IsActive {
			status.ActiveProxies++
			if pm.isProxyAvailable(proxy) {
				status.AvailableProxies++
			}
		} else {
			status.FailedProxies++
		}
	}

	return status
}

// RefreshProxies 刷新代理池
func (pm *ProxyManager) RefreshProxies() error {
	// 这里可以实现从外部源获取代理的逻辑
	// 目前只是一个占位实现
	return nil
}

// isProxyAvailable 检查代理是否可用
func (pm *ProxyManager) isProxyAvailable(proxy *interfaces.ProxyInfo) bool {
	if !proxy.IsActive {
		return false
	}

	if proxy.FailCount >= pm.config.MaxFailCount {
		return false
	}

	// 检查是否正在使用中
	pm.usedMux.RLock()
	_, inUse := pm.usedProxies[proxy.ID]
	pm.usedMux.RUnlock()

	if inUse {
		// 检查使用时间是否超过限制
		if time.Since(proxy.LastUsed) > pm.config.MaxUsageTime {
			pm.ReleaseProxy(proxy)
			return true
		}
		return false
	}

	return true
}

// startBackgroundTasks 启动后台任务
func (pm *ProxyManager) startBackgroundTasks() {
	// 健康检查任务
	go func() {
		ticker := time.NewTicker(pm.config.CheckInterval)
		defer ticker.Stop()

		for range ticker.C {
			pm.healthCheck()
		}
	}()

	// 代理刷新任务
	go func() {
		ticker := time.NewTicker(pm.config.RefreshInterval)
		defer ticker.Stop()

		for range ticker.C {
			pm.RefreshProxies()
		}
	}()

	// 清理过期使用记录
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			pm.cleanupExpiredUsage()
		}
	}()
}

// healthCheck 健康检查
func (pm *ProxyManager) healthCheck() {
	// 这里可以实现代理健康检查的逻辑
	// 目前只是一个占位实现
}

// cleanupExpiredUsage 清理过期的使用记录
func (pm *ProxyManager) cleanupExpiredUsage() {
	pm.usedMux.Lock()
	defer pm.usedMux.Unlock()

	for id, proxy := range pm.usedProxies {
		if time.Since(proxy.LastUsed) > pm.config.MaxUsageTime {
			delete(pm.usedProxies, id)
		}
	}
}

// AddProxy 添加代理
func (pm *ProxyManager) AddProxy(proxy *interfaces.ProxyInfo) {
	pm.proxiesMux.Lock()
	defer pm.proxiesMux.Unlock()

	pm.proxies = append(pm.proxies, proxy)
}

// RemoveProxy 移除代理
func (pm *ProxyManager) RemoveProxy(proxyID string) {
	pm.proxiesMux.Lock()
	defer pm.proxiesMux.Unlock()

	for i, proxy := range pm.proxies {
		if proxy.ID == proxyID {
			pm.proxies = append(pm.proxies[:i], pm.proxies[i+1:]...)
			break
		}
	}

	// 从使用中列表移除
	pm.usedMux.Lock()
	delete(pm.usedProxies, proxyID)
	pm.usedMux.Unlock()
}
