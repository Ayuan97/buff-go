package service

import (
	"buff-go/internal/model"
	"buff-go/internal/scraper/components/proxy"
	"buff-go/internal/scraper/interfaces"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"fmt"
	"strconv"
	"time"
)

// ProxyService 代理服务，整合新旧代理管理逻辑
type ProxyService struct {
	manager *proxy.ProxyManager
}

// NewProxyService 创建代理服务
func NewProxyService() *ProxyService {
	config := &proxy.ProxyManagerConfig{
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
		EnableLocalProxy: true, // 启用本机代理作为备用
		LocalProxyPlatforms: []interfaces.Platform{
			interfaces.PlatformBuff,
			interfaces.PlatformSteam,
		},
	}

	return &ProxyService{
		manager: proxy.NewProxyManager(config),
	}
}

// GetProxyForPlatform 获取指定平台的代理
func (ps *ProxyService) GetProxyForPlatform(platform interfaces.Platform) (*interfaces.ProxyInfo, error) {
	return ps.manager.GetProxyForPlatform(platform)
}

// GetProxyForPlatformLegacy 获取指定平台的代理（数据库直接查询）
func (ps *ProxyService) GetProxyForPlatformLegacy(platform model.Platform) (*model.Ip, string, string, error) {
	ips, err := myDao.GetProxiesForPlatform(platform)
	if err != nil {
		return nil, "", "", err
	}

	for _, ip := range ips {
		// 检查代理是否可用于该平台
		if !ip.IsAvailableForPlatform(platform) {
			continue
		}

		address := ip.Ip + ":" + strconv.Itoa(ip.Port)
		key := rediskey.GetProxyMapKey(address, string(platform))
		value := gredis.Get(key)

		if value == "" {
			return ip, address, key, nil
		}
	}
	return nil, "", "", fmt.Errorf("no available proxy for platform %s", platform)
}

// IsLocalProxyEnabled 检查是否启用了本机代理
func (ps *ProxyService) IsLocalProxyEnabled() bool {
	return ps.manager.IsLocalProxyEnabled()
}

// IsLocalProxySupportedForPlatform 检查本机代理是否支持指定平台
func (ps *ProxyService) IsLocalProxySupportedForPlatform(platform interfaces.Platform) bool {
	return ps.manager.IsLocalProxySupportedForPlatform(platform)
}

// GetLocalProxy 获取本机代理信息
func (ps *ProxyService) GetLocalProxy() *interfaces.ProxyInfo {
	return ps.manager.GetLocalProxy()
}

// MarkProxyFailedForPlatform 标记代理在指定平台失败
func (ps *ProxyService) MarkProxyFailedForPlatform(ip *model.Ip, platform model.Platform, reason string) error {
	if ip == nil {
		return fmt.Errorf("ip is nil")
	}

	// 标记平台失败
	err := ip.MarkPlatformFailed(platform, 3, 30*time.Minute)
	if err != nil {
		return err
	}

	// 更新数据库
	return myDao.UpdateProxyPlatformStatus(ip)
}

// RecoverProxyForPlatform 恢复代理在指定平台的状态
func (ps *ProxyService) RecoverProxyForPlatform(ip *model.Ip, platform model.Platform) error {
	if ip == nil {
		return fmt.Errorf("ip is nil")
	}

	err := ip.RecoverPlatformStatus(platform)
	if err != nil {
		return err
	}

	// 更新数据库
	return myDao.UpdateProxyPlatformStatus(ip)
}

// LoadProxiesFromDatabase 从数据库加载代理到管理器
func (ps *ProxyService) LoadProxiesFromDatabase() error {
	// 获取所有代理
	ips, err := myDao.GetAllIp()
	if err != nil {
		return err
	}

	// 转换为新格式并添加到管理器
	for _, dbIP := range ips {
		proxyInfo := ps.convertToProxyInfo(dbIP)
		ps.manager.AddProxy(proxyInfo)
	}

	return nil
}

// convertToProxyInfo 将数据库模型转换为代理信息
func (ps *ProxyService) convertToProxyInfo(dbIP *model.Ip) *interfaces.ProxyInfo {
	// 构建代理URL
	var proxyURL string
	if dbIP.ProxyType == 1 { // HTTP代理
		if dbIP.Name != "" && dbIP.PassWord != "" {
			proxyURL = fmt.Sprintf("http://%s:%s@%s:%d", dbIP.Name, dbIP.PassWord, dbIP.Ip, dbIP.Port)
		} else {
			proxyURL = fmt.Sprintf("http://%s:%d", dbIP.Ip, dbIP.Port)
		}
	} else { // SOCKS5代理
		if dbIP.Name != "" && dbIP.PassWord != "" {
			proxyURL = fmt.Sprintf("socks5://%s:%s@%s:%d", dbIP.Name, dbIP.PassWord, dbIP.Ip, dbIP.Port)
		} else {
			proxyURL = fmt.Sprintf("socks5://%s:%d", dbIP.Ip, dbIP.Port)
		}
	}

	// 获取支持的平台
	supportedPlatforms, _ := dbIP.GetSupportedPlatforms()

	// 获取平台状态
	platformStatuses, _ := dbIP.GetPlatformStatuses()

	// 转换平台状态格式
	interfaceStatuses := make(map[interfaces.Platform]*interfaces.PlatformStatus)
	for platform, status := range platformStatuses {
		interfaceStatuses[interfaces.Platform(platform)] = &interfaces.PlatformStatus{
			IsActive:    status.IsActive,
			FailCount:   status.FailCount,
			LastFailed:  status.LastFailed,
			BannedUntil: status.BannedUntil,
		}
	}

	// 转换支持的平台格式
	interfacePlatforms := make([]interfaces.Platform, len(supportedPlatforms))
	for i, platform := range supportedPlatforms {
		interfacePlatforms[i] = interfaces.Platform(platform)
	}

	return &interfaces.ProxyInfo{
		ID:                 fmt.Sprintf("%d", dbIP.ID),
		URL:                proxyURL,
		Type:               ps.getProxyType(dbIP.ProxyType),
		Username:           dbIP.Name,
		Password:           dbIP.PassWord,
		Country:            ps.getCountryName(dbIP.Country),
		Speed:              dbIP.Speed,
		LastUsed:           time.Time{},
		FailCount:          0,
		IsActive:           dbIP.IsGlobalActive,
		Region:             interfaces.ProxyRegion(dbIP.Region),
		SupportedPlatforms: interfacePlatforms,
		PlatformStatuses:   interfaceStatuses,
	}
}

// getProxyType 获取代理类型字符串
func (ps *ProxyService) getProxyType(proxyType int) string {
	switch proxyType {
	case 1:
		return "http"
	case 2:
		return "socks5"
	default:
		return "http"
	}
}

// getCountryName 获取国家名称
func (ps *ProxyService) getCountryName(country int) string {
	switch country {
	case 1:
		return "China"
	case 2:
		return "HongKong"
	case 3:
		return "Overseas"
	default:
		return "Unknown"
	}
}

// GetPoolStatusForPlatform 获取指定平台的代理池状态
func (ps *ProxyService) GetPoolStatusForPlatform(platform interfaces.Platform) *interfaces.ProxyPoolStatus {
	return ps.manager.GetPoolStatusForPlatform(platform)
}

// 全局代理服务实例
var GlobalProxyService *ProxyService

// InitProxyService 初始化全局代理服务
func InitProxyService() error {
	GlobalProxyService = NewProxyService()
	return GlobalProxyService.LoadProxiesFromDatabase()
}
