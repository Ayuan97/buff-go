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

// BuffBuyData Buff买入数据结构
type BuffBuyData struct {
	Code   string `json:"code"`
	Result struct {
		Items []BuffBuyItem `json:"items"`
	} `json:"data"`
}

// BuffBuyItem Buff买入商品项
type BuffBuyItem struct {
	Id             int    `json:"id"`
	Name           string `json:"name"`
	MarketHashName string `json:"market_hash_name"`
	BuyMaxPrice    string `json:"buy_max_price"`
	BuyNum         int    `json:"buy_num"`
	Appid          int    `json:"appid"`
}

// BuffBuyScraper Buff买入抓取器
type BuffBuyScraper struct {
	*base.BaseScraper
	dao    *dao.Dao
	config *BuffBuyConfig
}

// BuffBuyConfig Buff买入抓取器配置
type BuffBuyConfig struct {
	Game     string  `json:"game"`
	AppID    int     `json:"app_id"`
	MinPrice float64 `json:"min_price"`
	MaxPrice float64 `json:"max_price"`
	PageNum  int     `json:"page_num"`
}

// NewBuffBuyScraper 创建Buff买入抓取器
func NewBuffBuyScraper(dao *dao.Dao) *BuffBuyScraper {
	baseScraper := base.NewBaseScraper("buff_buy")
	return &BuffBuyScraper{
		BaseScraper: baseScraper,
		dao:         dao,
		config: &BuffBuyConfig{
			Game:     "csgo",
			AppID:    730,
			MinPrice: 0.01,
			MaxPrice: 1000.0,
			PageNum:  10,
		},
	}
}

// Initialize 初始化抓取器
func (bs *BuffBuyScraper) Initialize(config *interfaces.ScraperConfig) error {
	if err := bs.BaseScraper.Initialize(config); err != nil {
		return err
	}

	// 可以在这里设置特定的配置
	return nil
}

// SetManagers 设置管理器
func (bs *BuffBuyScraper) SetManagers(
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
func (bs *BuffBuyScraper) Start(ctx context.Context) error {
	if err := bs.BaseScraper.Start(ctx); err != nil {
		return err
	}

	// 启动特定的抓取逻辑
	go bs.startScraping()
	return nil
}

// ProcessTask 处理抓取任务
func (bs *BuffBuyScraper) ProcessTask(task *interfaces.ScrapingTask) (*interfaces.ScrapingResult, error) {
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
func (bs *BuffBuyScraper) startScraping() {
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
func (bs *BuffBuyScraper) scrapePages() {
	for i := 1; i <= bs.config.PageNum; i++ {
		url := fmt.Sprintf("https://buff.163.com/api/market/goods/buying?game=%s&page_num=%d&min_price=%.2f&max_price=%.2f&sort_by=price.desc&page_size=80&use_suggestion=0&_=%d",
			bs.config.Game, i, bs.config.MinPrice, bs.config.MaxPrice, time.Now().UnixNano()/1e6)

		task := &interfaces.ScrapingTask{
			ID:     fmt.Sprintf("buff_buy_page_%d", i),
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
func (bs *BuffBuyScraper) processResponse(result *interfaces.ScrapingResult) error {
	if result.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error: %d", result.StatusCode)
	}

	var buffData BuffBuyData
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
func (bs *BuffBuyScraper) processItems(items []BuffBuyItem) error {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // 限制并发数

	for _, item := range items {
		wg.Add(1)
		go func(item BuffBuyItem) {
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
func (bs *BuffBuyScraper) processItem(item BuffBuyItem) error {
	// 转换价格
	price, err := strconv.ParseFloat(item.BuyMaxPrice, 64)
	if err != nil {
		return fmt.Errorf("价格转换失败: %v", err)
	}

	// 创建商品数据（简化实现）
	itemData := map[string]interface{}{
		"item_id":          item.Id,
		"name":             item.Name,
		"market_hash_name": item.MarketHashName,
		"buy_max_price":    price,
		"buy_num":          item.BuyNum,
		"appid":            item.Appid,
		"created_on":       time.Now().Unix(),
		"modified_on":      time.Now().Unix(),
	}

	// 这里可以实现具体的数据库保存逻辑
	_ = itemData // 暂时忽略，避免编译错误

	// 缓存到Redis
	cacheKey := fmt.Sprintf("buff_buy:%d", item.Id)
	if data, err := json.Marshal(itemData); err == nil {
		gredis.Set(cacheKey, string(data), 10*time.Minute)
	}

	return nil
}
