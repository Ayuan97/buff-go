package buff

import (
	"buff-go/global"
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
	"sync/atomic"
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
	// 设置任务处理器为自己，这样worker就会调用BuffBuyScraper的ProcessTask方法
	bs.BaseScraper.SetTaskProcessor(bs)
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

	global.Logger.WithFields(map[string]interface{}{
		"scraper":   "buff_buy",
		"interval":  "30s",
		"pages":     bs.config.PageNum,
		"game":      bs.config.Game,
		"min_price": bs.config.MinPrice,
		"max_price": bs.config.MaxPrice,
	}).Info("[BuffBuyScraper] 开始抓取循环，抓取间隔30秒")

	for {
		select {
		case <-bs.BaseScraper.Ctx.Done():
			global.Logger.WithFields(map[string]interface{}{
				"scraper": "buff_buy",
			}).Info("[BuffBuyScraper] 收到停止信号，退出抓取循环")
			return
		case <-ticker.C:
			startTime := time.Now()
			global.Logger.WithFields(map[string]interface{}{
				"scraper":    "buff_buy",
				"start_time": startTime.Format("2006-01-02 15:04:05.000"),
			}).Info("[BuffBuyScraper] 开始新一轮抓取")

			bs.scrapePages()

			duration := time.Since(startTime)
			global.Logger.WithFields(map[string]interface{}{
				"scraper":  "buff_buy",
				"duration": duration.String(),
				"end_time": time.Now().Format("2006-01-02 15:04:05.000"),
			}).Info("[BuffBuyScraper] 本轮抓取完成")
		}
	}
}

// scrapePages 抓取页面
func (bs *BuffBuyScraper) scrapePages() {
	global.Logger.WithFields(map[string]interface{}{
		"scraper":     "buff_buy",
		"total_pages": bs.config.PageNum,
		"game":        bs.config.Game,
		"price_range": fmt.Sprintf("%.2f-%.2f", bs.config.MinPrice, bs.config.MaxPrice),
	}).Info("[BuffBuyScraper] 开始抓取页面")

	for i := 1; i <= bs.config.PageNum; i++ {
		url := fmt.Sprintf("https://buff.163.com/api/market/goods/buying?game=%s&page_num=%d&min_price=%.2f&max_price=%.2f&sort_by=price.desc&page_size=80&use_suggestion=0&_=%d",
			bs.config.Game, i, bs.config.MinPrice, bs.config.MaxPrice, time.Now().UnixNano()/1e6)

		global.Logger.WithFields(map[string]interface{}{
			"scraper":     "buff_buy",
			"page":        i,
			"total_pages": bs.config.PageNum,
			"url":         url,
		}).Info("[BuffBuyScraper] 创建抓取任务")

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
			global.Logger.WithFields(map[string]interface{}{
				"scraper": "buff_buy",
				"page":    i,
				"error":   err.Error(),
			}).Error("[BuffBuyScraper] 添加任务失败")
		} else {
			global.Logger.WithFields(map[string]interface{}{
				"scraper": "buff_buy",
				"page":    i,
				"task_id": task.ID,
			}).Info("[BuffBuyScraper] 任务添加成功")
		}

		// 页面间延迟
		if i < bs.config.PageNum {
			global.Logger.WithFields(map[string]interface{}{
				"scraper": "buff_buy",
				"page":    i,
				"delay":   "2s",
			}).Debug("[BuffBuyScraper] 页面间延迟")
			time.Sleep(2 * time.Second)
		}
	}

	global.Logger.WithFields(map[string]interface{}{
		"scraper":     "buff_buy",
		"total_pages": bs.config.PageNum,
	}).Info("[BuffBuyScraper] 所有页面任务创建完成")
}

// processResponse 处理响应数据
func (bs *BuffBuyScraper) processResponse(result *interfaces.ScrapingResult) error {
	startTime := time.Now()

	global.Logger.WithFields(map[string]interface{}{
		"scraper":     "buff_buy",
		"task_id":     result.TaskID,
		"status_code": result.StatusCode,
		"body_size":   len(result.Body),
	}).Info("[BuffBuyScraper] 开始处理响应数据")

	if result.StatusCode != http.StatusOK {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":     "buff_buy",
			"task_id":     result.TaskID,
			"status_code": result.StatusCode,
		}).Error("[BuffBuyScraper] HTTP状态码错误")
		return fmt.Errorf("HTTP error: %d", result.StatusCode)
	}

	var buffData BuffBuyData
	if err := json.Unmarshal(result.Body, &buffData); err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":      "buff_buy",
			"task_id":      result.TaskID,
			"error":        err.Error(),
			"body_preview": string(result.Body[:min(len(result.Body), 200)]),
		}).Error("[BuffBuyScraper] JSON解析失败")
		return fmt.Errorf("JSON解析失败: %v", err)
	}

	if buffData.Code != "OK" {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":  "buff_buy",
			"task_id":  result.TaskID,
			"api_code": buffData.Code,
			"body":     string(result.Body),
		}).Error("[BuffBuyScraper] API返回错误")
		return fmt.Errorf("API返回错误: %s", buffData.Code)
	}

	itemCount := len(buffData.Result.Items)
	global.Logger.WithFields(map[string]interface{}{
		"scraper":    "buff_buy",
		"task_id":    result.TaskID,
		"item_count": itemCount,
		"api_code":   buffData.Code,
	}).Info("[BuffBuyScraper] 响应数据解析成功")

	// 处理商品数据
	err := bs.processItems(buffData.Result.Items)

	duration := time.Since(startTime)
	if err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"task_id":    result.TaskID,
			"item_count": itemCount,
			"duration":   duration.String(),
			"error":      err.Error(),
		}).Error("[BuffBuyScraper] 处理商品数据失败")
	} else {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"task_id":    result.TaskID,
			"item_count": itemCount,
			"duration":   duration.String(),
		}).Info("[BuffBuyScraper] 响应数据处理完成")
	}

	return err
}

