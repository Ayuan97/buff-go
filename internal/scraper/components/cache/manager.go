package cache

import (
	"buff-go/internal/scraper/interfaces"
	"buff-go/pkg/gredis"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// CacheManager 缓存管理器实现
type CacheManager struct {
	localCache map[string]*interfaces.CacheItem
	localMux   sync.RWMutex
	stats      *interfaces.CacheStats
	statsMux   sync.RWMutex
	prefix     string
}

// NewCacheManager 创建缓存管理器
func NewCacheManager(prefix string) *CacheManager {
	cm := &CacheManager{
		localCache: make(map[string]*interfaces.CacheItem),
		stats:      &interfaces.CacheStats{},
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

	// 反序列化值
	var result interface{}
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		cm.updateStats(false)
		return nil, fmt.Errorf("failed to unmarshal cache value: %v", err)
	}

	// 更新本地缓存
	item := &interfaces.CacheItem{
		Key:       key,
		Value:     result,
		TTL:       5 * time.Minute, // 默认TTL
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}

	cm.localMux.Lock()
	cm.localCache[key] = item
	cm.localMux.Unlock()

	cm.updateStats(true)
	return result, nil
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
	item := &interfaces.CacheItem{
		Key:       key,
		Value:     value,
		TTL:       ttl,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}

	cm.localMux.Lock()
	cm.localCache[key] = item
	cm.statsMux.Lock()
	cm.stats.TotalKeys++
	cm.statsMux.Unlock()
	cm.localMux.Unlock()

	return nil
}

// Delete 删除缓存
func (cm *CacheManager) Delete(key string) error {
	fullKey := cm.getFullKey(key)

	// 从Redis删除（如果Redis可用）
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
	if _, exists := cm.localCache[key]; exists {
		delete(cm.localCache, key)
		cm.statsMux.Lock()
		cm.stats.TotalKeys--
		cm.statsMux.Unlock()
	}
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
	cm.localCache = make(map[string]*interfaces.CacheItem)
	cm.statsMux.Lock()
	cm.stats.TotalKeys = 0
	cm.statsMux.Unlock()
	cm.localMux.Unlock()

	// 这里可以实现清空Redis中特定前缀的缓存
	// 为了简化，暂时不实现

	return nil
}

// GetStats 获取缓存统计信息
func (cm *CacheManager) GetStats() *interfaces.CacheStats {
	cm.statsMux.RLock()
	defer cm.statsMux.RUnlock()

	stats := *cm.stats

	// 计算命中率
	if stats.HitCount+stats.MissCount > 0 {
		stats.HitRate = float64(stats.HitCount) / float64(stats.HitCount+stats.MissCount)
	}

	return &stats
}

// getFullKey 获取完整的缓存键
func (cm *CacheManager) getFullKey(key string) string {
	if cm.prefix == "" {
		return key
	}
	return fmt.Sprintf("%s:%s", cm.prefix, key)
}

// isExpired 检查缓存项是否过期
func (cm *CacheManager) isExpired(item *interfaces.CacheItem) bool {
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
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cm.cleanup()
	}
}

// cleanup 清理过期的缓存项
func (cm *CacheManager) cleanup() {
	cm.localMux.Lock()
	defer cm.localMux.Unlock()

	expiredKeys := make([]string, 0)
	for key, item := range cm.localCache {
		if cm.isExpired(item) {
			expiredKeys = append(expiredKeys, key)
		}
	}

	for _, key := range expiredKeys {
		delete(cm.localCache, key)
		cm.statsMux.Lock()
		cm.stats.TotalKeys--
		cm.statsMux.Unlock()
	}
}
