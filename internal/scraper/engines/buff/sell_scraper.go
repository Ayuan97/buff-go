package buff

import (
	"buff-go/internal/dao"
	"buff-go/internal/scraper/core/base"
	"buff-go/internal/scraper/interfaces"
	"buff-go/pkg/gredis"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
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
	*base.BaseScraper
	dao    *dao.Dao
	config *BuffSellConfig
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
func NewBuffSellScraper(dao *dao.Dao) *BuffSellScraper {
	baseScraper := base.NewBaseScraper("buff_sell")
	return &BuffSellScraper{
		BaseScraper: baseScraper,
		dao:         dao,
		config: &BuffSellConfig{
			Game:     "csgo",
			AppID:    730,
			MinPrice: 0.01,
			MaxPrice: 1000.0,
			PageNum:  10,
		},
	}
}

// Initialize 初始化抓取器
func (bs *BuffSellScraper) Initialize(config *interfaces.ScraperConfig) error {
	if err := bs.BaseScraper.Initialize(config); err != nil {
		return err
	}

	// 可以在这里设置特定的配置
	return nil
}

// SetManagers 设置管理器
func (bs *BuffSellScraper) SetManagers(
	httpManager interfaces.IHTTPClientManager,
	proxyManager interfaces.IProxyManager,
	configManager interfaces.IConfigManager,
	cacheManager interfaces.ICacheManager,
	errorHandler interfaces.IErrorHandler,
	taskManager interfaces.ITaskManager,
) {
	bs.BaseScraper.SetManagers(httpManager, proxyManager, configManager, cacheManager, errorHandler, taskManager)
}

// Start 开始抓取
func (bs *BuffSellScraper) Start(ctx context.Context) error {
	if err := bs.BaseScraper.Start(ctx); err != nil {
		return err
	}

	// 启动特定的抓取逻辑
	go bs.startScraping()
	return nil
}

// ProcessTask 处理抓取任务
func (bs *BuffSellScraper) ProcessTask(task *interfaces.ScrapingTask) (*interfaces.ScrapingResult, error) {
	// 执行HTTP请求
	result, err := bs.BaseScraper.ProcessTask(task)
	if err != nil {
		return result, err
	}

	// 处理响应数据
	if err := bs.processResponse(result); err != nil {
		result.Error = err
		return result, err
	}

	return result, nil
}

// startScraping 开始抓取
func (bs *BuffSellScraper) startScraping() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-bs.BaseScraper.Ctx.Done():
			return
		case <-ticker.C:
			bs.scrapePages()
		}
	}
}

// scrapePages 抓取页面
func (bs *BuffSellScraper) scrapePages() {
	for i := 1; i <= bs.config.PageNum; i++ {
		url := fmt.Sprintf("https://buff.163.com/api/market/goods/selling?game=%s&page_num=%d&min_price=%.2f&max_price=%.2f&sort_by=price.asc&page_size=80&use_suggestion=0&_=%d",
			bs.config.Game, i, bs.config.MinPrice, bs.config.MaxPrice, time.Now().UnixNano()/1e6)

		task := &interfaces.ScrapingTask{
			ID:     fmt.Sprintf("buff_sell_page_%d", i),
			URL:    url,
			Method: "GET",
			Headers: map[string]string{
				"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
				"Referer":    "https://buff.163.com/",
			},
			CreatedAt: time.Now(),
		}

		if err := bs.TaskManager.AddTask(task); err != nil {
			fmt.Printf("添加任务失败: %v\n", err)
		}

		// 页面间延迟
		time.Sleep(2 * time.Second)
	}
}

// processResponse 处理响应数据
func (bs *BuffSellScraper) processResponse(result *interfaces.ScrapingResult) error {
	if result.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error: %d", result.StatusCode)
	}

	var buffData BuffSellData
	if err := json.Unmarshal(result.Body, &buffData); err != nil {
		return fmt.Errorf("JSON解析失败: %v", err)
	}

	if buffData.Code != "OK" {
		return fmt.Errorf("API返回错误: %s", buffData.Code)
	}

	// 处理商品数据
	return bs.processItems(buffData.Result.Items)
}

// processItems 处理商品数据
func (bs *BuffSellScraper) processItems(items []BuffSellItem) error {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // 限制并发数

	for _, item := range items {
		wg.Add(1)
		go func(item BuffSellItem) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if err := bs.processItem(item); err != nil {
				fmt.Printf("处理商品失败 %s: %v\n", item.Name, err)
			}
		}(item)
	}

	wg.Wait()
	return nil
}

// processItem 处理单个商品
func (bs *BuffSellScraper) processItem(item BuffSellItem) error {
	// 转换价格
	price, err := strconv.ParseFloat(item.SellMinPrice, 64)
	if err != nil {
		return fmt.Errorf("价格转换失败: %v", err)
	}

	// 创建商品数据（简化实现）
	itemData := map[string]interface{}{
		"item_id":          item.Id,
		"name":             item.Name,
		"market_hash_name": item.MarketHashName,
		"sell_min_price":   price,
		"sell_num":         item.SellNum,
		"appid":            item.Appid,
		"created_on":       time.Now().Unix(),
		"modified_on":      time.Now().Unix(),
	}

	// 这里可以实现具体的数据库保存逻辑
	_ = itemData // 暂时忽略，避免编译错误

	// 缓存到Redis
	cacheKey := fmt.Sprintf("buff_sell:%d", item.Id)
	if data, err := json.Marshal(itemData); err == nil {
		gredis.Set(cacheKey, string(data), 10*time.Minute)
	}

	return nil
}
