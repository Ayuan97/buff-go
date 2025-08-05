package factory

import (
	"buff-go/internal/dao"
	"buff-go/internal/scraper/engines/buff"
	"buff-go/internal/scraper/engines/steam"
	"buff-go/internal/scraper/interfaces"
	"context"
	"fmt"
	"time"
)

// ScraperFactory 抓取器工厂
type ScraperFactory struct {
	dao           *dao.Dao
	httpManager   interfaces.IHTTPClientManager
	proxyManager  interfaces.IProxyManager
	configManager interfaces.IConfigManager
	cacheManager  interfaces.ICacheManager
	errorHandler  interfaces.IErrorHandler
	taskManager   interfaces.ITaskManager
}

// NewScraperFactory 创建抓取器工厂
func NewScraperFactory(dao *dao.Dao) *ScraperFactory {
	return &ScraperFactory{
		dao: dao,
	}
}

// SetManagers 设置管理器
func (sf *ScraperFactory) SetManagers(
	httpManager interfaces.IHTTPClientManager,
	proxyManager interfaces.IProxyManager,
	configManager interfaces.IConfigManager,
	cacheManager interfaces.ICacheManager,
	errorHandler interfaces.IErrorHandler,
	taskManager interfaces.ITaskManager,
) {
	sf.httpManager = httpManager
	sf.proxyManager = proxyManager
	sf.configManager = configManager
	sf.cacheManager = cacheManager
	sf.errorHandler = errorHandler
	sf.taskManager = taskManager
}

