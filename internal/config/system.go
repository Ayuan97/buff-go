package config

import (
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"fmt"
)

// SystemConfigServiceImpl 系统配置服务实现
type SystemConfigServiceImpl struct {
	dao *dao.Dao
}

// NewSystemConfigService 创建系统配置服务
func NewSystemConfigService(dao *dao.Dao) SystemConfigService {
	return &SystemConfigServiceImpl{
		dao: dao,
	}
}

// InitializeDefaultConfigs 初始化默认配置
func (s *SystemConfigServiceImpl) InitializeDefaultConfigs() error {
	fmt.Println("开始初始化默认配置...")

	// 检查并创建CSGO配置 (ID=1)
	if err := s.createDefaultConfigIfNotExists(1, "CSGO"); err != nil {
		return fmt.Errorf("创建CSGO默认配置失败: %v", err)
	}

	// 检查并创建DOTA2配置 (ID=2)
	if err := s.createDefaultConfigIfNotExists(2, "DOTA2"); err != nil {
		return fmt.Errorf("创建DOTA2默认配置失败: %v", err)
	}

	fmt.Println("默认配置初始化完成")
	return nil
}

// createDefaultConfigIfNotExists 创建默认配置（如果不存在）
func (s *SystemConfigServiceImpl) createDefaultConfigIfNotExists(id int64, gameName string) error {
	// 直接调用GetOneSystemConfig，如果不存在会自动创建默认配置
	config := s.dao.GetOneSystemConfig(id)
	if config.ID != 0 {
		fmt.Printf("%s配置已存在或已创建，ID=%d\n", gameName, config.ID)
	} else {
		fmt.Printf("%s配置创建完成，ID=%d\n", gameName, id)
	}
	return nil
}

// GetCurrentGameConfig 获取当前游戏配置
// 使用新的逻辑：优先返回启用的配置，如果都未启用则返回CSGO配置
func (s *SystemConfigServiceImpl) GetCurrentGameConfig() (model.Config, string, error) {
	return s.dao.GetActiveGameConfig()
}

// GetConfigByGameName 根据游戏名称获取配置
func (s *SystemConfigServiceImpl) GetConfigByGameName(gameName string) model.Config {
	return s.dao.GetConfigByGameName(gameName)
}

// GetAllGameConfigs 获取所有游戏配置
func (s *SystemConfigServiceImpl) GetAllGameConfigs() map[string]model.Config {
	return s.dao.GetAllConfigs()
}

// ValidateConfig 验证配置有效性
func (s *SystemConfigServiceImpl) ValidateConfig(config model.Config) error {
	if config.BuffPageNum <= 0 {
		return fmt.Errorf("页面数量配置无效: %d", config.BuffPageNum)
	}

	if config.MinPrice < 0 {
		return fmt.Errorf("最小价格配置无效: %f", config.MinPrice)
	}

	if config.MaxPrice <= config.MinPrice {
		return fmt.Errorf("最大价格配置无效: %f (应大于最小价格 %f)", config.MaxPrice, config.MinPrice)
	}

	return nil
}

// 便利函数，用于向后兼容
var (
	defaultService SystemConfigService
	defaultManager SystemConfigManager
)

// InitDefaultService 初始化默认服务实例
func InitDefaultService(dao *dao.Dao) {
	defaultService = NewSystemConfigService(dao)
	defaultManager = GetSystemConfigManager(dao)
}

// InitializeDefaultConfigs 初始化默认配置（便利函数）
func InitializeDefaultConfigs() error {
	if defaultService == nil {
		return fmt.Errorf("系统配置服务未初始化")
	}
	return defaultService.InitializeDefaultConfigs()
}

// GetCurrentGameConfig 获取当前游戏配置（便利函数）
func GetCurrentGameConfig() (model.Config, string, error) {
	if defaultService == nil {
		return model.Config{}, "", fmt.Errorf("系统配置服务未初始化")
	}
	return defaultService.GetCurrentGameConfig()
}

// GetConfigByGameName 根据游戏名称获取配置（便利函数）
func GetConfigByGameName(gameName string) model.Config {
	if defaultService == nil {
		return model.Config{}
	}
	return defaultService.GetConfigByGameName(gameName)
}

// GetAllGameConfigs 获取所有游戏配置（便利函数）
func GetAllGameConfigs() map[string]model.Config {
	if defaultService == nil {
		return make(map[string]model.Config)
	}
	return defaultService.GetAllGameConfigs()
}

// ValidateConfig 验证配置有效性（便利函数）
func ValidateConfig(config model.Config) error {
	if defaultService == nil {
		return fmt.Errorf("系统配置服务未初始化")
	}
	return defaultService.ValidateConfig(config)
}

// GetConfigManager 获取配置管理器（便利函数）
func GetConfigManager() SystemConfigManager {
	return defaultManager
}
