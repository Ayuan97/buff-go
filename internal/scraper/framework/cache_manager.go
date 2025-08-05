package framework

import (
	"buff-go/pkg/gredis"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// CacheManager 缓存管理器实现
type CacheManager struct {
	localCache map[string]*CacheItem
	localMux   sync.RWMutex
	stats      *CacheStats
	statsMux   sync.RWMutex
	prefix     string
}

// NewCacheManager 创建缓存管理器
func NewCacheManager(prefix string) *CacheManager {
	cm := &CacheManager{
		localCache: make(map[string]*CacheItem),
		stats:      &CacheStats{},
		prefix:     prefix,
	}

	// 启动清理任务
	go cm.startCleanupTask()

	return cm
}

// Get 获取缓存值
func (cm *CacheManager) Get(key string) (interface{}, error) {
	fullKey := cm.getFullKey(key)

	// 先从本地缓存获取
	cm.localMux.RLock()
	if item, exists := cm.localCache[key]; exists {
		if !cm.isExpired(item) {
			cm.localMux.RUnlock()
			cm.updateStats(true)
			return item.Value, nil
		}
		// 过期了，删除本地缓存
		delete(cm.localCache, key)
	}
	cm.localMux.RUnlock()

	// 尝试从Redis获取（如果Redis可用）
	var value string
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Redis不可用时设置为空值
				value = ""
			}
		}()
		value = gredis.Get(fullKey)
	}()

	if value == "" {
		cm.updateStats(false)
		return nil, fmt.Errorf("cache miss for key: %s", key)
	}

	// 解析缓存值
	var cacheValue interface{}
	if err := json.Unmarshal([]byte(value), &cacheValue); err != nil {
		cm.updateStats(false)
		return nil, fmt.Errorf("failed to unmarshal cache value: %v", err)
	}

	// 更新本地缓存
	item := &CacheItem{
		Key:       key,
		Value:     cacheValue,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(5 * time.Minute), // 本地缓存5分钟
	}

	cm.localMux.Lock()
	cm.localCache[key] = item
	cm.localMux.Unlock()

	cm.updateStats(true)
	return cacheValue, nil
}

// Set 设置缓存值
func (cm *CacheManager) Set(key string, value interface{}, ttl time.Duration) error {
	fullKey := cm.getFullKey(key)

	// 序列化值
	valueBytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal cache value: %v", err)
	}

	// 尝试设置到Redis（如果Redis可用）
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Redis不可用时忽略错误
			}
		}()
		gredis.Set(fullKey, string(valueBytes), ttl)
	}()

	// 更新本地缓存
	item := &CacheItem{
		Key:       key,
		Value:     value,
		TTL:       ttl,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}

	cm.localMux.Lock()
	cm.localCache[key] = item
	cm.localMux.Unlock()

	return nil
}

// Delete 删除缓存
func (cm *CacheManager) Delete(key string) error {
	fullKey := cm.getFullKey(key)

	// 尝试从Redis删除（如果Redis可用）
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Redis不可用时忽略错误
			}
		}()
		gredis.Del(fullKey)
	}()

	// 从本地缓存删除
	cm.localMux.Lock()
	delete(cm.localCache, key)
	cm.localMux.Unlock()

	return nil
}

// Exists 检查缓存是否存在
func (cm *CacheManager) Exists(key string) bool {
	// 先检查本地缓存
	cm.localMux.RLock()
	if item, exists := cm.localCache[key]; exists {
		if !cm.isExpired(item) {
			cm.localMux.RUnlock()
			return true
		}
		// 过期了，删除本地缓存
		delete(cm.localCache, key)
	}
	cm.localMux.RUnlock()

	// 检查Redis（如果Redis可用）
	fullKey := cm.getFullKey(key)
	var value string
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Redis不可用时设置为空值
				value = ""
			}
		}()
		value = gredis.Get(fullKey)
	}()
	return value != ""
}

// Clear 清空缓存
func (cm *CacheManager) Clear() error {
	// 清空本地缓存
	cm.localMux.Lock()
	cm.localCache = make(map[string]*CacheItem)
	cm.localMux.Unlock()

	// 这里可以实现清空Redis中特定前缀的缓存
	// 为了简化，暂时不实现

	return nil
}

