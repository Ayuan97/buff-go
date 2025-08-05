package service

import (
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"fmt"
	"sync"

	"gorm.io/gorm"
)

var (
	myDao           *dao.Dao
	configManager   *SystemConfigManager
	configManagerMu sync.Once
)

// SystemConfigManager 系统配置管理器
type SystemConfigManager struct {
	dao       *dao.Dao
	listeners map[int64][]func(configID int64)
	mu        sync.RWMutex
}

// ConfigChangeListener 配置变更监听器函数类型
type ConfigChangeListener func(configID int64)

func Initialize(engine *gorm.DB) {
	myDao = dao.New(engine)
}

// GetConfigManager 获取配置管理器实例
func GetConfigManager() *SystemConfigManager {
	configManagerMu.Do(func() {
		configManager = &SystemConfigManager{
			dao:       myDao,
			listeners: make(map[int64][]func(configID int64)),
		}
	})
	return configManager
}

// AddConfigChangeListener 添加配置变更监听器
func (scm *SystemConfigManager) AddConfigChangeListener(configID int64, listener ConfigChangeListener) {
	scm.mu.Lock()
	defer scm.mu.Unlock()

	if scm.listeners[configID] == nil {
		scm.listeners[configID] = make([]func(configID int64), 0)
	}
	scm.listeners[configID] = append(scm.listeners[configID], listener)
}

// GetConfig 获取指定ID的配置
func (scm *SystemConfigManager) GetConfig(configID int64) model.Config {
	return scm.dao.GetOneSystemConfig(configID)
}

// ValidateConfig 验证配置的有效性
func ValidateConfig(config model.Config) error {
	if config.ID == 0 {
		return fmt.Errorf("配置ID不能为空")
	}

	if config.BuffPageNum < 0 {
		return fmt.Errorf("Buff页面数量不能为负数")
	}

	if config.SteamPageNum < 0 {
		return fmt.Errorf("Steam页面数量不能为负数")
	}

	if config.MinPrice < 0 {
		return fmt.Errorf("最小价格不能为负数")
	}

	if config.MaxPrice < 0 {
		return fmt.Errorf("最大价格不能为负数")
	}

	if config.MinPrice > config.MaxPrice {
		return fmt.Errorf("最小价格不能大于最大价格")
	}

	return nil
}

// GetCurrentGameConfig 获取当前活跃的游戏配置
func GetCurrentGameConfig() (model.Config, string, error) {
	if myDao == nil {
		return model.Config{}, "", fmt.Errorf("DAO未初始化")
	}

	// 使用DAO中的GetActiveGameConfig方法
	config, game, err := myDao.GetActiveGameConfig()
	if err != nil {
		return config, game, fmt.Errorf("获取活跃游戏配置失败: %v", err)
	}

	// 验证配置
	if err := ValidateConfig(config); err != nil {
		return config, game, fmt.Errorf("配置验证失败: %v", err)
	}

	return config, game, nil
}
