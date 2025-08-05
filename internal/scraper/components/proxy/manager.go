package proxy

import (
	"buff-go/internal/scraper/core"
	"errors"
	"math/rand"
	"sync"
	"time"
)

// ProxyManager 代理管理器实现
type ProxyManager struct {
	proxies     []*core.ProxyInfo
	proxiesMux  sync.RWMutex
	usedProxies map[string]*core.ProxyInfo
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
		proxies:     make([]*core.ProxyInfo, 0),
		usedProxies: make(map[string]*core.ProxyInfo),
		config:      config,
	}

	// 启动后台任务
	go pm.startBackgroundTasks()

	return pm
}

// GetProxy 获取可用代理
func (pm *ProxyManager) GetProxy() (*core.ProxyInfo, error) {
	pm.proxiesMux.RLock()
	defer pm.proxiesMux.RUnlock()

	if len(pm.proxies) == 0 {
		return nil, errors.New("no proxies available")
	}

	// 过滤可用代理
	availableProxies := make([]*core.ProxyInfo, 0)
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
func (pm *ProxyManager) ReleaseProxy(proxy *core.ProxyInfo) {
	if proxy == nil {
		return
	}

	pm.usedMux.Lock()
	delete(pm.usedProxies, proxy.ID)
	pm.usedMux.Unlock()
}

// MarkProxyFailed 标记代理失效
func (pm *ProxyManager) MarkProxyFailed(proxy *core.ProxyInfo, reason string) {
	if proxy == nil {
		return
	}

	pm.proxiesMux.Lock()
	defer pm.proxiesMux.Unlock()

	// 查找代理并增加失败计数
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
	pm.usedMux.Lock()
	delete(pm.usedProxies, proxy.ID)
	pm.usedMux.Unlock()
}

// GetPoolStatus 获取代理池状态
func (pm *ProxyManager) GetPoolStatus() *core.ProxyPoolStatus {
	pm.proxiesMux.RLock()
	defer pm.proxiesMux.RUnlock()

	status := &core.ProxyPoolStatus{
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
	// 这里应该从外部数据源获取代理列表
	// 为了演示，我们创建一些示例代理
	newProxies := pm.fetchProxiesFromSource()

	pm.proxiesMux.Lock()
	pm.proxies = newProxies
	pm.proxiesMux.Unlock()

	return nil
}

// AddProxy 添加代理
func (pm *ProxyManager) AddProxy(proxy *core.ProxyInfo) {
	if proxy == nil {
		return
	}

	pm.proxiesMux.Lock()
	defer pm.proxiesMux.Unlock()

	// 检查是否已存在
	for _, p := range pm.proxies {
		if p.ID == proxy.ID || p.URL == proxy.URL {
			return
		}
	}

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

// isProxyAvailable 检查代理是否可用
func (pm *ProxyManager) isProxyAvailable(proxy *core.ProxyInfo) bool {
	if !proxy.IsActive {
		return false
	}

	// 检查是否正在使用中
	pm.usedMux.RLock()
	_, inUse := pm.usedProxies[proxy.ID]
	pm.usedMux.RUnlock()

	if inUse {
		// 检查使用时间是否超限
		if time.Since(proxy.LastUsed) > pm.config.MaxUsageTime {
			pm.usedMux.Lock()
			delete(pm.usedProxies, proxy.ID)
			pm.usedMux.Unlock()
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
}

// healthCheck 健康检查
func (pm *ProxyManager) healthCheck() {
	pm.proxiesMux.RLock()
	proxies := make([]*core.ProxyInfo, len(pm.proxies))
	copy(proxies, pm.proxies)
	pm.proxiesMux.RUnlock()

	for _, proxy := range proxies {
		if !proxy.IsActive {
			continue
		}

		// 这里应该实际测试代理的可用性
		// 为了演示，我们简单地重置失败计数
		if proxy.FailCount > 0 && time.Since(proxy.LastUsed) > 10*time.Minute {
			pm.proxiesMux.Lock()
			proxy.FailCount = 0
			proxy.IsActive = true
			pm.proxiesMux.Unlock()
		}
	}
}

// fetchProxiesFromSource 从数据源获取代理列表
func (pm *ProxyManager) fetchProxiesFromSource() []*core.ProxyInfo {
	// 集成现有的代理获取逻辑
	// 这里可以从数据库、外部API或其他数据源获取代理

	// 示例：从数据库获取代理（需要注入DAO）
	// 实际实现时应该通过依赖注入获取DAO实例
	proxies := make([]*core.ProxyInfo, 0)

	// 添加一些默认代理作为后备
	defaultProxies := []*core.ProxyInfo{
		{
			ID:       "default_proxy_1",
			URL:      "http://127.0.0.1:8080",
			Type:     "http",
			Country:  "Local",
			Speed:    100,
			IsActive: true,
		},
	}

	proxies = append(proxies, defaultProxies...)
	return proxies
}

// GetProxyByID 根据ID获取代理
func (pm *ProxyManager) GetProxyByID(id string) *core.ProxyInfo {
	pm.proxiesMux.RLock()
	defer pm.proxiesMux.RUnlock()

	for _, proxy := range pm.proxies {
		if proxy.ID == id {
			return proxy
		}
	}

	return nil
}

// SetDAO 设置DAO实例用于从数据库获取代理
func (pm *ProxyManager) SetDAO(dao interface{}) {
	// 这里可以保存DAO实例，用于从数据库获取代理
	// 实际实现时需要定义具体的接口
}

// LoadProxiesFromDB 从数据库加载代理
func (pm *ProxyManager) LoadProxiesFromDB() error {
	// 这里集成现有的数据库代理获取逻辑
	// 例如：调用 myDao.GetBuffIps() 或类似方法

	// 示例实现（实际需要根据现有DAO接口调整）
	/*
		ips, err := dao.GetBuffIps()
		if err != nil {
			return err
		}

		proxies := make([]*ProxyInfo, 0, len(ips))
		for _, ip := range ips {
			proxy := &ProxyInfo{
				ID:       fmt.Sprintf("%s:%d", ip.Ip, ip.Port),
				URL:      fmt.Sprintf("http://%s:%d", ip.Ip, ip.Port),
				Type:     "http",
				IsActive: true,
			}
			if ip.IsHttps == "1" {
				proxy.URL = fmt.Sprintf("https://%s:%d", ip.Ip, ip.Port)
				proxy.Type = "https"
			}
			proxies = append(proxies, proxy)
		}

		pm.proxiesMux.Lock()
		pm.proxies = proxies
		pm.proxiesMux.Unlock()
	*/

	return nil
}
