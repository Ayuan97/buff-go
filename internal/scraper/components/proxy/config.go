package proxy

import (
	"buff-go/internal/scraper/interfaces"
	"fmt"
	"time"
)

// DefaultProxyManagerConfig 返回默认的代理管理器配置
func DefaultProxyManagerConfig() *ProxyManagerConfig {
	return &ProxyManagerConfig{
		MaxFailCount:    5,
		CheckInterval:   5 * time.Minute,
		RefreshInterval: 30 * time.Minute,
		MaxUsageTime:    10 * time.Minute,
		PlatformBanDuration: map[interfaces.Platform]time.Duration{
			interfaces.PlatformBuff:     30 * time.Minute,
			interfaces.PlatformSteam:    60 * time.Minute,
			interfaces.PlatformC5Game:   45 * time.Minute,
			interfaces.PlatformIGXE:     45 * time.Minute,
			interfaces.PlatformUUYouPin: 45 * time.Minute,
		},
		PlatformMaxFails: map[interfaces.Platform]int{
			interfaces.PlatformBuff:     3,
			interfaces.PlatformSteam:    5,
			interfaces.PlatformC5Game:   3,
			interfaces.PlatformIGXE:     3,
			interfaces.PlatformUUYouPin: 3,
		},
		EnableLocalProxy: true, // 默认启用本机代理
		LocalProxyPlatforms: []interfaces.Platform{
			interfaces.PlatformBuff,
			interfaces.PlatformSteam,
			interfaces.PlatformC5Game,
			interfaces.PlatformIGXE,
			interfaces.PlatformUUYouPin,
		},
	}
}

// BuffOnlyProxyConfig 返回仅支持Buff平台的代理配置
func BuffOnlyProxyConfig() *ProxyManagerConfig {
	config := DefaultProxyManagerConfig()
	config.LocalProxyPlatforms = []interfaces.Platform{
		interfaces.PlatformBuff,
	}
	return config
}

// SteamOnlyProxyConfig 返回仅支持Steam平台的代理配置
func SteamOnlyProxyConfig() *ProxyManagerConfig {
	config := DefaultProxyManagerConfig()
	config.LocalProxyPlatforms = []interfaces.Platform{
		interfaces.PlatformSteam,
	}
	return config
}

// DisableLocalProxyConfig 返回禁用本机代理的配置
func DisableLocalProxyConfig() *ProxyManagerConfig {
	config := DefaultProxyManagerConfig()
	config.EnableLocalProxy = false
	config.LocalProxyPlatforms = nil
	return config
}

// ProductionProxyConfig 返回生产环境的代理配置
func ProductionProxyConfig() *ProxyManagerConfig {
	return &ProxyManagerConfig{
		MaxFailCount:    3, // 生产环境更严格的失败阈值
		CheckInterval:   3 * time.Minute,
		RefreshInterval: 15 * time.Minute,
		MaxUsageTime:    5 * time.Minute, // 生产环境更短的使用时间
		PlatformBanDuration: map[interfaces.Platform]time.Duration{
			interfaces.PlatformBuff:     15 * time.Minute, // 生产环境更短的封禁时间
			interfaces.PlatformSteam:    30 * time.Minute,
			interfaces.PlatformC5Game:   20 * time.Minute,
			interfaces.PlatformIGXE:     20 * time.Minute,
			interfaces.PlatformUUYouPin: 20 * time.Minute,
		},
		PlatformMaxFails: map[interfaces.Platform]int{
			interfaces.PlatformBuff:     2, // 生产环境更严格
			interfaces.PlatformSteam:    3,
			interfaces.PlatformC5Game:   2,
			interfaces.PlatformIGXE:     2,
			interfaces.PlatformUUYouPin: 2,
		},
		EnableLocalProxy: true,
		LocalProxyPlatforms: []interfaces.Platform{
			interfaces.PlatformBuff,
			interfaces.PlatformSteam,
			interfaces.PlatformC5Game,
			interfaces.PlatformIGXE,
			interfaces.PlatformUUYouPin,
		},
	}
}

