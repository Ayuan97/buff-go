package main

import (
	"buff-go/global"
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"buff-go/internal/scraper/core"
	"buff-go/internal/service"
	"buff-go/pkg/logger"
	"buff-go/pkg/setting"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/go-redis/redis/v8"
)

var (
	scraperTypes = flag.String("scrapers", "buff_buy", "Comma-separated list of scrapers to run (buff_buy,buff_sell,steam_buy,steam_sell)")
	configFile   = flag.String("config", "", "Path to config file")
)

func main() {
	flag.Parse()

	fmt.Fprintf(color.Output, "%s\n", color.GreenString("开始初始化统一抓取器框架..."))

	// 初始化基础组件
	if err := initialize(); err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	fmt.Fprintf(color.Output, "%s\n", color.GreenString("初始化成功，开始启动抓取器..."))

	// 创建管理器
	managers, err := createManagers()
	if err != nil {
		log.Fatalf("创建管理器失败: %v", err)
	}

	// 创建抓取器工厂和管理器
	scraperFactory := core.NewScraperFactory(managers.dao)
	scraperFactory.SetManagers(
		managers.httpManager,
		managers.proxyManager,
		managers.configManager,
		managers.cacheManager,
		managers.errorHandler,
		managers.taskManager,
	)

	scraperManager := core.NewScraperManager(scraperFactory)

	// 解析要启动的抓取器类型
	types := parseScraperTypes(*scraperTypes)

	// 启动抓取器
	for _, scraperType := range types {
		fmt.Printf("启动抓取器: %s\n", scraperType)
		if err := scraperManager.StartScraper(scraperType); err != nil {
			log.Printf("启动抓取器 %s 失败: %v", scraperType, err)
		} else {
			fmt.Fprintf(color.Output, "%s\n", color.GreenString(fmt.Sprintf("抓取器 %s 启动成功", scraperType)))
		}
	}

	// 等待信号
	waitForSignal(scraperManager)

	fmt.Fprintf(color.Output, "%s\n", color.YellowString("正在关闭抓取器..."))

	// 停止所有抓取器
	if err := scraperManager.StopAllScrapers(); err != nil {
		log.Printf("停止抓取器失败: %v", err)
	}

	fmt.Fprintf(color.Output, "%s\n", color.GreenString("抓取器已安全关闭"))
}

// Managers 管理器集合
type Managers struct {
	dao           *dao.Dao
	httpManager   core.IHTTPClientManager
	proxyManager  core.IProxyManager
	configManager core.IConfigManager
	cacheManager  core.ICacheManager
	errorHandler  core.IErrorHandler
	taskManager   core.ITaskManager
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

// createManagers 创建管理器
func createManagers() (*Managers, error) {
	// 创建DAO
	daoInstance := dao.New(global.DBEngine)

	// 创建各种管理器
	proxyManager := core.NewProxyManager()
	errorHandler := core.NewErrorHandler()
	httpManager := core.NewHTTPClientManager(proxyManager, errorHandler)
	configManager := core.NewConfigManager()
	cacheManager := core.NewCacheManager()
	taskManager := core.NewTaskManager()

	return &Managers{
		dao:           daoInstance,
		httpManager:   httpManager,
		proxyManager:  proxyManager,
		configManager: configManager,
		cacheManager:  cacheManager,
		errorHandler:  errorHandler,
		taskManager:   taskManager,
	}, nil
}

// parseScraperTypes 解析抓取器类型
func parseScraperTypes(typesStr string) []core.ScraperType {
	var types []core.ScraperType

	parts := strings.Split(typesStr, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			types = append(types, core.ScraperType(part))
		}
	}

	return types
}

// waitForSignal 等待信号
func waitForSignal(scraperManager *core.ScraperManager) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 启动状态监控
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				status := scraperManager.GetAllScraperStatus()
				fmt.Printf("抓取器状态: %+v\n", status)
			case <-sigChan:
				return
			}
		}
	}()

	<-sigChan
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
