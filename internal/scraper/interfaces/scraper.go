package interfaces

import (
	"context"
)

// IScraper 抓取器接口
type IScraper interface {
	// Initialize 初始化抓取器
	Initialize(config *ScraperConfig) error

	// Start 开始抓取
	Start(ctx context.Context) error

	// Stop 停止抓取
	Stop() error

	// GetStatus 获取抓取状态
	GetStatus() ScraperStatus

	// ProcessTask 处理单个抓取任务
	ProcessTask(task *ScrapingTask) (*ScrapingResult, error)

	// GetName 获取抓取器名称
	GetName() string
}

// ScraperType 抓取器类型
type ScraperType string

const (
	ScraperTypeBuffBuy   ScraperType = "buff_buy"
	ScraperTypeBuffSell  ScraperType = "buff_sell"
	ScraperTypeSteamBuy  ScraperType = "steam_buy"
	ScraperTypeSteamSell ScraperType = "steam_sell"
)

// IScraperFactory 抓取器工厂接口
type IScraperFactory interface {
	// CreateScraper 创建抓取器
	CreateScraper(scraperType ScraperType) (IScraper, error)

	// GetAvailableScraperTypes 获取可用的抓取器类型
	GetAvailableScraperTypes() []ScraperType

	// ValidateScraperType 验证抓取器类型
	ValidateScraperType(scraperType ScraperType) bool
}

// IScraperManager 抓取器管理器接口
type IScraperManager interface {
	// StartScraper 启动抓取器
	StartScraper(scraperType ScraperType) error

	// StopScraper 停止抓取器
	StopScraper(scraperType ScraperType) error

	// GetScraperStatus 获取抓取器状态
	GetScraperStatus(scraperType ScraperType) (ScraperStatus, error)

	// GetAllScraperStatus 获取所有抓取器状态
	GetAllScraperStatus() map[ScraperType]ScraperStatus

	// StopAllScrapers 停止所有抓取器
	StopAllScrapers() error
}
