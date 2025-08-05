package framework

import (
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	fmt.Printf("buff - buy - name: %s | 价格: %s | 数量: %d\n", item.Name, item.BuyMaxPrice, item.BuyNum)

	key := rediskey.GetCacheKey(item.MarketHashName, game)

	// 查询缓存
	value, _ := gredis.HGetAll(key)
	var info model.Info

	if len(value) == 0 {
		// 缓存不存在，查询数据库
		_, err := bs.dao.GetOneInfoByMarketHashName(item.MarketHashName)
		if err != nil {
			// 数据库不存在，创建新记录
			info.Appid = item.Appid
			info.GoodsId = item.Id
			info.MarketHashName = item.MarketHashName
			info.BuffBuyPrice = util.StringToFloat64(item.BuyMaxPrice)
			info.BuffBuyNum = item.BuyNum
			info.Name = item.Name
			info.Game = game
			info.BuffBuyUpdate = int(time.Now().Unix())

			// 插入数据库
			bs.dao.CreateInfo(&info)
		} else {
			// 数据库存在，更新数据
			info.BuffBuyPrice = util.StringToFloat64(item.BuyMaxPrice)
			info.BuffBuyNum = item.BuyNum
			info.BuffBuyUpdate = int(time.Now().Unix())
			bs.dao.UpdateInfo(&info)
		}

		// 更新缓存
		gredis.Hset(key, "buff_buy_price", info.BuffBuyPrice)
		gredis.Hset(key, "buff_buy_num", info.BuffBuyNum)
		gredis.Hset(key, "buff_buy_update", info.BuffBuyUpdate)
	} else {
		// 缓存存在，直接更新
		oldPrice := util.StringToFloat64(value["buff_buy_price"])
		newPrice := util.StringToFloat64(item.BuyMaxPrice)

		if oldPrice != newPrice {
			// 价格有变化，更新缓存和数据库
			gredis.Hset(key, "buff_buy_price", newPrice)
			gredis.Hset(key, "buff_buy_num", item.BuyNum)
			gredis.Hset(key, "buff_buy_update", time.Now().Unix())

			// 更新数据库
			info.MarketHashName = item.MarketHashName
			info.BuffBuyPrice = newPrice
			info.BuffBuyNum = item.BuyNum
			info.BuffBuyUpdate = int(time.Now().Unix())
			bs.dao.UpdateInfo(&info)
		}
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
