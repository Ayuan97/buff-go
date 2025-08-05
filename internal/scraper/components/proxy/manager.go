package proxy

import (
	"buff-go/internal/scraper/interfaces"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// ProxyManager 代理管理器实现
type ProxyManager struct {
	proxies     []*interfaces.ProxyInfo          // 代理列表
	proxiesMux  sync.RWMutex                     // 代理列表读写锁
	usedProxies map[string]*interfaces.ProxyInfo // 使用中的代理列表
	usedMux     sync.RWMutex                     // 使用中的代理列表读写锁
	config      *ProxyManagerConfig              // 代理管理器配置
}

// ProxyManagerConfig 代理管理器配置
type ProxyManagerConfig struct {
	MaxFailCount        int                                   `json:"max_fail_count"`        // 最大失败次数
	HealthCheckURL      string                                `json:"health_check_url"`      // 健康检查URL
	CheckInterval       time.Duration                         `json:"check_interval"`        // 检查间隔
	RefreshInterval     time.Duration                         `json:"refresh_interval"`      // 刷新间隔
	MaxUsageTime        time.Duration                         `json:"max_usage_time"`        // 最大使用时间
	PlatformBanDuration map[interfaces.Platform]time.Duration `json:"platform_ban_duration"` // 各平台封禁时长
	PlatformMaxFails    map[interfaces.Platform]int           `json:"platform_max_fails"`    // 各平台最大失败次数
}

// NewProxyManager 创建代理管理器
func NewProxyManager(config *ProxyManagerConfig) *ProxyManager {
	if config == nil {
		config = &ProxyManagerConfig{
			MaxFailCount:    5,
			CheckInterval:   5 * time.Minute,
			RefreshInterval: 30 * time.Minute,
			MaxUsageTime:    10 * time.Minute,
			PlatformBanDuration: map[interfaces.Platform]time.Duration{
				interfaces.PlatformBuff:  30 * time.Minute,
				interfaces.PlatformSteam: 60 * time.Minute,
			},
			PlatformMaxFails: map[interfaces.Platform]int{
				interfaces.PlatformBuff:  3,
				interfaces.PlatformSteam: 5,
			},
		}
	}

	// 确保平台配置存在
	if config.PlatformBanDuration == nil {
		config.PlatformBanDuration = map[interfaces.Platform]time.Duration{
			interfaces.PlatformBuff:  30 * time.Minute,
			interfaces.PlatformSteam: 60 * time.Minute,
		}
	}
	if config.PlatformMaxFails == nil {
		config.PlatformMaxFails = map[interfaces.Platform]int{
			interfaces.PlatformBuff:  3,
			interfaces.PlatformSteam: 5,
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

// GetProxyForPlatform 获取指定平台的可用代理
func (pm *ProxyManager) GetProxyForPlatform(platform interfaces.Platform) (*interfaces.ProxyInfo, error) {
	pm.proxiesMux.RLock()
	defer pm.proxiesMux.RUnlock()

	if len(pm.proxies) == 0 {
		return nil, errors.New("no proxies available")
	}

	// 过滤适用于指定平台的代理
	availableProxies := make([]*interfaces.ProxyInfo, 0)
	for _, proxy := range pm.proxies {
		if pm.isProxyAvailableForPlatform(proxy, platform) {
			availableProxies = append(availableProxies, proxy)
		}
	}

	if len(availableProxies) == 0 {
		return nil, fmt.Errorf("no available proxies for platform %s", platform)
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

// MarkProxyFailedForPlatform 标记代理在指定平台失效
func (pm *ProxyManager) MarkProxyFailedForPlatform(proxy *interfaces.ProxyInfo, platform interfaces.Platform, reason string) {
	if proxy == nil {
		return
	}

	pm.proxiesMux.Lock()
	defer pm.proxiesMux.Unlock()

	for _, p := range pm.proxies {
		if p.ID == proxy.ID {
			// 更新平台状态
			if p.PlatformStatuses == nil {
				p.PlatformStatuses = make(map[interfaces.Platform]*interfaces.PlatformStatus)
			}

			status, exists := p.PlatformStatuses[platform]
			if !exists {
				status = &interfaces.PlatformStatus{IsActive: true}
				p.PlatformStatuses[platform] = status
			}

			status.FailCount++
			status.LastFailed = time.Now()

			// 获取平台配置
			maxFails, exists := pm.config.PlatformMaxFails[platform]
			if !exists {
				maxFails = pm.config.MaxFailCount
			}

			banDuration, exists := pm.config.PlatformBanDuration[platform]
			if !exists {
				banDuration = 30 * time.Minute
			}

			// 如果失败次数超过阈值，临时封禁
			if status.FailCount >= maxFails {
				status.IsActive = false
				status.BannedUntil = time.Now().Add(banDuration)
			}

			break
		}
	}

	// 从使用中列表移除
	pm.ReleaseProxy(proxy)
}

// RecoverProxyForPlatform 恢复代理在指定平台的状态
func (pm *ProxyManager) RecoverProxyForPlatform(proxy *interfaces.ProxyInfo, platform interfaces.Platform) error {
	if proxy == nil {
		return errors.New("proxy is nil")
	}

	pm.proxiesMux.Lock()
	defer pm.proxiesMux.Unlock()

	for _, p := range pm.proxies {
		if p.ID == proxy.ID {
			if p.PlatformStatuses == nil {
				p.PlatformStatuses = make(map[interfaces.Platform]*interfaces.PlatformStatus)
			}

			status, exists := p.PlatformStatuses[platform]
			if !exists {
				status = &interfaces.PlatformStatus{IsActive: true}
				p.PlatformStatuses[platform] = status
			}

			status.IsActive = true
			status.FailCount = 0
			status.BannedUntil = time.Time{}

			return nil
		}
	}

	return fmt.Errorf("proxy with ID %s not found", proxy.ID)
}

// GetPoolStatusForPlatform 获取指定平台的代理池状态
func (pm *ProxyManager) GetPoolStatusForPlatform(platform interfaces.Platform) *interfaces.ProxyPoolStatus {
	pm.proxiesMux.RLock()
	defer pm.proxiesMux.RUnlock()

	status := &interfaces.ProxyPoolStatus{
		TotalProxies: 0,
	}

	for _, proxy := range pm.proxies {
		// 检查代理是否支持该平台
		if pm.isProxySupportsPlatform(proxy, platform) {
			status.TotalProxies++

			if proxy.IsActive && pm.isProxyAvailableForPlatform(proxy, platform) {
				status.ActiveProxies++
				status.AvailableProxies++
			} else {
				status.FailedProxies++
			}
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

// isProxySupportsPlatform 检查代理是否支持指定平台
func (pm *ProxyManager) isProxySupportsPlatform(proxy *interfaces.ProxyInfo, platform interfaces.Platform) bool {
	if len(proxy.SupportedPlatforms) == 0 {
		// 如果没有明确指定支持的平台，根据地区判断
		return pm.isRegionSupportsPlatform(proxy.Region, platform)
	}

	for _, p := range proxy.SupportedPlatforms {
		if p == platform {
			return true
		}
	}
	return false
}

// isProxyAvailableForPlatform 检查代理是否可用于指定平台
func (pm *ProxyManager) isProxyAvailableForPlatform(proxy *interfaces.ProxyInfo, platform interfaces.Platform) bool {
	if !proxy.IsActive {
		return false
	}

	// 检查是否支持该平台
	if !pm.isProxySupportsPlatform(proxy, platform) {
		return false
	}

	// 检查平台状态
	if proxy.PlatformStatuses != nil {
		status, exists := proxy.PlatformStatuses[platform]
		if exists {
			// 检查是否被封禁
			if !status.BannedUntil.IsZero() && time.Now().Before(status.BannedUntil) {
				return false
			}
			if !status.IsActive {
				return false
			}
		}
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

// isRegionSupportsPlatform 检查地区是否支持指定平台
func (pm *ProxyManager) isRegionSupportsPlatform(region interfaces.ProxyRegion, platform interfaces.Platform) bool {
	switch region {
	case interfaces.ProxyRegionDomestic:
		return platform == interfaces.PlatformBuff
	case interfaces.ProxyRegionHongKong:
		return platform == interfaces.PlatformBuff || platform == interfaces.PlatformSteam
	case interfaces.ProxyRegionOverseas:
		return platform == interfaces.PlatformSteam
	default:
		return false
	}
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
