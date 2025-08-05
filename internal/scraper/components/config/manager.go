package config

import (
	"buff-go/internal/dao"
	"buff-go/internal/scraper/core"
	"buff-go/pkg/gredis"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// ConfigManager 配置管理器实现
type ConfigManager struct {
	dao      *dao.Dao
	cache    map[string]*core.ConfigItem
	cacheMux sync.RWMutex
	watchers map[string][]func(oldValue, newValue interface{})
	watchMux sync.RWMutex
}

// NewConfigManager 创建配置管理器
func NewConfigManager(dao *dao.Dao) *ConfigManager {
	cm := &ConfigManager{
		dao:      dao,
		cache:    make(map[string]*core.ConfigItem),
		watchers: make(map[string][]func(oldValue, newValue interface{})),
	}

	// 启动配置监听
	go cm.startConfigWatcher()

	return cm
}

// Get 获取配置值
func (cm *ConfigManager) Get(key string) (interface{}, error) {
	// 先从缓存获取
	cm.cacheMux.RLock()
	if item, exists := cm.cache[key]; exists {
		cm.cacheMux.RUnlock()
		return item.Value, nil
	}
	cm.cacheMux.RUnlock()

	// 尝试从Redis获取（如果Redis可用）
	defer func() {
		if r := recover(); r != nil {
			// Redis不可用时忽略错误
		}
	}()

	cacheKey := fmt.Sprintf("config:key:%s", key)
	value := gredis.Get(cacheKey)
	if value != "" {
		var configValue interface{}
		if err := json.Unmarshal([]byte(value), &configValue); err == nil {
			item := &core.ConfigItem{
				Key:       key,
				Value:     configValue,
				UpdatedAt: time.Now(),
			}

			cm.cacheMux.Lock()
			cm.cache[key] = item
			cm.cacheMux.Unlock()

			return configValue, nil
		}
	}

	// 从数据库获取
	return cm.getFromDatabase(key)
}

// GetString 获取字符串配置
func (cm *ConfigManager) GetString(key string) (string, error) {
	value, err := cm.Get(key)
	if err != nil {
		return "", err
	}

	if str, ok := value.(string); ok {
		return str, nil
	}

	return fmt.Sprintf("%v", value), nil
}

// GetInt 获取整数配置
func (cm *ConfigManager) GetInt(key string) (int, error) {
	value, err := cm.Get(key)
	if err != nil {
		return 0, err
	}

	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		return strconv.Atoi(v)
	default:
		return 0, fmt.Errorf("cannot convert %T to int", value)
	}
}

// GetBool 获取布尔配置
func (cm *ConfigManager) GetBool(key string) (bool, error) {
	value, err := cm.Get(key)
	if err != nil {
		return false, err
	}

	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		return strconv.ParseBool(v)
	case int:
		return v != 0, nil
	case float64:
		return v != 0, nil
	default:
		return false, fmt.Errorf("cannot convert %T to bool", value)
	}
}

// GetFloat64 获取浮点数配置
func (cm *ConfigManager) GetFloat64(key string) (float64, error) {
	value, err := cm.Get(key)
	if err != nil {
		return 0, err
	}

	switch v := value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", value)
	}
}

// GetDuration 获取时间间隔配置
func (cm *ConfigManager) GetDuration(key string) (time.Duration, error) {
	value, err := cm.Get(key)
	if err != nil {
		return 0, err
	}

	switch v := value.(type) {
	case string:
		return time.ParseDuration(v)
	case int:
		return time.Duration(v) * time.Second, nil
	case int64:
		return time.Duration(v) * time.Second, nil
	case float64:
		return time.Duration(v) * time.Second, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to duration", value)
	}
}

// Set 设置配置值
func (cm *ConfigManager) Set(key string, value interface{}) error {
	// 获取旧值用于通知
	oldValue, _ := cm.Get(key)

	// 更新缓存
	item := &core.ConfigItem{
		Key:       key,
		Value:     value,
		UpdatedAt: time.Now(),
	}

	cm.cacheMux.Lock()
	cm.cache[key] = item
	cm.cacheMux.Unlock()

	// 尝试更新Redis缓存（如果Redis可用）
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Redis不可用时忽略错误
			}
		}()

		cacheKey := fmt.Sprintf("config:key:%s", key)
		valueBytes, err := json.Marshal(value)
		if err == nil {
			gredis.Set(cacheKey, string(valueBytes), time.Hour)
		}
	}()

	// 通知监听器
	cm.notifyWatchers(key, oldValue, value)

	// 这里可以选择是否同步到数据库
	// 为了简化，暂时只更新缓存

	return nil
}

// Watch 监听配置变化
func (cm *ConfigManager) Watch(key string, callback func(oldValue, newValue interface{})) error {
	if callback == nil {
		return errors.New("callback cannot be nil")
	}

	cm.watchMux.Lock()
	defer cm.watchMux.Unlock()

	if cm.watchers[key] == nil {
		cm.watchers[key] = make([]func(oldValue, newValue interface{}), 0)
	}

	cm.watchers[key] = append(cm.watchers[key], callback)

	return nil
}

