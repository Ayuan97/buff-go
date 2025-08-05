package main

import (
	"buff-go/global"
	"buff-go/internal/model"
	"buff-go/internal/service"
	"fmt"
	"log"

	"buff-go/pkg/logger"
	"buff-go/pkg/setting"
)

func main() {
	fmt.Println("开始配置测试...")
	
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
	
	// 初始化服务
	service.Initialize(global.DBEngine)
	
	fmt.Println("初始化完成，开始测试配置...")
	
	// 测试配置获取
	testConfigRetrieval()
	
	fmt.Println("配置测试完成")
}

func testConfigRetrieval() {
	fmt.Println("\n=== 配置获取测试 ===")
	
	// 测试获取当前游戏配置
	config, game, err := service.GetCurrentGameConfig()
	if err != nil {
		fmt.Printf("获取当前游戏配置失败: %v\n", err)
		return
	}
	
	fmt.Printf("当前游戏: %s\n", game)
	fmt.Printf("配置ID: %d\n", config.ID)
	fmt.Printf("Buff买入状态: %d\n", config.BuffBuyStatus)
	fmt.Printf("Buff页面数: %d\n", config.BuffPageNum)
	fmt.Printf("价格范围: %.2f - %.2f\n", config.MinPrice, config.MaxPrice)
	
	// 验证配置有效性
	if err := service.ValidateConfig(config); err != nil {
		fmt.Printf("配置验证失败: %v\n", err)
	} else {
		fmt.Println("配置验证通过")
	}
	
	// 测试CSGO配置
	fmt.Println("\n--- CSGO配置测试 ---")
	testSpecificConfig(1, "CSGO")
	
	// 测试DOTA2配置
	fmt.Println("\n--- DOTA2配置测试 ---")
	testSpecificConfig(2, "DOTA2")
}

func testSpecificConfig(id int64, gameName string) {
	configManager := service.GetConfigManager()
	config := configManager.GetConfig(id)
	
	fmt.Printf("%s配置 (ID=%d):\n", gameName, id)
	fmt.Printf("  配置ID: %d\n", config.ID)
	fmt.Printf("  Buff买入状态: %d\n", config.BuffBuyStatus)
	fmt.Printf("  Buff出售状态: %d\n", config.BuffSellStatus)
	fmt.Printf("  Steam买入状态: %d\n", config.SteamBuyStatus)
	fmt.Printf("  Steam出售状态: %d\n", config.SteamSellStatus)
	fmt.Printf("  Buff页面数: %d\n", config.BuffPageNum)
	fmt.Printf("  Steam页面数: %d\n", config.SteamPageNum)
	fmt.Printf("  价格范围: %.2f - %.2f\n", config.MinPrice, config.MaxPrice)
	fmt.Printf("  Buff比例: %.2f\n", config.BotBuffProportion)
	fmt.Printf("  Steam比例: %.2f\n", config.BotSteamProportion)
	
	if config.ID == 0 {
		fmt.Printf("  ⚠️  警告: %s配置ID为0，可能存在问题\n", gameName)
	} else {
		fmt.Printf("  ✅ %s配置正常\n", gameName)
	}
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
