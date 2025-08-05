package main

import (
	"buff-go/global"
	"buff-go/internal/migration"
	"buff-go/internal/model"
	"buff-go/internal/service"
	"fmt"
	"log"

	"buff-go/pkg/logger"
	"buff-go/pkg/setting"
)

func main() {
	fmt.Println("开始配置系统迁移...")
	
	// 初始化设置
	if err := setupSetting(); err != nil {
		log.Fatalf("设置配置失败: %v", err)
	}
	
	// 初始化日志
	if err := setupLogger(); err != nil {
		log.Fatalf("设置日志失败: %v", err)
	}
	
	// 初始化数据库
	if err := setupDBEngine(); err != nil {
		log.Fatalf("设置数据库失败: %v", err)
	}
	
	// 运行迁移
	if err := migration.RunConfigMigration(global.DBEngine); err != nil {
		log.Fatalf("配置迁移失败: %v", err)
	}
	
	// 验证迁移
	if err := migration.ValidateMigration(global.DBEngine); err != nil {
		log.Fatalf("迁移验证失败: %v", err)
	}
	
	// 初始化服务（这会触发配置初始化）
	service.Initialize(global.DBEngine)
	
	// 测试新的配置获取方式
	testNewConfigSystem()
	
	fmt.Println("配置系统迁移完成！")
}

func testNewConfigSystem() {
	fmt.Println("\n=== 测试新配置系统 ===")
	
	// 测试获取当前游戏配置
	config, game, err := service.GetCurrentGameConfig()
	if err != nil {
		fmt.Printf("❌ 获取当前游戏配置失败: %v\n", err)
		return
	}
	
	fmt.Printf("✅ 当前活跃游戏: %s\n", game)
	fmt.Printf("✅ 配置ID: %d\n", config.ID)
	fmt.Printf("✅ 游戏名称: %s\n", config.GameName)
	fmt.Printf("✅ Buff买入状态: %d\n", config.BuffBuyStatus)
	fmt.Printf("✅ 页面数量: %d\n", config.BuffPageNum)
	
	// 测试根据游戏名称获取配置
	fmt.Println("\n--- 测试根据游戏名称获取配置 ---")
	csgoConfig := service.GetConfigByGameName("csgo")
	fmt.Printf("✅ CSGO配置 - ID: %d, GameName: %s, 页面数: %d\n", 
		csgoConfig.ID, csgoConfig.GameName, csgoConfig.BuffPageNum)
	
	dota2Config := service.GetConfigByGameName("dota2")
	fmt.Printf("✅ DOTA2配置 - ID: %d, GameName: %s, 页面数: %d\n", 
		dota2Config.ID, dota2Config.GameName, dota2Config.BuffPageNum)
	
	// 测试获取所有配置
	fmt.Println("\n--- 测试获取所有配置 ---")
	allConfigs := service.GetAllGameConfigs()
	for gameName, config := range allConfigs {
		fmt.Printf("✅ %s配置 - ID: %d, GameName: %s, Buff状态: %d\n", 
			gameName, config.ID, config.GameName, config.BuffBuyStatus)
	}
	
	fmt.Println("\n新配置系统测试完成！")
}

func setupSetting() error {
	setting, err := setting.NewSetting()
	if err != nil {
		return err
	}
	
	err = setting.ReadSection("Server", &global.ServerSetting)
	if err != nil {
		return err
	}
	
	err = setting.ReadSection("Log", &global.LoggerSetting)
	if err != nil {
		return err
	}
	
	err = setting.ReadSection("Database", &global.DatabaseSetting)
	if err != nil {
		return err
	}
	
	err = setting.ReadSection("Redis", &global.RedisSetting)
	if err != nil {
		return err
	}
	
	return nil
}

func setupLogger() error {
	logger, err := logger.New(global.LoggerSetting)
	if err != nil {
		return err
	}
	global.Logger = logger
	return nil
}

func setupDBEngine() error {
	var err error
	global.DBEngine, err = model.NewDBEngine(global.DatabaseSetting)
	if err != nil {
		return err
	}
	return nil
}
