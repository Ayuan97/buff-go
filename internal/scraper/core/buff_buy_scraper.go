package core

import (
	"buff-go/internal/dao"
	"buff-go/internal/model"
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
	*BaseScraper
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
	baseScraper := NewBaseScraper("buff_buy")

	return &BuffBuyScraper{
		BaseScraper: baseScraper,
		dao:         dao,
	}
}

// Initialize 初始化抓取器
func (bs *BuffBuyScraper) Initialize(config *ScraperConfig) error {
	if err := bs.BaseScraper.Initialize(config); err != nil {
		return err
	}

	// 获取Buff买入特定配置
	bs.config = &BuffBuyConfig{
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
func (bs *BuffBuyScraper) Start(ctx context.Context) error {
	// 检查配置状态
	if bs.ConfigManager != nil {
		if status, err := bs.ConfigManager.GetInt("buff.buy.status"); err == nil && status == 0 {
			return fmt.Errorf("buff buy scraper is disabled")
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
func (bs *BuffBuyScraper) ProcessTask(task *ScrapingTask) (*ScrapingResult, error) {
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
func (bs *BuffBuyScraper) generateTasks() error {
	for i := 1; i <= bs.config.PageNum; i++ {
		url := fmt.Sprintf("https://buff.163.com/api/market/goods/buying?game=%s&page_num=%d&min_price=%.2f&max_price=%.2f&sort_by=price.desc&page_size=80&use_suggestion=0&_=%d",
			bs.config.Game, i, bs.config.MinPrice, bs.config.MaxPrice, time.Now().UnixNano()/1e6)

		task := &ScrapingTask{
			ID:     fmt.Sprintf("buff_buy_page_%d", i),
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
func (bs *BuffBuyScraper) processResponse(result *ScrapingResult) error {
	if result.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error: %d", result.StatusCode)
	}

	// 解析JSON数据
	var buffData BuffBuyData
	if err := json.Unmarshal(result.Body, &buffData); err != nil {
		return fmt.Errorf("failed to parse JSON: %v", err)
	}

	// 处理数据
	if err := bs.handleBuffData(buffData, result.Metadata); err != nil {
		return fmt.Errorf("failed to handle buff data: %v", err)
	}

	return nil
}

// handleBuffData 处理Buff数据
func (bs *BuffBuyScraper) handleBuffData(buffData BuffBuyData, metadata map[string]interface{}) error {
	game := bs.config.Game
	appid := bs.config.AppID

	if gameStr, ok := metadata["game"].(string); ok {
		game = gameStr
	}
	if appidInt, ok := metadata["app_id"].(int); ok {
		appid = appidInt
	}

	var wg sync.WaitGroup
	for _, item := range buffData.Result.Items {
		wg.Add(1)
		go func(item BuffBuyItem) {
			defer wg.Done()
			bs.processBuffItem(item, game, appid)
		}(item)
	}
	wg.Wait()

	return nil
}

// processBuffItem 处理单个Buff商品
func (bs *BuffBuyScraper) processBuffItem(item BuffBuyItem, game string, appid int) {
	// 转换价格字符串为浮点数
	buyMaxPrice, err := strconv.ParseFloat(item.BuyMaxPrice, 64)
	if err != nil {
		fmt.Printf("Failed to parse price %s for item %s: %v\n", item.BuyMaxPrice, item.Name, err)
		return
	}

	// 构建商品数据
	goods := &model.Goods{
		Name:           item.Name,
		MarketHashName: item.MarketHashName,
		BuyMaxPrice:    buyMaxPrice,
		BuyNum:         item.BuyNum,
		Appid:          appid,
		Game:           game,
		GoodsId:        item.Id,
	}

	// 使用现有的 CreateGoods 方法（存在即更新）
	if !bs.dao.CreateGoods(goods) {
		fmt.Printf("Failed to create/update goods %s\n", item.Name)
		return
	}
	fmt.Printf("Processed goods: %s\n", item.Name)

	// 缓存商品信息
	bs.cacheGoodsInfo(item)
}

// cacheGoodsInfo 缓存商品信息
func (bs *BuffBuyScraper) cacheGoodsInfo(item BuffBuyItem) {
	cacheKey := fmt.Sprintf("goods:%s", item.MarketHashName)
	goodsData := map[string]interface{}{
		"name":             item.Name,
		"market_hash_name": item.MarketHashName,
		"buy_max_price":    item.BuyMaxPrice,
		"buy_num":          item.BuyNum,
		"appid":            item.Appid,
		"updated_at":       time.Now().Unix(),
	}

	goodsJSON, err := json.Marshal(goodsData)
	if err != nil {
		fmt.Printf("Failed to marshal goods data for %s: %v\n", item.Name, err)
		return
	}

	if err := gredis.Set(cacheKey, string(goodsJSON), time.Hour); err != nil {
		fmt.Printf("Failed to cache goods %s: %v\n", item.Name, err)
	}
}

// GetConfig 获取配置
func (bs *BuffBuyScraper) GetConfig() *BuffBuyConfig {
	return bs.config
}

// UpdateConfig 更新配置
func (bs *BuffBuyScraper) UpdateConfig(config *BuffBuyConfig) {
	bs.config = config
}

// SetManagers 设置管理器
func (bs *BuffBuyScraper) SetManagers(
	httpManager IHTTPClientManager,
	proxyManager IProxyManager,
	configManager IConfigManager,
	cacheManager ICacheManager,
	errorHandler IErrorHandler,
	taskManager ITaskManager,
) {
	bs.BaseScraper.SetManagers(httpManager, proxyManager, configManager, cacheManager, errorHandler, taskManager)
}
