package main

import (
	"buff-go/global"
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"buff-go/internal/scraper/framework"
	"buff-go/internal/service"
	"buff-go/pkg/logger"
	"buff-go/pkg/setting"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/go-redis/redis/v8"
)

var (
	action = flag.String("action", "list", "Action to perform: list, get, set, delete")
	key    = flag.String("key", "", "Configuration key")
	value  = flag.String("value", "", "Configuration value")
)

func main() {
	flag.Parse()

	fmt.Println("🔧 配置管理工具")
	fmt.Println("================")

	// 初始化基础组件
	if err := initialize(); err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	// 创建配置管理器
	daoInstance := dao.New(global.DBEngine)
	configManager := framework.NewConfigManager(daoInstance)

	// 执行操作
	switch *action {
	case "list":
		listConfigs(configManager)
	case "get":
		getConfig(configManager, *key)
	case "set":
		setConfig(configManager, *key, *value)
	case "delete":
		deleteConfig(configManager, *key)
	default:
		fmt.Printf("未知操作: %s\n", *action)
		printUsage()
	}
}

func listConfigs(cm framework.IConfigManager) {
	fmt.Println("📋 配置列表:")

	// 常用配置键列表
	keys := []string{
		"system.type",
		"buff.buy.status",
		"buff.sell.status",
		"steam.buy.status",
		"steam.sell.status",
		"buff.page.num",
		"steam.page.num",
		"min.price",
		"max.price",
		"buff.buy.delay",
		"buff.sell.delay",
		"steam.buy.delay",
		"steam.sell.delay",
	}

	for _, key := range keys {
		value, err := cm.Get(key)
		if err != nil {
			fmt.Printf("  %s: <未设置> (%v)\n", key, err)
		} else {
			fmt.Printf("  %s: %v\n", key, value)
		}
	}
}

func getConfig(cm framework.IConfigManager, key string) {
	if key == "" {
		fmt.Println("❌ 请指定配置键")
		return
	}

	value, err := cm.Get(key)
	if err != nil {
		fmt.Printf("❌ 获取配置失败: %v\n", err)
		return
	}

	fmt.Printf("✅ %s = %v\n", key, value)
}

func setConfig(cm framework.IConfigManager, key, value string) {
	if key == "" || value == "" {
		fmt.Println("❌ 请指定配置键和值")
		return
	}

	err := cm.Set(key, value)
	if err != nil {
		fmt.Printf("❌ 设置配置失败: %v\n", err)
		return
	}

	fmt.Printf("✅ 已设置 %s = %s\n", key, value)
}

func deleteConfig(cm framework.IConfigManager, key string) {
	if key == "" {
		fmt.Println("❌ 请指定配置键")
		return
	}

	// 配置管理器暂时没有删除方法，可以设置为空值
	err := cm.Set(key, "")
	if err != nil {
		fmt.Printf("❌ 删除配置失败: %v\n", err)
		return
	}

	fmt.Printf("✅ 已删除配置 %s\n", key)
}

func printUsage() {
	fmt.Println("\n📖 使用说明:")
	fmt.Println("  列出所有配置: go run config_tool.go -action=list")
	fmt.Println("  获取配置: go run config_tool.go -action=get -key=buff.buy.status")
	fmt.Println("  设置配置: go run config_tool.go -action=set -key=buff.buy.status -value=1")
	fmt.Println("  删除配置: go run config_tool.go -action=delete -key=buff.buy.status")
}

// initialize 初始化基础组件
func initialize() error {
	// 设置配置
	if err := setupSetting(); err != nil {
		return fmt.Errorf("设置配置失败: %v", err)
	}

	// 设置日志
	if err := setupLogger(); err != nil {
		return fmt.Errorf("设置日志失败: %v", err)
	}

	// 设置数据库
	if err := setupDBEngine(); err != nil {
		return fmt.Errorf("设置数据库失败: %v", err)
	}

	// 初始化服务
	service.Initialize(global.DBEngine)

	return nil
}

// setupSetting 设置配置
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

// setupLogger 设置日志
func setupLogger() error {
	logger, err := logger.New(global.LoggerSetting)
	if err != nil {
		return err
	}
	global.Logger = logger
	return nil
}

// setupDBEngine 设置数据库引擎
func setupDBEngine() error {
	var err error
	global.DBEngine, err = model.NewDBEngine(global.DatabaseSetting)
	if err != nil {
		return err
	}

	global.Redis = redis.NewClient(&redis.Options{
		Addr:     global.RedisSetting.Host,
		Password: global.RedisSetting.Password,
		DB:       global.RedisSetting.DB,
	})

	return nil
}
