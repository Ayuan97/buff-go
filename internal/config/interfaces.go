package config

import (
	"buff-go/internal/model"
)

// SystemConfigManager 系统配置管理器接口
type SystemConfigManager interface {
	// GetConfig 获取配置
	GetConfig(configID int64) model.Config
	
	// UpdateConfig 更新配置
	UpdateConfig(configID int64, config *model.Config) error
	
	// RefreshConfig 刷新配置缓存
	RefreshConfig(configID int64)
	
	// AddConfigChangeListener 添加配置变更监听器
	AddConfigChangeListener(configID int64, listener ConfigChangeListener)
	
	// RemoveConfigChangeListener 移除配置变更监听器
	RemoveConfigChangeListener(configID int64)
}

// ConfigChangeListener 配置变更监听器
type ConfigChangeListener func(configID int64)

// SystemConfigService 系统配置服务接口
type SystemConfigService interface {
	// InitializeDefaultConfigs 初始化默认配置
	InitializeDefaultConfigs() error
	
	// GetCurrentGameConfig 获取当前游戏配置
	GetCurrentGameConfig() (model.Config, string, error)
	
	// GetConfigByGameName 根据游戏名称获取配置
	GetConfigByGameName(gameName string) model.Config
	
	// GetAllGameConfigs 获取所有游戏配置
	GetAllGameConfigs() map[string]model.Config
	
	// ValidateConfig 验证配置有效性
	ValidateConfig(config model.Config) error
}
