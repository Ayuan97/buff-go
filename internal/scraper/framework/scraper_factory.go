package framework

import (
	"buff-go/internal/dao"
	"context"
	"fmt"
	"time"
)

// ScraperType 抓取器类型
type ScraperType string

const (
	ScraperTypeBuffBuy   ScraperType = "buff_buy"
	ScraperTypeBuffSell  ScraperType = "buff_sell"
	ScraperTypeSteamBuy  ScraperType = "steam_buy"
	ScraperTypeSteamSell ScraperType = "steam_sell"
)

// ScraperFactory 抓取器工厂
type ScraperFactory struct {
	dao           *dao.Dao
	httpManager   IHTTPClientManager
	proxyManager  IProxyManager
	configManager IConfigManager
	cacheManager  ICacheManager
	errorHandler  IErrorHandler
	taskManager   ITaskManager
}

// NewScraperFactory 创建抓取器工厂
func NewScraperFactory(dao *dao.Dao) *ScraperFactory {
	return &ScraperFactory{
		dao: dao,
	}
}

// SetManagers 设置管理器
func (sf *ScraperFactory) SetManagers(
	httpManager IHTTPClientManager,
	proxyManager IProxyManager,
	configManager IConfigManager,
	cacheManager ICacheManager,
	errorHandler IErrorHandler,
	taskManager ITaskManager,
) {
	sf.httpManager = httpManager
	sf.proxyManager = proxyManager
	sf.configManager = configManager
	sf.cacheManager = cacheManager
	sf.errorHandler = errorHandler
	sf.taskManager = taskManager
}

// CreateScraper 创建抓取器
func (sf *ScraperFactory) CreateScraper(scraperType ScraperType) (IScraper, error) {
	var scraper IScraper
	var err error

	switch scraperType {
	case ScraperTypeBuffBuy:
		scraper, err = sf.createBuffBuyScraper()
	case ScraperTypeBuffSell:
		scraper, err = sf.createBuffSellScraper()
	case ScraperTypeSteamBuy:
		scraper, err = sf.createSteamBuyScraper()
	case ScraperTypeSteamSell:
		scraper, err = sf.createSteamSellScraper()
	default:
		return nil, fmt.Errorf("unknown scraper type: %s", scraperType)
	}

	if err != nil {
		return nil, err
	}

	// 设置管理器（通过接口设置）
	if manageable, ok := scraper.(interface {
		SetManagers(IHTTPClientManager, IProxyManager, IConfigManager, ICacheManager, IErrorHandler, ITaskManager)
	}); ok {
		manageable.SetManagers(
			sf.httpManager,
			sf.proxyManager,
			sf.configManager,
			sf.cacheManager,
			sf.errorHandler,
			sf.taskManager,
		)
	}

	return scraper, nil
}

// createBuffBuyScraper 创建Buff买入抓取器
func (sf *ScraperFactory) createBuffBuyScraper() (IScraper, error) {
	scraper := NewBuffBuyScraper(sf.dao)

	// 获取配置
	var config *ScraperConfig
	var err error
	if configMgr, ok := sf.configManager.(*ConfigManager); ok {
		config, err = configMgr.GetScraperConfig("buff_buy")
		if err != nil {
			return nil, fmt.Errorf("failed to get buff buy config: %v", err)
		}
	} else {
		// 使用默认配置
		config = &ScraperConfig{
			Name:           "buff_buy",
			MaxConcurrency: 5,
			RequestDelay:   time.Second,
			RetryCount:     3,
			Timeout:        30 * time.Second,
			UseProxy:       true,
			EnableCache:    true,
			CacheTTL:       5 * time.Minute,
		}
	}

	// 初始化抓取器
	if err := scraper.Initialize(config); err != nil {
		return nil, fmt.Errorf("failed to initialize buff buy scraper: %v", err)
	}

	return scraper, nil
}

// createBuffSellScraper 创建Buff卖出抓取器
func (sf *ScraperFactory) createBuffSellScraper() (IScraper, error) {
	// TODO: 实现Buff卖出抓取器
	return nil, fmt.Errorf("buff sell scraper not implemented yet")
}

