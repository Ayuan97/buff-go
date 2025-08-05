package business

import (
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"buff-go/internal/scraper/framework"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"fmt"
	"sync"
	"time"
)

// DataProcessor 数据处理器
type DataProcessor struct {
	dao          *dao.Dao
	cacheManager framework.ICacheManager
	priceMonitor *PriceMonitor
	mu           sync.RWMutex
}

// ProcessingResult 处理结果
type ProcessingResult struct {
	Success     bool                   `json:"success"`
	ProcessedAt time.Time              `json:"processed_at"`
	ItemCount   int                    `json:"item_count"`
	Errors      []string               `json:"errors"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// NewDataProcessor 创建数据处理器
func NewDataProcessor(dao *dao.Dao, cacheManager framework.ICacheManager, priceMonitor *PriceMonitor) *DataProcessor {
	return &DataProcessor{
		dao:          dao,
		cacheManager: cacheManager,
		priceMonitor: priceMonitor,
	}
}

// ProcessBuffBuyData 处理Buff买入数据
func (dp *DataProcessor) ProcessBuffBuyData(items []BuffBuyItem, game string, appid int) *ProcessingResult {
	result := &ProcessingResult{
		ProcessedAt: time.Now(),
		Metadata:    make(map[string]interface{}),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []string

	for _, item := range items {
		wg.Add(1)
		go func(item BuffBuyItem) {
			defer wg.Done()

			if err := dp.processBuffBuyItem(item, game, appid); err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("处理商品 %s 失败: %v", item.Name, err))
				mu.Unlock()
			}
		}(item)
	}

	wg.Wait()

	result.Success = len(errors) == 0
	result.ItemCount = len(items)
	result.Errors = errors
	result.Metadata["game"] = game
	result.Metadata["appid"] = appid

	return result
}

// processBuffBuyItem 处理单个Buff买入商品
func (dp *DataProcessor) processBuffBuyItem(item BuffBuyItem, game string, appid int) error {
	key := rediskey.GetCacheKey(item.MarketHashName, game)

	// 查询缓存
	value, _ := gredis.HGetAll(key)
	var info model.Info

	newPrice := util.StringToFloat64(item.BuyMaxPrice)

	if len(value) == 0 {
		// 缓存不存在，查询数据库
		existingInfo, err := dp.dao.GetOneInfoByMarketHashName(item.MarketHashName)
		if err != nil {
			// 数据库不存在，创建新记录
			info = model.Info{
				Appid:          item.Appid,
				GoodsId:        item.Id,
				MarketHashName: item.MarketHashName,
				BuffBuyPrice:   newPrice,
				BuffBuyNum:     item.BuyNum,
				Name:           item.Name,
				Game:           game,
				BuffBuyUpdate:  int(time.Now().Unix()),
			}

			if err := dp.dao.CreateInfo(&info); err != nil {
				return fmt.Errorf("创建商品信息失败: %v", err)
			}
		} else {
			// 数据库存在，更新数据
			oldPrice := existingInfo.BuffBuyPrice
			existingInfo.BuffBuyPrice = newPrice
			existingInfo.BuffBuyNum = item.BuyNum
			existingInfo.BuffBuyUpdate = int(time.Now().Unix())

			if err := dp.dao.UpdateInfo(existingInfo); err != nil {
				return fmt.Errorf("更新商品信息失败: %v", err)
			}

			// 通知价格监控器
			if dp.priceMonitor != nil && oldPrice != newPrice {
				dp.priceMonitor.NotifyPriceChange(&PriceChangeEvent{
					MarketHashName: item.MarketHashName,
					Game:           game,
					Type:           "buff_buy",
					OldPrice:       oldPrice,
					NewPrice:       newPrice,
					Timestamp:      time.Now(),
				})
			}

			info = *existingInfo
		}

		// 更新缓存
		dp.updateCache(key, &info)
	} else {
		// 缓存存在，检查价格变化
		oldPrice := util.StringToFloat64(value["buff_buy_price"])

		if oldPrice != newPrice {
			// 价格有变化，更新缓存和数据库
			dp.updateCachePrice(key, "buff_buy_price", newPrice)
			dp.updateCachePrice(key, "buff_buy_num", float64(item.BuyNum))
			dp.updateCachePrice(key, "buff_buy_update", float64(time.Now().Unix()))

			// 更新数据库
			info.MarketHashName = item.MarketHashName
			info.BuffBuyPrice = newPrice
			info.BuffBuyNum = item.BuyNum
			info.BuffBuyUpdate = int(time.Now().Unix())

			if err := dp.dao.UpdateInfo(&info); err != nil {
				return fmt.Errorf("更新商品价格失败: %v", err)
			}

			// 通知价格监控器
			if dp.priceMonitor != nil {
				dp.priceMonitor.NotifyPriceChange(&PriceChangeEvent{
					MarketHashName: item.MarketHashName,
					Game:           game,
					Type:           "buff_buy",
					OldPrice:       oldPrice,
					NewPrice:       newPrice,
					Timestamp:      time.Now(),
				})
			}
		}
	}

	return nil
}

// updateCache 更新缓存
func (dp *DataProcessor) updateCache(key string, info *model.Info) {
	gredis.Hset(key, "buff_buy_price", info.BuffBuyPrice)
	gredis.Hset(key, "buff_buy_num", info.BuffBuyNum)
	gredis.Hset(key, "buff_buy_update", info.BuffBuyUpdate)
}

// updateCachePrice 更新缓存价格
func (dp *DataProcessor) updateCachePrice(key, field string, value float64) {
	gredis.Hset(key, field, value)
}

// processSteamBuyItem 处理单个Steam买入商品
func (dp *DataProcessor) processSteamBuyItem(item SteamBuyItem, game string, appid int) error {
	key := rediskey.GetCacheKey(item.MarketHashName, game)

	// 查询缓存
	value, _ := gredis.HGetAll(key)
	var info model.Info

	newPrice := item.SellPrice

	if len(value) == 0 {
		// 缓存不存在，查询数据库
		existingInfo, err := dp.dao.GetOneInfoByMarketHashName(item.MarketHashName)
		if err != nil {
			// 数据库不存在，创建新记录
			info = model.Info{
				Appid:          appid,
				MarketHashName: item.MarketHashName,
				SteamBuyPrice:  newPrice,
				SteamBuyNum:    item.SellListings,
				Name:           item.Name,
				Game:           game,
				SteamBuyUpdate: int(time.Now().Unix()),
			}

			if !dp.dao.CreateInfo(&info) {
				return fmt.Errorf("创建Steam买入商品信息失败")
			}
		} else {
			// 数据库存在，更新数据
			oldPrice := existingInfo.SteamBuyPrice
			existingInfo.SteamBuyPrice = newPrice
			existingInfo.SteamBuyNum = item.SellListings
			existingInfo.SteamBuyUpdate = int(time.Now().Unix())

			if err := dp.dao.UpdateInfo(existingInfo); err != nil {
				return fmt.Errorf("更新Steam买入商品信息失败: %v", err)
			}

			// 通知价格监控器
			if dp.priceMonitor != nil && oldPrice != newPrice {
				dp.priceMonitor.NotifyPriceChange(&PriceChangeEvent{
					MarketHashName: item.MarketHashName,
					Game:           game,
					Type:           "steam_buy",
					OldPrice:       oldPrice,
					NewPrice:       newPrice,
					Timestamp:      time.Now(),
				})
			}

			info = *existingInfo
		}

		// 更新缓存
		dp.updateSteamBuyCache(key, &info)
	} else {
		// 缓存存在，检查价格变化
		oldPrice := util.StringToFloat64(value["steam_buy_price"])

		if oldPrice != newPrice {
			// 价格有变化，更新缓存和数据库
			dp.updateCachePrice(key, "steam_buy_price", newPrice)
			dp.updateCachePrice(key, "steam_buy_num", float64(item.SellListings))
			dp.updateCachePrice(key, "steam_buy_update", float64(time.Now().Unix()))

			// 更新数据库
			info.MarketHashName = item.MarketHashName
			info.SteamBuyPrice = newPrice
			info.SteamBuyNum = item.SellListings
			info.SteamBuyUpdate = int(time.Now().Unix())

			if err := dp.dao.UpdateInfo(&info); err != nil {
				return fmt.Errorf("更新Steam买入商品价格失败: %v", err)
			}

			// 通知价格监控器
			if dp.priceMonitor != nil {
				dp.priceMonitor.NotifyPriceChange(&PriceChangeEvent{
					MarketHashName: item.MarketHashName,
					Game:           game,
					Type:           "steam_buy",
					OldPrice:       oldPrice,
					NewPrice:       newPrice,
					Timestamp:      time.Now(),
				})
			}
		}
	}

	return nil
}

// processSteamSellItem 处理单个Steam卖出商品
func (dp *DataProcessor) processSteamSellItem(item SteamSellItem, game string, appid int) error {
	key := rediskey.GetCacheKey(item.MarketHashName, game)

	// 查询缓存
	value, _ := gredis.HGetAll(key)
	var info model.Info

	newPrice := item.SellPrice

	if len(value) == 0 {
		// 缓存不存在，查询数据库
		existingInfo, err := dp.dao.GetOneInfoByMarketHashName(item.MarketHashName)
		if err != nil {
			// 数据库不存在，创建新记录
			info = model.Info{
				Appid:           appid,
				MarketHashName:  item.MarketHashName,
				SteamSellPrice:  newPrice,
				SteamSellNum:    item.SellListings,
				Name:            item.Name,
				Game:            game,
				SteamSellUpdate: int(time.Now().Unix()),
			}

			if !dp.dao.CreateInfo(&info) {
				return fmt.Errorf("创建Steam卖出商品信息失败")
			}
		} else {
			// 数据库存在，更新数据
			oldPrice := existingInfo.SteamSellPrice
			existingInfo.SteamSellPrice = newPrice
			existingInfo.SteamSellNum = item.SellListings
			existingInfo.SteamSellUpdate = int(time.Now().Unix())

			if err := dp.dao.UpdateInfo(existingInfo); err != nil {
				return fmt.Errorf("更新Steam卖出商品信息失败: %v", err)
			}

			// 通知价格监控器
			if dp.priceMonitor != nil && oldPrice != newPrice {
				dp.priceMonitor.NotifyPriceChange(&PriceChangeEvent{
					MarketHashName: item.MarketHashName,
					Game:           game,
					Type:           "steam_sell",
					OldPrice:       oldPrice,
					NewPrice:       newPrice,
					Timestamp:      time.Now(),
				})
			}

			info = *existingInfo
		}

		// 更新缓存
		dp.updateSteamSellCache(key, &info)
	} else {
		// 缓存存在，检查价格变化
		oldPrice := util.StringToFloat64(value["steam_sell_price"])

		if oldPrice != newPrice {
			// 价格有变化，更新缓存和数据库
			dp.updateCachePrice(key, "steam_sell_price", newPrice)
			dp.updateCachePrice(key, "steam_sell_num", float64(item.SellListings))
			dp.updateCachePrice(key, "steam_sell_update", float64(time.Now().Unix()))

			// 更新数据库
			info.MarketHashName = item.MarketHashName
			info.SteamSellPrice = newPrice
			info.SteamSellNum = item.SellListings
			info.SteamSellUpdate = int(time.Now().Unix())

			if err := dp.dao.UpdateInfo(&info); err != nil {
				return fmt.Errorf("更新Steam卖出商品价格失败: %v", err)
			}

			// 通知价格监控器
			if dp.priceMonitor != nil {
				dp.priceMonitor.NotifyPriceChange(&PriceChangeEvent{
					MarketHashName: item.MarketHashName,
					Game:           game,
					Type:           "steam_sell",
					OldPrice:       oldPrice,
					NewPrice:       newPrice,
					Timestamp:      time.Now(),
				})
			}
		}
	}

	return nil
}

// updateSteamBuyCache 更新Steam买入缓存
func (dp *DataProcessor) updateSteamBuyCache(key string, info *model.Info) {
	gredis.Hset(key, "steam_buy_price", info.SteamBuyPrice)
	gredis.Hset(key, "steam_buy_num", info.SteamBuyNum)
	gredis.Hset(key, "steam_buy_update", info.SteamBuyUpdate)
}

// updateSteamSellCache 更新Steam卖出缓存
func (dp *DataProcessor) updateSteamSellCache(key string, info *model.Info) {
	gredis.Hset(key, "steam_sell_price", info.SteamSellPrice)
	gredis.Hset(key, "steam_sell_num", info.SteamSellNum)
	gredis.Hset(key, "steam_sell_update", info.SteamSellUpdate)
}

// 适配新框架接口的方法

// ProcessSteamBuyDataFramework 处理Steam买入数据（框架接口适配）
func (dp *DataProcessor) ProcessSteamBuyDataFramework(items []framework.SteamBuyItemData, game string, appid int) *framework.ProcessingResult {
	// 转换数据格式
	businessItems := make([]SteamBuyItem, len(items))
	for i, item := range items {
		businessItems[i] = SteamBuyItem{
			Name:           item.Name,
			HashName:       item.HashName,
			MarketHashName: item.MarketHashName,
			SellPrice:      item.SellPrice,
			SellPriceText:  item.SellPriceText,
			SellListings:   item.SellListings,
			Appid:          item.Appid,
			IconURL:        item.IconURL,
		}
	}

	// 调用原有的处理方法
	result := dp.ProcessSteamBuyData(businessItems, game, appid)

	// 转换返回结果格式
	return &framework.ProcessingResult{
		Success:     result.Success,
		ProcessedAt: result.ProcessedAt,
		ItemCount:   result.ItemCount,
		Errors:      result.Errors,
		Metadata:    result.Metadata,
	}
}

// ProcessSteamSellDataFramework 处理Steam卖出数据（框架接口适配）
func (dp *DataProcessor) ProcessSteamSellDataFramework(items []framework.SteamSellItemData, game string, appid int) *framework.ProcessingResult {
	// 转换数据格式
	businessItems := make([]SteamSellItem, len(items))
	for i, item := range items {
		businessItems[i] = SteamSellItem{
			Name:           item.Name,
			HashName:       item.HashName,
			MarketHashName: item.MarketHashName,
			SellPrice:      item.SellPrice,
			SellPriceText:  item.SellPriceText,
			SellListings:   item.SellListings,
			Appid:          item.Appid,
			IconURL:        item.IconURL,
		}
	}

	// 调用原有的处理方法
	result := dp.ProcessSteamSellData(businessItems, game, appid)

	// 转换返回结果格式
	return &framework.ProcessingResult{
		Success:     result.Success,
		ProcessedAt: result.ProcessedAt,
		ItemCount:   result.ItemCount,
		Errors:      result.Errors,
		Metadata:    result.Metadata,
	}
}

// ProcessBuffSellData 处理Buff卖出数据
func (dp *DataProcessor) ProcessBuffSellData(items []BuffSellItem, game string, appid int) *ProcessingResult {
	// TODO: 实现Buff卖出数据处理逻辑
	return &ProcessingResult{
		Success:     true,
		ProcessedAt: time.Now(),
		ItemCount:   len(items),
		Metadata:    map[string]interface{}{"game": game, "appid": appid},
	}
}

// ProcessSteamBuyData 处理Steam买入数据
func (dp *DataProcessor) ProcessSteamBuyData(items []SteamBuyItem, game string, appid int) *ProcessingResult {
	result := &ProcessingResult{
		ProcessedAt: time.Now(),
		Metadata:    make(map[string]interface{}),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []string

	for _, item := range items {
		wg.Add(1)
		go func(item SteamBuyItem) {
			defer wg.Done()

			if err := dp.processSteamBuyItem(item, game, appid); err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("处理Steam买入商品 %s 失败: %v", item.Name, err))
				mu.Unlock()
			}
		}(item)
	}

	wg.Wait()

	result.Success = len(errors) == 0
	result.ItemCount = len(items)
	result.Errors = errors
	result.Metadata["game"] = game
	result.Metadata["appid"] = appid

	return result
}

// ProcessSteamSellData 处理Steam卖出数据
func (dp *DataProcessor) ProcessSteamSellData(items []SteamSellItem, game string, appid int) *ProcessingResult {
	result := &ProcessingResult{
		ProcessedAt: time.Now(),
		Metadata:    make(map[string]interface{}),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []string

	for _, item := range items {
		wg.Add(1)
		go func(item SteamSellItem) {
			defer wg.Done()

			if err := dp.processSteamSellItem(item, game, appid); err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("处理Steam卖出商品 %s 失败: %v", item.Name, err))
				mu.Unlock()
			}
		}(item)
	}

	wg.Wait()

	result.Success = len(errors) == 0
	result.ItemCount = len(items)
	result.Errors = errors
	result.Metadata["game"] = game
	result.Metadata["appid"] = appid

	return result
}

// GetProcessingStats 获取处理统计信息
func (dp *DataProcessor) GetProcessingStats() map[string]interface{} {
	return map[string]interface{}{
		"processor_name":  "DataProcessor",
		"created_at":      time.Now(),
		"cache_enabled":   dp.cacheManager != nil,
		"monitor_enabled": dp.priceMonitor != nil,
	}
}

// 数据结构定义
type BuffBuyItem struct {
	Id             int    `json:"id"`
	Name           string `json:"name"`
	MarketHashName string `json:"market_hash_name"`
	BuyMaxPrice    string `json:"buy_max_price"`
	BuyNum         int    `json:"buy_num"`
	Appid          int    `json:"appid"`
}

type BuffSellItem struct {
	Id             int    `json:"id"`
	Name           string `json:"name"`
	MarketHashName string `json:"market_hash_name"`
	SellMinPrice   string `json:"sell_min_price"`
	SellNum        int    `json:"sell_num"`
	Appid          int    `json:"appid"`
}

type SteamBuyItem struct {
	Name           string  `json:"name"`
	HashName       string  `json:"hash_name"`
	MarketHashName string  `json:"market_hash_name"`
	SellPrice      float64 `json:"sell_price"`
	SellPriceText  string  `json:"sell_price_text"`
	SellListings   int     `json:"sell_listings"`
	Appid          int     `json:"appid"`
	IconURL        string  `json:"icon_url"`
}

type SteamSellItem struct {
	Name           string  `json:"name"`
	HashName       string  `json:"hash_name"`
	MarketHashName string  `json:"market_hash_name"`
	SellPrice      float64 `json:"sell_price"`
	SellPriceText  string  `json:"sell_price_text"`
	SellListings   int     `json:"sell_listings"`
	Appid          int     `json:"appid"`
	IconURL        string  `json:"icon_url"`
}
