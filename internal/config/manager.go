package config

import (
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

// SystemConfigManagerImpl 系统配置管理器实现
type SystemConfigManagerImpl struct {
	dao       *dao.Dao
	listeners map[int64][]ConfigChangeListener
	mutex     sync.RWMutex
	pubsub    *redis.PubSub
}

// ConfigChangeMessage 配置变更消息
type ConfigChangeMessage struct {
	ConfigID  int64  `json:"config_id"`
	Timestamp int64  `json:"timestamp"`
	Action    string `json:"action"`
}

var (
	systemConfigManager *SystemConfigManagerImpl
	once                sync.Once
)

// GetSystemConfigManager 获取系统配置管理器单例
func GetSystemConfigManager(dao *dao.Dao) SystemConfigManager {
	once.Do(func() {
		systemConfigManager = &SystemConfigManagerImpl{
			dao:       dao,
			listeners: make(map[int64][]ConfigChangeListener),
		}
		systemConfigManager.startListening()
	})
	return systemConfigManager
}

// AddConfigChangeListener 添加配置变更监听器
func (cm *SystemConfigManagerImpl) AddConfigChangeListener(configID int64, listener ConfigChangeListener) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	if cm.listeners[configID] == nil {
		cm.listeners[configID] = make([]ConfigChangeListener, 0)
	}
	cm.listeners[configID] = append(cm.listeners[configID], listener)
}

// RemoveConfigChangeListener 移除配置变更监听器
func (cm *SystemConfigManagerImpl) RemoveConfigChangeListener(configID int64) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	delete(cm.listeners, configID)
}

// startListening 开始监听配置变更
func (cm *SystemConfigManagerImpl) startListening() {
	// 订阅全局配置变更频道
	cm.pubsub = gredis.Subscribe("config_change:all")

	go func() {
		defer cm.pubsub.Close()

		for {
			msg, err := cm.pubsub.ReceiveMessage(context.Background())
			if err != nil {
				fmt.Printf("配置变更监听错误: %v\n", err)
				time.Sleep(time.Second)
				continue
			}

			// 解析消息
			var changeMsg ConfigChangeMessage
			if err := json.Unmarshal([]byte(msg.Payload), &changeMsg); err != nil {
				fmt.Printf("配置变更消息解析错误: %v\n", err)
				continue
			}

			// 通知监听器
			cm.notifyListeners(changeMsg.ConfigID)
		}
	}()
}

// notifyListeners 通知监听器
func (cm *SystemConfigManagerImpl) notifyListeners(configID int64) {
	cm.mutex.RLock()
	listeners := cm.listeners[configID]
	cm.mutex.RUnlock()

	for _, listener := range listeners {
		go func(l ConfigChangeListener) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("配置变更监听器执行错误: %v\n", r)
				}
			}()
			l(configID)
		}(listener)
	}
}

// RefreshConfig 刷新配置缓存
func (cm *SystemConfigManagerImpl) RefreshConfig(configID int64) {
	cm.dao.ClearConfigCache(configID)
}

// UpdateConfig 更新配置
func (cm *SystemConfigManagerImpl) UpdateConfig(configID int64, config *model.Config) error {
	return cm.dao.UpdateSystemConfig(configID, config)
}

// GetConfig 获取配置
func (cm *SystemConfigManagerImpl) GetConfig(configID int64) model.Config {
	return cm.dao.GetOneSystemConfig(configID)
}
