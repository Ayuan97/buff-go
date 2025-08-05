package steam

import (
	"buff-go/internal/scraper/core/base"
	"buff-go/internal/scraper/interfaces"
	"context"
	"fmt"
	"time"
)

// SteamSellScraper Steam卖出抓取器
type SteamSellScraper struct {
	*base.BaseScraper
}

// NewSteamSellScraper 创建Steam卖出抓取器
func NewSteamSellScraper() *SteamSellScraper {
	baseScraper := base.NewBaseScraper("steam_sell")
	return &SteamSellScraper{
		BaseScraper: baseScraper,
	}
}

// Initialize 初始化抓取器
func (ss *SteamSellScraper) Initialize(config *interfaces.ScraperConfig) error {
	if err := ss.BaseScraper.Initialize(config); err != nil {
		return err
	}

	// 可以在这里设置特定的配置
	return nil
}

// SetManagers 设置管理器
func (ss *SteamSellScraper) SetManagers(
	httpManager interfaces.IHTTPClientManager,
	proxyManager interfaces.IProxyManager,
	configManager interfaces.IConfigManager,
	cacheManager interfaces.ICacheManager,
	errorHandler interfaces.IErrorHandler,
	taskManager interfaces.ITaskManager,
) {
	ss.BaseScraper.SetManagers(httpManager, proxyManager, configManager, cacheManager, errorHandler, taskManager)
}

// Start 开始抓取
func (ss *SteamSellScraper) Start(ctx context.Context) error {
	if err := ss.BaseScraper.Start(ctx); err != nil {
		return err
	}

	// 启动特定的抓取逻辑
	go ss.startScraping()
	return nil
}

// ProcessTask 处理抓取任务
func (ss *SteamSellScraper) ProcessTask(task *interfaces.ScrapingTask) (*interfaces.ScrapingResult, error) {
	// 执行HTTP请求
	result, err := ss.BaseScraper.ProcessTask(task)
	if err != nil {
		return result, err
	}

	// 处理响应数据
	if err := ss.processResponse(result); err != nil {
		result.Error = err
		return result, err
	}

	return result, nil
}

// startScraping 开始抓取
func (ss *SteamSellScraper) startScraping() {
	ticker := time.NewTicker(60 * time.Second) // Steam抓取间隔更长
	defer ticker.Stop()

	for {
		select {
		case <-ss.BaseScraper.Ctx.Done():
			return
		case <-ticker.C:
			ss.scrapeData()
		}
	}
}

// scrapeData 抓取数据
func (ss *SteamSellScraper) scrapeData() {
	// Steam市场API URL
	url := "https://steamcommunity.com/market/search/render/?query=&start=0&count=100&search_descriptions=0&sort_column=price&sort_dir=asc&appid=730"

	task := &interfaces.ScrapingTask{
		ID:     fmt.Sprintf("steam_sell_%d", time.Now().Unix()),
		URL:    url,
		Method: "GET",
		Headers: map[string]string{
			"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
			"Referer":    "https://steamcommunity.com/market/",
		},
		CreatedAt: time.Now(),
	}

	if err := ss.TaskManager.AddTask(task); err != nil {
		fmt.Printf("添加Steam卖出任务失败: %v\n", err)
	}
}

// processResponse 处理响应数据
func (ss *SteamSellScraper) processResponse(result *interfaces.ScrapingResult) error {
	// 这里可以实现Steam数据的具体处理逻辑
	// 由于Steam API的复杂性，这里只是一个基础框架
	fmt.Printf("Steam卖出数据处理: 状态码=%d, 数据长度=%d\n", result.StatusCode, len(result.Body))
	return nil
}