// GetStats 获取缓存统计信息
func (cm *CacheManager) GetStats() *CacheStats {
	cm.statsMux.RLock()
	defer cm.statsMux.RUnlock()

	// 计算命中率
	hitRate := float64(0)
	if cm.stats.HitCount+cm.stats.MissCount > 0 {
		hitRate = float64(cm.stats.HitCount) / float64(cm.stats.HitCount+cm.stats.MissCount)
	}

	return &CacheStats{
		TotalKeys:   cm.stats.TotalKeys,
		HitCount:    cm.stats.HitCount,
		MissCount:   cm.stats.MissCount,
		HitRate:     hitRate,
		MemoryUsage: cm.stats.MemoryUsage,
	}
}

// GetWithTTL 获取缓存值和TTL
func (cm *CacheManager) GetWithTTL(key string) (interface{}, time.Duration, error) {
	value, err := cm.Get(key)
	if err != nil {
		return nil, 0, err
	}

	// 从本地缓存获取TTL
	cm.localMux.RLock()
	if item, exists := cm.localCache[key]; exists {
		ttl := time.Until(item.ExpiresAt)
		cm.localMux.RUnlock()
		return value, ttl, nil
	}
	cm.localMux.RUnlock()

	// 如果本地缓存没有，返回默认TTL
	return value, 5 * time.Minute, nil
}

// SetNX 仅当key不存在时设置
func (cm *CacheManager) SetNX(key string, value interface{}, ttl time.Duration) (bool, error) {
	if cm.Exists(key) {
		return false, nil
	}

	err := cm.Set(key, value, ttl)
	return err == nil, err
}

// Increment 递增数值
func (cm *CacheManager) Increment(key string, delta int64) (int64, error) {
	fullKey := cm.getFullKey(key)

	// 使用Redis的INCR命令（只能递增1，如果需要递增delta，需要多次调用）
	var result int64
	var err error

	if delta == 1 {
		result, err = gredis.Incr(fullKey)
	} else {
		// 对于非1的增量，我们需要使用其他方法
		// 这里简化处理，直接返回错误
		return 0, fmt.Errorf("increment by %d not supported, only increment by 1 is supported", delta)
	}

	if err != nil {
		return 0, err
	}

	// 更新本地缓存
	cm.localMux.Lock()
	cm.localCache[key] = &CacheItem{
		Key:       key,
		Value:     result,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	cm.localMux.Unlock()

	return result, nil
}

// GetMulti 批量获取缓存
func (cm *CacheManager) GetMulti(keys []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for _, key := range keys {
		if value, err := cm.Get(key); err == nil {
			result[key] = value
		}
	}

	return result, nil
}

// SetMulti 批量设置缓存
func (cm *CacheManager) SetMulti(items map[string]interface{}, ttl time.Duration) error {
	for key, value := range items {
		if err := cm.Set(key, value, ttl); err != nil {
			return err
		}
	}

	return nil
}

// getFullKey 获取完整的缓存键
func (cm *CacheManager) getFullKey(key string) string {
	if cm.prefix == "" {
		return key
	}
	return fmt.Sprintf("%s:%s", cm.prefix, key)
}

// isExpired 检查缓存项是否过期
func (cm *CacheManager) isExpired(item *CacheItem) bool {
	return time.Now().After(item.ExpiresAt)
}

// updateStats 更新统计信息
func (cm *CacheManager) updateStats(hit bool) {
	cm.statsMux.Lock()
	defer cm.statsMux.Unlock()

	if hit {
		cm.stats.HitCount++
	} else {
		cm.stats.MissCount++
	}
}

// startCleanupTask 启动清理任务
func (cm *CacheManager) startCleanupTask() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cm.cleanup()
	}
}

// cleanup 清理过期的本地缓存
func (cm *CacheManager) cleanup() {
	cm.localMux.Lock()
	defer cm.localMux.Unlock()

	now := time.Now()
	for key, item := range cm.localCache {
		if now.After(item.ExpiresAt) {
			delete(cm.localCache, key)
		}
	}

	// 更新统计信息
	cm.statsMux.Lock()
	cm.stats.TotalKeys = int64(len(cm.localCache))
	cm.statsMux.Unlock()
}
