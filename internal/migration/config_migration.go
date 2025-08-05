package migration

import (
	"buff-go/internal/model"
	"fmt"
	"gorm.io/gorm"
)

// MigrateConfigTable 迁移配置表，添加 game_name 字段并迁移数据
func MigrateConfigTable(db *gorm.DB) error {
	fmt.Println("开始迁移配置表...")
	
	// 1. 自动迁移表结构，添加 game_name 字段
	if err := db.AutoMigrate(&model.Config{}); err != nil {
		return fmt.Errorf("自动迁移配置表失败: %v", err)
	}
	
	// 2. 检查并更新现有记录的 game_name 字段
	if err := updateExistingConfigRecords(db); err != nil {
		return fmt.Errorf("更新现有配置记录失败: %v", err)
	}
	
	fmt.Println("配置表迁移完成")
	return nil
}

// updateExistingConfigRecords 更新现有配置记录的 game_name 字段
func updateExistingConfigRecords(db *gorm.DB) error {
	var configs []model.Config
	
	// 获取所有配置记录
	if err := db.Find(&configs).Error; err != nil {
		return err
	}
	
	for _, config := range configs {
		// 如果 game_name 为空，根据 ID 设置默认值
		if config.GameName == "" {
			var gameName string
			if config.ID == 1 {
				gameName = "csgo"
			} else if config.ID == 2 {
				gameName = "dota2"
			} else {
				// 其他ID默认为csgo
				gameName = "csgo"
			}
			
			// 更新记录
			if err := db.Model(&config).Where("id = ?", config.ID).Update("game_name", gameName).Error; err != nil {
				fmt.Printf("更新配置记录失败 ID=%d: %v\n", config.ID, err)
				continue
			}
			
			fmt.Printf("已更新配置记录 ID=%d game_name=%s\n", config.ID, gameName)
		}
	}
	
	return nil
}

// CreateDefaultConfigs 创建默认配置记录
func CreateDefaultConfigs(db *gorm.DB) error {
	fmt.Println("检查并创建默认配置...")
	
	// 检查CSGO配置
	var csgoCount int64
	db.Model(&model.Config{}).Where("id = ?", 1).Count(&csgoCount)
	if csgoCount == 0 {
		csgoConfig := &model.Config{
			Model:              &model.Model{ID: 1},
			GameName:           "csgo",
			BuffBuyStatus:      1,
			BuffSellStatus:     1,
			SteamBuyStatus:     0,
			SteamSellStatus:    0,
			BuffBuyDelay:       5,
			BuffSellDelay:      5,
			SteamBuyDelay:      10,
			SteamSellDelay:     10,
			BotFilter:          "",
			BuffPageNum:        10,
			SteamPageNum:       5,
			BotBuffProportion:  0.95,
			BotSteamProportion: 0.95,
			BotPrice:           100.0,
			MinPrice:           0.01,
			MaxPrice:           1000.0,
		}
		
		if err := db.Create(csgoConfig).Error; err != nil {
			return fmt.Errorf("创建CSGO默认配置失败: %v", err)
		}
		fmt.Println("已创建CSGO默认配置")
	}
	
	// 检查DOTA2配置
	var dota2Count int64
	db.Model(&model.Config{}).Where("id = ?", 2).Count(&dota2Count)
	if dota2Count == 0 {
		dota2Config := &model.Config{
			Model:              &model.Model{ID: 2},
			GameName:           "dota2",
			BuffBuyStatus:      1,
			BuffSellStatus:     1,
			SteamBuyStatus:     0,
			SteamSellStatus:    0,
			BuffBuyDelay:       5,
			BuffSellDelay:      5,
			SteamBuyDelay:      10,
			SteamSellDelay:     10,
			BotFilter:          "",
			BuffPageNum:        5,  // DOTA2默认较少页面
			SteamPageNum:       3,
			BotBuffProportion:  0.95,
			BotSteamProportion: 0.95,
			BotPrice:           100.0,
			MinPrice:           0.01,
			MaxPrice:           1000.0,
		}
		
		if err := db.Create(dota2Config).Error; err != nil {
			return fmt.Errorf("创建DOTA2默认配置失败: %v", err)
		}
		fmt.Println("已创建DOTA2默认配置")
	}
	
	return nil
}

// RunConfigMigration 运行完整的配置迁移
func RunConfigMigration(db *gorm.DB) error {
	fmt.Println("=== 开始配置系统迁移 ===")
	
	// 1. 迁移表结构
	if err := MigrateConfigTable(db); err != nil {
		return err
	}
	
	// 2. 创建默认配置
	if err := CreateDefaultConfigs(db); err != nil {
		return err
	}
	
	fmt.Println("=== 配置系统迁移完成 ===")
	return nil
}

// ValidateMigration 验证迁移结果
func ValidateMigration(db *gorm.DB) error {
	fmt.Println("验证迁移结果...")
	
	var configs []model.Config
	if err := db.Find(&configs).Error; err != nil {
		return fmt.Errorf("查询配置失败: %v", err)
	}
	
	for _, config := range configs {
		if config.GameName == "" {
			return fmt.Errorf("配置记录 ID=%d 的 game_name 字段为空", config.ID)
		}
		fmt.Printf("✅ 配置记录 ID=%d game_name=%s 验证通过\n", config.ID, config.GameName)
	}
	
	fmt.Println("迁移验证完成")
	return nil
}