// CreateScraper 创建抓取器
func (sf *ScraperFactory) CreateScraper(scraperType interfaces.ScraperType) (interfaces.IScraper, error) {
	var scraper interfaces.IScraper
	var err error

	switch scraperType {
	case interfaces.ScraperTypeBuffBuy:
		scraper, err = sf.createBuffBuyScraper()
	case interfaces.ScraperTypeBuffSell:
		scraper, err = sf.createBuffSellScraper()
	case interfaces.ScraperTypeSteamBuy:
		scraper, err = sf.createSteamBuyScraper()
	case interfaces.ScraperTypeSteamSell:
		scraper, err = sf.createSteamSellScraper()
	default:
		return nil, fmt.Errorf("unknown scraper type: %s", scraperType)
	}

	if err != nil {
		return nil, err
	}

	// 设置管理器（通过接口设置）
	if manageable, ok := scraper.(interface {
		SetManagers(interfaces.IHTTPClientManager, interfaces.IProxyManager, interfaces.IConfigManager, interfaces.ICacheManager, interfaces.IErrorHandler, interfaces.ITaskManager)
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
func (sf *ScraperFactory) createBuffBuyScraper() (interfaces.IScraper, error) {
	scraper := buff.NewBuffBuyScraper(sf.dao)

	// 获取配置
	var config *interfaces.ScraperConfig
	// 使用默认配置
	config = &interfaces.ScraperConfig{
		Name:           "buff_buy",
		MaxConcurrency: 5,
		RequestDelay:   time.Second,
		RetryCount:     3,
		Timeout:        30 * time.Second,
		UseProxy:       true,
		EnableCache:    true,
		CacheTTL:       5 * time.Minute,
	}

	// 初始化抓取器
	if err := scraper.Initialize(config); err != nil {
		return nil, fmt.Errorf("failed to initialize buff buy scraper: %v", err)
	}

	// 设置管理器
	if sf.httpManager != nil && sf.proxyManager != nil && sf.configManager != nil &&
		sf.cacheManager != nil && sf.errorHandler != nil && sf.taskManager != nil {
		scraper.SetManagers(sf.httpManager, sf.proxyManager, sf.configManager,
			sf.cacheManager, sf.errorHandler, sf.taskManager)
	}

	return scraper, nil
}

// createBuffSellScraper 创建Buff卖出抓取器
func (sf *ScraperFactory) createBuffSellScraper() (interfaces.IScraper, error) {
	scraper := buff.NewBuffSellScraper(sf.dao)

	// 获取配置
	var config *interfaces.ScraperConfig
	// 使用默认配置
	config = &interfaces.ScraperConfig{
		Name:           "buff_sell",
		MaxConcurrency: 3,
		RequestDelay:   2 * time.Second,
		RetryCount:     3,
		Timeout:        30 * time.Second,
		UseProxy:       true,
		EnableCache:    true,
		CacheTTL:       10 * time.Minute,
	}

	// 初始化抓取器
	if err := scraper.Initialize(config); err != nil {
		return nil, fmt.Errorf("failed to initialize buff sell scraper: %v", err)
	}

	return scraper, nil
}

// createSteamBuyScraper 创建Steam买入抓取器
func (sf *ScraperFactory) createSteamBuyScraper() (interfaces.IScraper, error) {
	scraper := steam.NewSteamBuyScraper()

	config := &interfaces.ScraperConfig{
		Name:           "steam_buy",
		MaxConcurrency: 3,
		RequestDelay:   2 * time.Second,
		RetryCount:     3,
		Timeout:        30 * time.Second,
		UseProxy:       true,
		EnableCache:    true,
		CacheTTL:       10 * time.Minute,
	}

	if err := scraper.Initialize(config); err != nil {
		return nil, fmt.Errorf("failed to initialize steam buy scraper: %v", err)
	}

	return scraper, nil
}

// createSteamSellScraper 创建Steam卖出抓取器
func (sf *ScraperFactory) createSteamSellScraper() (interfaces.IScraper, error) {
	scraper := steam.NewSteamSellScraper()

	config := &interfaces.ScraperConfig{
		Name:           "steam_sell",
		MaxConcurrency: 3,
		RequestDelay:   2 * time.Second,
		RetryCount:     3,
		Timeout:        30 * time.Second,
		UseProxy:       true,
		EnableCache:    true,
		CacheTTL:       10 * time.Minute,
	}

	if err := scraper.Initialize(config); err != nil {
		return nil, fmt.Errorf("failed to initialize steam sell scraper: %v", err)
	}

	return scraper, nil
}

// GetAvailableScraperTypes 获取可用的抓取器类型
func (sf *ScraperFactory) GetAvailableScraperTypes() []interfaces.ScraperType {
	return []interfaces.ScraperType{
		interfaces.ScraperTypeBuffBuy,
		interfaces.ScraperTypeBuffSell,
		interfaces.ScraperTypeSteamBuy,
		interfaces.ScraperTypeSteamSell,
	}
}

// ValidateScraperType 验证抓取器类型
func (sf *ScraperFactory) ValidateScraperType(scraperType interfaces.ScraperType) bool {
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
	scrapers map[interfaces.ScraperType]interfaces.IScraper
}

// NewScraperManager 创建抓取器管理器
func NewScraperManager(factory *ScraperFactory) *ScraperManager {
	return &ScraperManager{
		factory:  factory,
		scrapers: make(map[interfaces.ScraperType]interfaces.IScraper),
	}
}

// StartScraper 启动抓取器
func (sm *ScraperManager) StartScraper(scraperType interfaces.ScraperType) error {
	// 检查抓取器是否已经在运行
	if scraper, exists := sm.scrapers[scraperType]; exists {
		if scraper.GetStatus() == interfaces.StatusRunning {
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
func (sm *ScraperManager) StopScraper(scraperType interfaces.ScraperType) error {
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
func (sm *ScraperManager) GetScraperStatus(scraperType interfaces.ScraperType) (interfaces.ScraperStatus, error) {
	scraper, exists := sm.scrapers[scraperType]
	if !exists {
		return interfaces.StatusStopped, nil
	}

	return scraper.GetStatus(), nil
}

// GetAllScraperStatus 获取所有抓取器状态
func (sm *ScraperManager) GetAllScraperStatus() map[interfaces.ScraperType]interfaces.ScraperStatus {
	status := make(map[interfaces.ScraperType]interfaces.ScraperStatus)

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
