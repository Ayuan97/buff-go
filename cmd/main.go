package main

import (
	"buff-go/global"
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"buff-go/internal/scraper/components/cache"
	"buff-go/internal/scraper/components/client"
	"buff-go/internal/scraper/components/config"
	errorhandler "buff-go/internal/scraper/components/error"
	"buff-go/internal/scraper/components/proxy"
	"buff-go/internal/scraper/components/task"
	"buff-go/internal/scraper/core/factory"
	"buff-go/internal/scraper/interfaces"
	"buff-go/internal/service"
	"buff-go/pkg/logger"
	"buff-go/pkg/setting"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/go-redis/redis/v8"
)

// ==================== 抓取器启动配置 ====================
// 通过注释/取消注释来控制启动哪些抓取器
// 注释掉不需要的抓取器，取消注释需要的抓取器
func getEnabledScrapers() []interfaces.ScraperType {
	var enabledScrapers []interfaces.ScraperType

	// Buff平台抓取器
	enabledScrapers = append(enabledScrapers, interfaces.ScraperTypeBuffBuy) // Buff买入数据抓取
	//enabledScrapers = append(enabledScrapers, interfaces.ScraperTypeBuffSell) // Buff卖出数据抓取

	// Steam平台抓取器
	//enabledScrapers = append(enabledScrapers, interfaces.ScraperTypeSteamBuy)  // Steam买入数据抓取
	//enabledScrapers = append(enabledScrapers, interfaces.ScraperTypeSteamSell) // Steam卖出数据抓取

	return enabledScrapers
}

// ========================================================

func main() {
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
	scraperFactory := factory.NewScraperFactory(managers.dao)
	scraperFactory.SetManagers(
		managers.httpManager,
		managers.proxyManager,
		managers.configManager,
		managers.cacheManager,
		managers.errorHandler,
		managers.taskManager,
	)

	scraperManager := factory.NewScraperManager(scraperFactory)

	// 获取启用的抓取器类型
	enabledScrapers := getEnabledScrapers()

	// 显示启用的抓取器
	fmt.Printf("启用的抓取器: ")
	for i, scraperType := range enabledScrapers {
		if i > 0 {
			fmt.Printf(", ")
		}
		fmt.Printf("%s", scraperType)
	}
	fmt.Printf("\n")

	// 启动抓取器
	for _, scraperType := range enabledScrapers {
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
	httpManager   interfaces.IHTTPClientManager
	proxyManager  interfaces.IProxyManager
	configManager interfaces.IConfigManager
	cacheManager  interfaces.ICacheManager
	errorHandler  interfaces.IErrorHandler
	taskManager   interfaces.ITaskManager
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
	ProxyManagerConfig := proxy.ProxyManagerConfig{
		MaxFailCount:    5,
		HealthCheckURL:  "",
		CheckInterval:   5 * time.Minute,  // 每5分钟检查一次代理健康状态
		RefreshInterval: 10 * time.Minute, // 每10分钟刷新一次代理池
		MaxUsageTime:    30 * time.Minute, // 代理最大使用时间30分钟
	}
	// 创建各种管理器
	proxyManager := proxy.NewProxyManager(&ProxyManagerConfig)
	errorHandler := errorhandler.NewErrorHandler()
	httpManager := client.NewHTTPClientManager(proxyManager, errorHandler)
	configManager := config.NewConfigManager()
	cacheManager := cache.NewCacheManager("scraper")
	taskManager := task.NewTaskManager()

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

// waitForSignal 等待信号
func waitForSignal(scraperManager *factory.ScraperManager) {
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
