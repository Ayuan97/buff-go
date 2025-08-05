package config

import (
	"buff-go/internal/scraper/interfaces"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// ConfigManager 配置管理器实现
type ConfigManager struct {
	configs   map[string]*interfaces.ConfigItem
	configMux sync.RWMutex
	watchers  map[string][]func(oldValue, newValue interface{})
	watchMux  sync.RWMutex
}

// NewConfigManager 创建配置管理器
func NewConfigManager() *ConfigManager {
	return &ConfigManager{
		configs:  make(map[string]*interfaces.ConfigItem),
		watchers: make(map[string][]func(oldValue, newValue interface{})),
	}
}

// Get 获取配置值
func (cm *ConfigManager) Get(key string) (interface{}, error) {
	cm.configMux.RLock()
	defer cm.configMux.RUnlock()

	item, exists := cm.configs[key]
	if !exists {
		return nil, fmt.Errorf("config key not found: %s", key)
	}

	return item.Value, nil
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
	case float32:
		return float64(v), nil
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
	case time.Duration:
		return v, nil
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
	cm.configMux.Lock()
	defer cm.configMux.Unlock()

	var oldValue interface{}
	if item, exists := cm.configs[key]; exists {
		oldValue = item.Value
		item.Value = value
		item.UpdatedAt = time.Now()
	} else {
		cm.configs[key] = &interfaces.ConfigItem{
			Key:       key,
			Value:     value,
			Type:      fmt.Sprintf("%T", value),
			UpdatedAt: time.Now(),
		}
	}

	// 通知监听器
	cm.notifyWatchers(key, oldValue, value)

	return nil
}

// Watch 监听配置变化
func (cm *ConfigManager) Watch(key string, callback func(oldValue, newValue interface{})) error {
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
	// 这里可以实现从文件或其他源重新加载配置的逻辑
	// 目前只是一个占位实现
	return nil
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
					// 忽略回调函数中的panic
				}
			}()
			cb(oldValue, newValue)
		}(callback)
	}
}

// GetAllConfigs 获取所有配置
func (cm *ConfigManager) GetAllConfigs() map[string]*interfaces.ConfigItem {
	cm.configMux.RLock()
	defer cm.configMux.RUnlock()

	result := make(map[string]*interfaces.ConfigItem)
	for key, item := range cm.configs {
		result[key] = &interfaces.ConfigItem{
			Key:         item.Key,
			Value:       item.Value,
			Type:        item.Type,
			Description: item.Description,
			UpdatedAt:   item.UpdatedAt,
		}
	}

	return result
}

// DeleteConfig 删除配置
func (cm *ConfigManager) DeleteConfig(key string) error {
	cm.configMux.Lock()
	defer cm.configMux.Unlock()

	if _, exists := cm.configs[key]; !exists {
		return fmt.Errorf("config key not found: %s", key)
	}

	delete(cm.configs, key)
	return nil
}

// SetDescription 设置配置描述
func (cm *ConfigManager) SetDescription(key, description string) error {
	cm.configMux.Lock()
	defer cm.configMux.Unlock()

	item, exists := cm.configs[key]
	if !exists {
		return fmt.Errorf("config key not found: %s", key)
	}

	item.Description = description
	item.UpdatedAt = time.Now()
	return nil
}

// LoadFromMap 从map加载配置
func (cm *ConfigManager) LoadFromMap(configs map[string]interface{}) error {
	for key, value := range configs {
		if err := cm.Set(key, value); err != nil {
			return fmt.Errorf("failed to set config %s: %v", key, err)
		}
	}
	return nil
}

// ToMap 转换为map
func (cm *ConfigManager) ToMap() map[string]interface{} {
	cm.configMux.RLock()
	defer cm.configMux.RUnlock()

	result := make(map[string]interface{})
	for key, item := range cm.configs {
		result[key] = item.Value
	}

	return result
}