// DevelopmentProxyConfig 返回开发环境的代理配置
func DevelopmentProxyConfig() *ProxyManagerConfig {
	return &ProxyManagerConfig{
		MaxFailCount:    10, // 开发环境更宽松的失败阈值
		CheckInterval:   10 * time.Minute,
		RefreshInterval: 60 * time.Minute,
		MaxUsageTime:    30 * time.Minute, // 开发环境更长的使用时间
		PlatformBanDuration: map[interfaces.Platform]time.Duration{
			interfaces.PlatformBuff:     60 * time.Minute, // 开发环境更长的封禁时间
			interfaces.PlatformSteam:    120 * time.Minute,
			interfaces.PlatformC5Game:   90 * time.Minute,
			interfaces.PlatformIGXE:     90 * time.Minute,
			interfaces.PlatformUUYouPin: 90 * time.Minute,
		},
		PlatformMaxFails: map[interfaces.Platform]int{
			interfaces.PlatformBuff:     5, // 开发环境更宽松
			interfaces.PlatformSteam:    8,
			interfaces.PlatformC5Game:   5,
			interfaces.PlatformIGXE:     5,
			interfaces.PlatformUUYouPin: 5,
		},
		EnableLocalProxy: true,
		LocalProxyPlatforms: []interfaces.Platform{
			interfaces.PlatformBuff,
			interfaces.PlatformSteam,
			interfaces.PlatformC5Game,
			interfaces.PlatformIGXE,
			interfaces.PlatformUUYouPin,
		},
	}
}

// ConfigOption 配置选项函数类型
type ConfigOption func(*ProxyManagerConfig)

// WithLocalProxy 设置本机代理选项
func WithLocalProxy(enabled bool, platforms ...interfaces.Platform) ConfigOption {
	return func(config *ProxyManagerConfig) {
		config.EnableLocalProxy = enabled
		if enabled && len(platforms) > 0 {
			config.LocalProxyPlatforms = platforms
		}
	}
}

// WithPlatformSettings 设置平台相关配置
func WithPlatformSettings(platform interfaces.Platform, maxFails int, banDuration time.Duration) ConfigOption {
	return func(config *ProxyManagerConfig) {
		if config.PlatformMaxFails == nil {
			config.PlatformMaxFails = make(map[interfaces.Platform]int)
		}
		if config.PlatformBanDuration == nil {
			config.PlatformBanDuration = make(map[interfaces.Platform]time.Duration)
		}
		config.PlatformMaxFails[platform] = maxFails
		config.PlatformBanDuration[platform] = banDuration
	}
}

// WithTimings 设置时间相关配置
func WithTimings(checkInterval, refreshInterval, maxUsageTime time.Duration) ConfigOption {
	return func(config *ProxyManagerConfig) {
		config.CheckInterval = checkInterval
		config.RefreshInterval = refreshInterval
		config.MaxUsageTime = maxUsageTime
	}
}

// NewProxyManagerWithOptions 使用选项创建代理管理器配置
func NewProxyManagerWithOptions(baseConfig *ProxyManagerConfig, options ...ConfigOption) *ProxyManagerConfig {
	if baseConfig == nil {
		baseConfig = DefaultProxyManagerConfig()
	}

	// 深拷贝基础配置
	config := &ProxyManagerConfig{
		MaxFailCount:        baseConfig.MaxFailCount,
		HealthCheckURL:      baseConfig.HealthCheckURL,
		CheckInterval:       baseConfig.CheckInterval,
		RefreshInterval:     baseConfig.RefreshInterval,
		MaxUsageTime:        baseConfig.MaxUsageTime,
		EnableLocalProxy:    baseConfig.EnableLocalProxy,
		PlatformBanDuration: make(map[interfaces.Platform]time.Duration),
		PlatformMaxFails:    make(map[interfaces.Platform]int),
		LocalProxyPlatforms: make([]interfaces.Platform, len(baseConfig.LocalProxyPlatforms)),
	}

	// 拷贝映射
	for k, v := range baseConfig.PlatformBanDuration {
		config.PlatformBanDuration[k] = v
	}
	for k, v := range baseConfig.PlatformMaxFails {
		config.PlatformMaxFails[k] = v
	}
	copy(config.LocalProxyPlatforms, baseConfig.LocalProxyPlatforms)

	// 应用选项
	for _, option := range options {
		option(config)
	}

	return config
}

// ValidateConfig 验证配置的有效性
func ValidateConfig(config *ProxyManagerConfig) error {
	if config == nil {
		return fmt.Errorf("配置不能为空")
	}

	if config.MaxFailCount <= 0 {
		return fmt.Errorf("最大失败次数必须大于0")
	}

	if config.CheckInterval <= 0 {
		return fmt.Errorf("检查间隔必须大于0")
	}

	if config.RefreshInterval <= 0 {
		return fmt.Errorf("刷新间隔必须大于0")
	}

	if config.MaxUsageTime <= 0 {
		return fmt.Errorf("最大使用时间必须大于0")
	}

	// 验证平台配置
	for platform, maxFails := range config.PlatformMaxFails {
		if maxFails <= 0 {
			return fmt.Errorf("平台 %s 的最大失败次数必须大于0", platform)
		}
	}

	for platform, duration := range config.PlatformBanDuration {
		if duration <= 0 {
			return fmt.Errorf("平台 %s 的封禁时长必须大于0", platform)
		}
	}

	return nil
}
