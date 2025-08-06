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

// GetCurrentGameConfig 获取当前启用状态的游戏配置
func (s *SystemConfigServiceImpl) GetCurrentGameConfig() (model.Config, string, error) {
	// 获取启用状态的配置，优先返回CSGO配置
	config := s.dao.GetOneSystemConfig(1)
	if config.ID != 0 && config.Status == 1 {
		return config, config.GameName, nil
	}

	// 如果CSGO配置未启用，尝试DOTA2配置
	config = s.dao.GetOneSystemConfig(2)
	if config.ID != 0 && config.Status == 1 {
		return config, config.GameName, nil
	}

	return model.Config{}, "", fmt.Errorf("没有找到启用状态的游戏配置")
}

// GetConfigByGameName 根据游戏名称获取启用状态的配置
func (s *SystemConfigServiceImpl) GetConfigByGameName(gameName string) model.Config {
	config := s.dao.GetConfigByGameName(gameName)

	// 检查配置是否启用
	if config.ID != 0 && config.Status == 1 {
		return config
	}

	// 如果配置未启用，返回空配置
	fmt.Printf("配置未启用或不存在 游戏：%s 状态：%d\n", gameName, config.Status)
	return model.Config{}
}

// GetAllGameConfigs 获取所有启用状态的游戏配置
func (s *SystemConfigServiceImpl) GetAllGameConfigs() map[string]model.Config {
	allConfigs := s.dao.GetAllConfigs()
	enabledConfigs := make(map[string]model.Config)

	// 只返回启用状态的配置
	for gameName, config := range allConfigs {
		if config.ID != 0 && config.Status == 1 {
			enabledConfigs[gameName] = config
		}
	}

	return enabledConfigs
}

// ValidateConfig 验证配置有效性
func (s *SystemConfigServiceImpl) ValidateConfig(config model.Config) error {
	if config.BuffPageNum <= 0 {
		return fmt.Errorf("页面数量配置无效: %d", config.BuffPageNum)
	}

	// 验证价格范围
	if config.MinPrice < 0 {
		return fmt.Errorf("最小价格配置无效: %f", config.MinPrice)
	}

	if config.MaxPrice <= config.MinPrice {
		return fmt.Errorf("最大价格配置无效: %f (应大于最小价格 %f)", config.MaxPrice, config.MinPrice)
	}

	return nil
}

// IsConfigEnabled 检查配置是否启用
func (s *SystemConfigServiceImpl) IsConfigEnabled(config model.Config) bool {
	return config.ID != 0 && config.Status == 1
}

// GetEnabledGameConfigs 获取所有启用状态的游戏配置
func (s *SystemConfigServiceImpl) GetEnabledGameConfigs() map[string]model.Config {
	return s.dao.GetEnabledConfigs()
}

// GetEnabledConfigByGameName 根据游戏名称获取启用状态的配置
func (s *SystemConfigServiceImpl) GetEnabledConfigByGameName(gameName string) (model.Config, error) {
	return s.dao.GetEnabledConfigByGameName(gameName)
}