// min 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// processItems 处理商品数据
func (bs *BuffBuyScraper) processItems(items []BuffBuyItem) error {
	startTime := time.Now()
	itemCount := len(items)

	global.Logger.WithFields(map[string]interface{}{
		"scraper":     "buff_buy",
		"item_count":  itemCount,
		"concurrency": 10,
	}).Info("[BuffBuyScraper] 开始处理商品数据")

	if itemCount == 0 {
		global.Logger.WithFields(map[string]interface{}{
			"scraper": "buff_buy",
		}).Warn("[BuffBuyScraper] 没有商品数据需要处理")
		return nil
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // 限制并发数
	var successCount, failCount int32

	for i, item := range items {
		wg.Add(1)
		go func(index int, item BuffBuyItem) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			global.Logger.WithFields(map[string]interface{}{
				"scraper":   "buff_buy",
				"item_id":   item.Id,
				"item_name": item.Name,
				"index":     index + 1,
				"total":     itemCount,
				"price":     item.BuyMaxPrice,
			}).Debug("[BuffBuyScraper] 开始处理单个商品")

			if err := bs.processItem(item); err != nil {
				atomic.AddInt32(&failCount, 1)
				global.Logger.WithFields(map[string]interface{}{
					"scraper":   "buff_buy",
					"item_id":   item.Id,
					"item_name": item.Name,
					"error":     err.Error(),
				}).Error("[BuffBuyScraper] 处理商品失败")
			} else {
				atomic.AddInt32(&successCount, 1)
				global.Logger.WithFields(map[string]interface{}{
					"scraper":   "buff_buy",
					"item_id":   item.Id,
					"item_name": item.Name,
				}).Debug("[BuffBuyScraper] 商品处理成功")
			}
		}(i, item)
	}

	wg.Wait()

	duration := time.Since(startTime)
	global.Logger.WithFields(map[string]interface{}{
		"scraper":       "buff_buy",
		"total_items":   itemCount,
		"success_count": int(successCount),
		"fail_count":    int(failCount),
		"duration":      duration.String(),
		"items_per_sec": float64(itemCount) / duration.Seconds(),
	}).Info("[BuffBuyScraper] 商品数据处理完成")

	return nil
}

// processItem 处理单个商品
func (bs *BuffBuyScraper) processItem(item BuffBuyItem) error {
	startTime := time.Now()

	// 转换价格
	price, err := strconv.ParseFloat(item.BuyMaxPrice, 64)
	if err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":   "buff_buy",
			"item_id":   item.Id,
			"item_name": item.Name,
			"price_str": item.BuyMaxPrice,
			"error":     err.Error(),
		}).Error("[BuffBuyScraper] 价格转换失败")
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

	global.Logger.WithFields(map[string]interface{}{
		"scraper":   "buff_buy",
		"item_id":   item.Id,
		"item_name": item.Name,
		"price":     price,
		"buy_num":   item.BuyNum,
		"appid":     item.Appid,
	}).Debug("[BuffBuyScraper] 商品数据创建成功")

	// 这里可以实现具体的数据库保存逻辑
	_ = itemData // 暂时忽略，避免编译错误

	// 缓存到Redis
	cacheKey := fmt.Sprintf("buff_buy:%d", item.Id)
	if data, err := json.Marshal(itemData); err == nil {
		if err := gredis.Set(cacheKey, string(data), 10*time.Minute); err != nil {
			global.Logger.WithFields(map[string]interface{}{
				"scraper":   "buff_buy",
				"item_id":   item.Id,
				"cache_key": cacheKey,
				"error":     err.Error(),
			}).Warn("[BuffBuyScraper] Redis缓存保存失败")
		} else {
			global.Logger.WithFields(map[string]interface{}{
				"scraper":   "buff_buy",
				"item_id":   item.Id,
				"cache_key": cacheKey,
				"ttl":       "10m",
			}).Debug("[BuffBuyScraper] 商品数据已缓存到Redis")
		}
	} else {
		global.Logger.WithFields(map[string]interface{}{
			"scraper": "buff_buy",
			"item_id": item.Id,
			"error":   err.Error(),
		}).Error("[BuffBuyScraper] 商品数据JSON序列化失败")
	}

	duration := time.Since(startTime)
	global.Logger.WithFields(map[string]interface{}{
		"scraper":   "buff_buy",
		"item_id":   item.Id,
		"item_name": item.Name,
		"duration":  duration.String(),
	}).Debug("[BuffBuyScraper] 单个商品处理完成")

	return nil
}
