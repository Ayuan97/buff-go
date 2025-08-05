package main

import (
	"buff-go/global"
	"buff-go/internal/model"
	"buff-go/internal/service"
	"fmt"
	"log"
	"time"

	"buff-go/pkg/logger"
	"buff-go/pkg/setting"
)

func main() {
	fmt.Println("开始配置变更测试...")
	
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
	
	fmt.Println("初始化完成，开始测试配置变更...")
	
	// 测试配置变更监听
	testConfigChangeListener()
	
	// 测试配置更新
	testConfigUpdate()
	
	fmt.Println("配置变更测试完成")
}

func testConfigChangeListener() {
	fmt.Println("\n=== 配置变更监听测试 ===")
	
	configManager := service.GetConfigManager()
	
	// 添加测试监听器
	configManager.AddConfigChangeListener(1, func(configID int64) {
		fmt.Printf("✅ 收到CSGO配置变更通知: %d\n", configID)
	})
	
	configManager.AddConfigChangeListener(2, func(configID int64) {
		fmt.Printf("✅ 收到DOTA2配置变更通知: %d\n", configID)
	})
	
	fmt.Println("配置变更监听器已注册")
	
	// 等待一下让监听器准备好
	time.Sleep(2 * time.Second)
	
	// 发送测试消息
	fmt.Println("发送CSGO配置变更测试消息...")
	err := service.PublishMessage("config_change:1", `{"config_id":1,"timestamp":1234567890,"action":"test"}`)
	if err != nil {
		fmt.Printf("❌ 发送CSGO测试消息失败: %v\n", err)
	} else {
		fmt.Println("✅ CSGO测试消息发送成功")
	}
	
	fmt.Println("发送DOTA2配置变更测试消息...")
	err = service.PublishMessage("config_change:2", `{"config_id":2,"timestamp":1234567890,"action":"test"}`)
	if err != nil {
		fmt.Printf("❌ 发送DOTA2测试消息失败: %v\n", err)
	} else {
		fmt.Println("✅ DOTA2测试消息发送成功")
	}
	
	// 等待消息处理
	time.Sleep(3 * time.Second)
}

func testConfigUpdate() {
	fmt.Println("\n=== 配置更新测试 ===")
	
	configManager := service.GetConfigManager()
	
	// 获取当前CSGO配置
	originalConfig := configManager.GetConfig(1)
	fmt.Printf("原始CSGO配置 - 页面数: %d, 价格范围: %.2f-%.2f\n", 
		originalConfig.BuffPageNum, originalConfig.MinPrice, originalConfig.MaxPrice)
	
	// 创建新配置
	newConfig := originalConfig
	newConfig.BuffPageNum = 15  // 修改页面数
	newConfig.MinPrice = 0.05   // 修改最小价格
	newConfig.MaxPrice = 2000.0 // 修改最大价格
	
	fmt.Printf("更新CSGO配置 - 页面数: %d, 价格范围: %.2f-%.2f\n", 
		newConfig.BuffPageNum, newConfig.MinPrice, newConfig.MaxPrice)
	
	// 更新配置
	err := configManager.UpdateConfig(1, &newConfig)
	if err != nil {
		fmt.Printf("❌ 更新配置失败: %v\n", err)
		return
	}
	
	fmt.Println("✅ 配置更新成功")
	
	// 等待配置变更通知
	time.Sleep(2 * time.Second)
	
	// 验证配置是否更新
	updatedConfig := configManager.GetConfig(1)
	fmt.Printf("更新后CSGO配置 - 页面数: %d, 价格范围: %.2f-%.2f\n", 
		updatedConfig.BuffPageNum, updatedConfig.MinPrice, updatedConfig.MaxPrice)
	
	if updatedConfig.BuffPageNum == newConfig.BuffPageNum && 
	   updatedConfig.MinPrice == newConfig.MinPrice && 
	   updatedConfig.MaxPrice == newConfig.MaxPrice {
		fmt.Println("✅ 配置更新验证成功")
	} else {
		fmt.Println("❌ 配置更新验证失败")
	}
	
	// 恢复原始配置
	fmt.Println("恢复原始配置...")
	err = configManager.UpdateConfig(1, &originalConfig)
	if err != nil {
		fmt.Printf("❌ 恢复原始配置失败: %v\n", err)
	} else {
		fmt.Println("✅ 原始配置恢复成功")
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