// Reload 重新加载配置
func (cm *ConfigManager) Reload() error {
	cm.cacheMux.Lock()
	defer cm.cacheMux.Unlock()

	// 清空缓存
	cm.cache = make(map[string]*core.ConfigItem)

	// 清空Redis缓存中的配置
	// 这里可以根据需要实现具体的清理逻辑

	return nil
}

// getFromDatabase 从数据库获取配置
func (cm *ConfigManager) getFromDatabase(key string) (interface{}, error) {
	// 根据key的格式判断获取哪种配置
	switch {
	case key == "system.type":
		system := cm.dao.GetOneSystem(1)
		return system.SystemType, nil
	case key == "buff.buy.status":
		config := cm.dao.GetOneSystemConfig(1)
		return config.BuffBuyStatus, nil
	case key == "buff.sell.status":
		config := cm.dao.GetOneSystemConfig(1)
		return config.BuffSellStatus, nil
	case key == "steam.buy.status":
		config := cm.dao.GetOneSystemConfig(1)
		return config.SteamBuyStatus, nil
	case key == "steam.sell.status":
		config := cm.dao.GetOneSystemConfig(1)
		return config.SteamSellStatus, nil
	case key == "buff.page.num":
		config := cm.dao.GetOneSystemConfig(1)
		return config.BuffPageNum, nil
	case key == "steam.page.num":
		config := cm.dao.GetOneSystemConfig(1)
		return config.SteamPageNum, nil
	case key == "min.price":
		config := cm.dao.GetOneSystemConfig(1)
		return config.MinPrice, nil
	case key == "max.price":
		config := cm.dao.GetOneSystemConfig(1)
		return config.MaxPrice, nil
	case key == "buff.buy.delay":
		config := cm.dao.GetOneSystemConfig(1)
		return config.BuffBuyDelay, nil
	case key == "buff.sell.delay":
		config := cm.dao.GetOneSystemConfig(1)
		return config.BuffSellDelay, nil
	case key == "steam.buy.delay":
		config := cm.dao.GetOneSystemConfig(1)
		return config.SteamBuyDelay, nil
	case key == "steam.sell.delay":
		config := cm.dao.GetOneSystemConfig(1)
		return config.SteamSellDelay, nil
	default:
		return nil, fmt.Errorf("unknown config key: %s", key)
	}
}

// notifyWatchers 通知监听器
func (cm *ConfigManager) notifyWatchers(key string, oldValue, newValue interface{}) {
	cm.watchMux.RLock()
	watchers := cm.watchers[key]
	cm.watchMux.RUnlock()

	for _, callback := range watchers {
		go func(cb func(oldValue, newValue interface{})) {
			defer func() {
				if r := recover(); r != nil {
					// 记录panic，但不影响其他监听器
					fmt.Printf("Config watcher panic: %v\n", r)
				}
			}()
			cb(oldValue, newValue)
		}(callback)
	}
}

// startConfigWatcher 启动配置监听
func (cm *ConfigManager) startConfigWatcher() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// 定期检查配置变化
		// 这里可以实现更复杂的配置变化检测逻辑
		cm.checkConfigChanges()
	}
}

// checkConfigChanges 检查配置变化
func (cm *ConfigManager) checkConfigChanges() {
	// 这里可以实现配置变化检测逻辑
	// 比如检查数据库中的配置是否有更新
	// 为了简化，暂时不实现具体逻辑
}

// GetScraperConfig 获取抓取器配置
func (cm *ConfigManager) GetScraperConfig(scraperName string) (*core.ScraperConfig, error) {
	config := &core.ScraperConfig{
		Name:           scraperName,
		MaxConcurrency: 5,
		RequestDelay:   time.Second,
		RetryCount:     3,
		Timeout:        30 * time.Second,
		UseProxy:       true,
		EnableCache:    true,
		CacheTTL:       5 * time.Minute,
	}

	// 根据抓取器名称获取特定配置
	switch scraperName {
	case "buff_buy":
		if status, err := cm.GetInt("buff.buy.status"); err == nil && status == 0 {
			return nil, errors.New("buff buy scraper is disabled")
		}
		if delay, err := cm.GetInt("buff.buy.delay"); err == nil {
			config.RequestDelay = time.Duration(delay) * time.Millisecond
		}
	case "buff_sell":
		if status, err := cm.GetInt("buff.sell.status"); err == nil && status == 0 {
			return nil, errors.New("buff sell scraper is disabled")
		}
		if delay, err := cm.GetInt("buff.sell.delay"); err == nil {
			config.RequestDelay = time.Duration(delay) * time.Millisecond
		}
	case "steam_buy":
		if status, err := cm.GetInt("steam.buy.status"); err == nil && status == 0 {
			return nil, errors.New("steam buy scraper is disabled")
		}
		if delay, err := cm.GetInt("steam.buy.delay"); err == nil {
			config.RequestDelay = time.Duration(delay) * time.Millisecond
		}
	case "steam_sell":
		if status, err := cm.GetInt("steam.sell.status"); err == nil && status == 0 {
			return nil, errors.New("steam sell scraper is disabled")
		}
		if delay, err := cm.GetInt("steam.sell.delay"); err == nil {
			config.RequestDelay = time.Duration(delay) * time.Millisecond
		}
	}

	return config, nil
}