// createSteamBuyScraper 创建Steam买入抓取器
func (sf *ScraperFactory) createSteamBuyScraper() (IScraper, error) {
	baseScraper := NewBaseScraper("steam_buy")

	config := &ScraperConfig{
		Name:           "steam_buy",
		MaxConcurrency: 3,
		RequestDelay:   2 * time.Second,
		RetryCount:     3,
		Timeout:        30 * time.Second,
		UseProxy:       true,
		EnableCache:    true,
		CacheTTL:       10 * time.Minute,
	}

	if err := baseScraper.Initialize(config); err != nil {
		return nil, fmt.Errorf("failed to initialize steam buy scraper: %v", err)
	}

	return baseScraper, nil
}

// createSteamSellScraper 创建Steam卖出抓取器
func (sf *ScraperFactory) createSteamSellScraper() (IScraper, error) {
	baseScraper := NewBaseScraper("steam_sell")

	config := &ScraperConfig{
		Name:           "steam_sell",
		MaxConcurrency: 3,
		RequestDelay:   2 * time.Second,
		RetryCount:     3,
		Timeout:        30 * time.Second,
		UseProxy:       true,
		EnableCache:    true,
		CacheTTL:       10 * time.Minute,
	}

	if err := baseScraper.Initialize(config); err != nil {
		return nil, fmt.Errorf("failed to initialize steam sell scraper: %v", err)
	}

	return baseScraper, nil
}

// GetAvailableScraperTypes 获取可用的抓取器类型
func (sf *ScraperFactory) GetAvailableScraperTypes() []ScraperType {
	return []ScraperType{
		ScraperTypeBuffBuy,
		ScraperTypeBuffSell,
		ScraperTypeSteamBuy,
		ScraperTypeSteamSell,
	}
}

// ValidateScraperType 验证抓取器类型
func (sf *ScraperFactory) ValidateScraperType(scraperType ScraperType) bool {
	availableTypes := sf.GetAvailableScraperTypes()
	for _, t := range availableTypes {
		if t == scraperType {
			return true
		}
	}
	return false
}

// ScraperManager 抓取器管理器
type ScraperManager struct {
	factory  *ScraperFactory
	scrapers map[ScraperType]IScraper
}

// NewScraperManager 创建抓取器管理器
func NewScraperManager(factory *ScraperFactory) *ScraperManager {
	return &ScraperManager{
		factory:  factory,
		scrapers: make(map[ScraperType]IScraper),
	}
}

// StartScraper 启动抓取器
func (sm *ScraperManager) StartScraper(scraperType ScraperType) error {
	// 检查抓取器是否已经在运行
	if scraper, exists := sm.scrapers[scraperType]; exists {
		if scraper.GetStatus() == StatusRunning {
			return fmt.Errorf("scraper %s is already running", scraperType)
		}
	}

	// 创建抓取器
	scraper, err := sm.factory.CreateScraper(scraperType)
	if err != nil {
		return fmt.Errorf("failed to create scraper %s: %v", scraperType, err)
	}

	// 启动抓取器
	if err := scraper.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start scraper %s: %v", scraperType, err)
	}

	sm.scrapers[scraperType] = scraper
	return nil
}

// StopScraper 停止抓取器
func (sm *ScraperManager) StopScraper(scraperType ScraperType) error {
	scraper, exists := sm.scrapers[scraperType]
	if !exists {
		return fmt.Errorf("scraper %s not found", scraperType)
	}

	if err := scraper.Stop(); err != nil {
		return fmt.Errorf("failed to stop scraper %s: %v", scraperType, err)
	}

	delete(sm.scrapers, scraperType)
	return nil
}

// GetScraperStatus 获取抓取器状态
func (sm *ScraperManager) GetScraperStatus(scraperType ScraperType) (ScraperStatus, error) {
	scraper, exists := sm.scrapers[scraperType]
	if !exists {
		return StatusStopped, nil
	}

	return scraper.GetStatus(), nil
}

// GetAllScraperStatus 获取所有抓取器状态
func (sm *ScraperManager) GetAllScraperStatus() map[ScraperType]ScraperStatus {
	status := make(map[ScraperType]ScraperStatus)

	for scraperType, scraper := range sm.scrapers {
		status[scraperType] = scraper.GetStatus()
	}

	return status
}

// StopAllScrapers 停止所有抓取器
func (sm *ScraperManager) StopAllScrapers() error {
	var errors []error

	for scraperType := range sm.scrapers {
		if err := sm.StopScraper(scraperType); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to stop some scrapers: %v", errors)
	}

	return nil
}
