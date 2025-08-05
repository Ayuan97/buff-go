package implementations

import (
	"buff-go/internal/dao"
	"buff-go/internal/scraper/business"
	"buff-go/internal/scraper/framework"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// BuffSellData Buff卖出数据结构
type BuffSellData struct {
	Code   string `json:"code"`
	Result struct {
		Items []BuffSellItem `json:"items"`
	} `json:"data"`
}

// BuffSellItem Buff卖出商品项
type BuffSellItem struct {
	Id             int    `json:"id"`
	Name           string `json:"name"`
	MarketHashName string `json:"market_hash_name"`
	SellMinPrice   string `json:"sell_min_price"`
	SellNum        int    `json:"sell_num"`
	Appid          int    `json:"appid"`
}

// BuffSellScraper Buff卖出抓取器
type BuffSellScraper struct {
	*framework.BaseScraper
	dao           *dao.Dao
	dataProcessor *business.DataProcessor
	config        *BuffSellConfig
}

// BuffSellConfig Buff卖出抓取器配置
type BuffSellConfig struct {
	Game     string  `json:"game"`
	AppID    int     `json:"app_id"`
	MinPrice float64 `json:"min_price"`
	MaxPrice float64 `json:"max_price"`
	PageNum  int     `json:"page_num"`
}

// NewBuffSellScraper 创建Buff卖出抓取器
func NewBuffSellScraper(dao *dao.Dao, dataProcessor *business.DataProcessor) *BuffSellScraper {
	baseScraper := framework.NewBaseScraper("buff_sell")

	return &BuffSellScraper{
		BaseScraper:   baseScraper,
		dao:           dao,
		dataProcessor: dataProcessor,
	}
}

// Initialize 初始化抓取器
func (bs *BuffSellScraper) Initialize(config *framework.ScraperConfig) error {
	if err := bs.BaseScraper.Initialize(config); err != nil {
		return err
	}

	// 获取Buff卖出特定配置
	bs.config = &BuffSellConfig{
		Game:     "csgo",
		AppID:    730,
		MinPrice: 0,
		MaxPrice: 10000,
		PageNum:  10,
	}

	// 从配置管理器获取配置
	if bs.ConfigManager != nil {
		if minPrice, err := bs.ConfigManager.GetFloat64("min.price"); err == nil {
			bs.config.MinPrice = minPrice
		}
		if maxPrice, err := bs.ConfigManager.GetFloat64("max.price"); err == nil {
			bs.config.MaxPrice = maxPrice
		}
		if pageNum, err := bs.ConfigManager.GetInt("buff.page.num"); err == nil {
			bs.config.PageNum = pageNum
		}
	}

	return nil
}

// Start 开始抓取
func (bs *BuffSellScraper) Start(ctx context.Context) error {
	// 检查配置状态
	if bs.ConfigManager != nil {
		if status, err := bs.ConfigManager.GetInt("buff.sell.status"); err == nil && status == 0 {
			return fmt.Errorf("buff sell scraper is disabled")
		}
	}

	// 生成抓取任务
	if err := bs.generateTasks(); err != nil {
		return fmt.Errorf("failed to generate tasks: %v", err)
	}

	// 启动基础抓取器
	return bs.BaseScraper.Start(ctx)
}

// ProcessTask 处理抓取任务
func (bs *BuffSellScraper) ProcessTask(task *framework.ScrapingTask) (*framework.ScrapingResult, error) {
	// 执行HTTP请求
	result, err := bs.BaseScraper.ProcessTask(task)
	if err != nil {
		return result, err
	}

	// 解析响应数据
	if err := bs.processResponse(result); err != nil {
		result.Error = err
		return result, err
	}

	return result, nil
}

// generateTasks 生成抓取任务
func (bs *BuffSellScraper) generateTasks() error {
	for i := 1; i <= bs.config.PageNum; i++ {
		url := fmt.Sprintf("https://buff.163.com/api/market/goods/selling?game=%s&page_num=%d&min_price=%.2f&max_price=%.2f&sort_by=price.asc&page_size=80&use_suggestion=0&_=%d",
			bs.config.Game, i, bs.config.MinPrice, bs.config.MaxPrice, time.Now().UnixNano()/1e6)

		task := &framework.ScrapingTask{
			ID:     fmt.Sprintf("buff_sell_page_%d", i),
			URL:    url,
			Method: "GET",
			Headers: map[string]string{
				"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/105.0.0.0 Safari/537.36",
			},
			Priority:   1,
			RetryCount: 3,
			CreatedAt:  time.Now(),
			Metadata: map[string]interface{}{
				"page":   i,
				"game":   bs.config.Game,
				"app_id": bs.config.AppID,
			},
		}

		if err := bs.TaskManager.AddTask(task); err != nil {
			return fmt.Errorf("failed to add task for page %d: %v", i, err)
		}
	}

	return nil
}

// processResponse 处理响应数据
func (bs *BuffSellScraper) processResponse(result *framework.ScrapingResult) error {
	if result.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error: %d", result.StatusCode)
	}

	// 解析JSON数据
	var buffData BuffSellData
	if err := json.Unmarshal(result.Body, &buffData); err != nil {
		return fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// 检查响应状态
	switch buffData.Code {
	case "OK":
		return bs.handleBuffData(buffData, result.Metadata)
	case "Action Forbidden":
		return fmt.Errorf("action forbidden")
	case "Login Required":
		return fmt.Errorf("login required")
	default:
		return fmt.Errorf("unknown response code: %s", buffData.Code)
	}
}

// handleBuffData 处理Buff数据
func (bs *BuffSellScraper) handleBuffData(buffData BuffSellData, metadata map[string]interface{}) error {
	game := bs.config.Game
	appid := bs.config.AppID

	if gameStr, ok := metadata["game"].(string); ok {
		game = gameStr
	}
	if appidInt, ok := metadata["app_id"].(int); ok {
		appid = appidInt
	}

	// 转换数据格式
	items := make([]business.BuffSellItem, len(buffData.Result.Items))
	for i, item := range buffData.Result.Items {
		items[i] = business.BuffSellItem{
			Id:             item.Id,
			Name:           item.Name,
			MarketHashName: item.MarketHashName,
			SellMinPrice:   item.SellMinPrice,
			SellNum:        item.SellNum,
			Appid:          item.Appid,
		}

		fmt.Printf("buff - sell - name: %s | 价格: %s | 数量: %d\n", item.Name, item.SellMinPrice, item.SellNum)
	}

	// 使用数据处理器处理数据
	if bs.dataProcessor != nil {
		result := bs.dataProcessor.ProcessBuffSellData(items, game, appid)
		if !result.Success {
			return fmt.Errorf("data processing failed: %v", result.Errors)
		}
	}

	return nil
}

// GetConfig 获取配置
func (bs *BuffSellScraper) GetConfig() *BuffSellConfig {
	return bs.config
}

// UpdateConfig 更新配置
func (bs *BuffSellScraper) UpdateConfig(config *BuffSellConfig) {
	bs.config = config
}

// SetManagers 设置管理器
func (bs *BuffSellScraper) SetManagers(
	httpManager framework.IHTTPClientManager,
	proxyManager framework.IProxyManager,
	configManager framework.IConfigManager,
	cacheManager framework.ICacheManager,
	errorHandler framework.IErrorHandler,
	taskManager framework.ITaskManager,
) {
	bs.BaseScraper.SetManagers(httpManager, proxyManager, configManager, cacheManager, errorHandler, taskManager)
}

// SetDataProcessor 设置数据处理器
func (bs *BuffSellScraper) SetDataProcessor(processor *business.DataProcessor) {
	bs.dataProcessor = processor
}
