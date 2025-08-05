package business

import (
	"buff-go/internal/scraper/core"
	"fmt"
	"sync"
	"time"
)

// PriceChangeEvent 价格变化事件
type PriceChangeEvent struct {
	MarketHashName string    `json:"market_hash_name"`
	Game           string    `json:"game"`
	Type           string    `json:"type"` // buff_buy, buff_sell, steam_buy, steam_sell
	OldPrice       float64   `json:"old_price"`
	NewPrice       float64   `json:"new_price"`
	ChangePercent  float64   `json:"change_percent"`
	Timestamp      time.Time `json:"timestamp"`
}

// PriceAlert 价格警报
type PriceAlert struct {
	ID             string    `json:"id"`
	MarketHashName string    `json:"market_hash_name"`
	Game           string    `json:"game"`
	Type           string    `json:"type"`
	Condition      string    `json:"condition"` // increase, decrease, threshold
	Threshold      float64   `json:"threshold"`
	Percentage     float64   `json:"percentage"`
	CreatedAt      time.Time `json:"created_at"`
	IsActive       bool      `json:"is_active"`
}

// PriceMonitor 价格监控器
type PriceMonitor struct {
	alerts       map[string]*PriceAlert
	alertsMux    sync.RWMutex
	subscribers  []PriceChangeSubscriber
	subsMux      sync.RWMutex
	cacheManager core.ICacheManager
	stats        *MonitorStats
	statsMux     sync.RWMutex
}

// PriceChangeSubscriber 价格变化订阅者接口
type PriceChangeSubscriber interface {
	OnPriceChange(event *PriceChangeEvent) error
}

// MonitorStats 监控统计信息
type MonitorStats struct {
	TotalEvents     int64     `json:"total_events"`
	AlertsTriggered int64     `json:"alerts_triggered"`
	ActiveAlerts    int       `json:"active_alerts"`
	LastEventTime   time.Time `json:"last_event_time"`
	StartTime       time.Time `json:"start_time"`
}

// NewPriceMonitor 创建价格监控器
func NewPriceMonitor(cacheManager core.ICacheManager) *PriceMonitor {
	return &PriceMonitor{
		alerts:       make(map[string]*PriceAlert),
		subscribers:  make([]PriceChangeSubscriber, 0),
		cacheManager: cacheManager,
		stats: &MonitorStats{
			StartTime: time.Now(),
		},
	}
}

// NotifyPriceChange 通知价格变化
func (pm *PriceMonitor) NotifyPriceChange(event *PriceChangeEvent) {
	// 计算变化百分比
	if event.OldPrice > 0 {
		event.ChangePercent = ((event.NewPrice - event.OldPrice) / event.OldPrice) * 100
	}

	// 更新统计信息
	pm.updateStats(event)

	// 检查警报
	pm.checkAlerts(event)

	// 通知订阅者
	pm.notifySubscribers(event)

	// 记录到缓存
	pm.recordEvent(event)
}

// AddAlert 添加价格警报
func (pm *PriceMonitor) AddAlert(alert *PriceAlert) error {
	if alert.ID == "" {
		alert.ID = pm.generateAlertID()
	}

	alert.CreatedAt = time.Now()
	alert.IsActive = true

	pm.alertsMux.Lock()
	pm.alerts[alert.ID] = alert
	pm.alertsMux.Unlock()

	return nil
}

// RemoveAlert 移除价格警报
func (pm *PriceMonitor) RemoveAlert(alertID string) error {
	pm.alertsMux.Lock()
	defer pm.alertsMux.Unlock()

	if _, exists := pm.alerts[alertID]; !exists {
		return fmt.Errorf("alert not found: %s", alertID)
	}

	delete(pm.alerts, alertID)
	return nil
}

// GetAlerts 获取所有警报
func (pm *PriceMonitor) GetAlerts() []*PriceAlert {
	pm.alertsMux.RLock()
	defer pm.alertsMux.RUnlock()

	alerts := make([]*PriceAlert, 0, len(pm.alerts))
	for _, alert := range pm.alerts {
		alerts = append(alerts, alert)
	}

	return alerts
}

// Subscribe 订阅价格变化事件
func (pm *PriceMonitor) Subscribe(subscriber PriceChangeSubscriber) {
	pm.subsMux.Lock()
	pm.subscribers = append(pm.subscribers, subscriber)
	pm.subsMux.Unlock()
}

// Unsubscribe 取消订阅价格变化事件
func (pm *PriceMonitor) Unsubscribe(subscriber PriceChangeSubscriber) {
	pm.subsMux.Lock()
	defer pm.subsMux.Unlock()

	for i, sub := range pm.subscribers {
		if sub == subscriber {
			pm.subscribers = append(pm.subscribers[:i], pm.subscribers[i+1:]...)
			break
		}
	}
}

// GetStats 获取监控统计信息
func (pm *PriceMonitor) GetStats() *MonitorStats {
	pm.statsMux.RLock()
	defer pm.statsMux.RUnlock()

	// 返回统计信息的副本
	stats := *pm.stats

	pm.alertsMux.RLock()
	stats.ActiveAlerts = len(pm.alerts)
	pm.alertsMux.RUnlock()

	return &stats
}

// checkAlerts 检查警报条件
func (pm *PriceMonitor) checkAlerts(event *PriceChangeEvent) {
	pm.alertsMux.RLock()
	defer pm.alertsMux.RUnlock()

	for _, alert := range pm.alerts {
		if !alert.IsActive {
			continue
		}

		// 检查商品和类型匹配
		if alert.MarketHashName != "" && alert.MarketHashName != event.MarketHashName {
			continue
		}
		if alert.Game != "" && alert.Game != event.Game {
			continue
		}
		if alert.Type != "" && alert.Type != event.Type {
			continue
		}

		// 检查警报条件
		triggered := false
		switch alert.Condition {
		case "increase":
			triggered = event.ChangePercent > 0 && event.ChangePercent >= alert.Percentage
		case "decrease":
			triggered = event.ChangePercent < 0 && (-event.ChangePercent) >= alert.Percentage
		case "threshold":
			triggered = event.NewPrice >= alert.Threshold
		}

		if triggered {
			pm.triggerAlert(alert, event)
		}
	}
}

// triggerAlert 触发警报
func (pm *PriceMonitor) triggerAlert(alert *PriceAlert, event *PriceChangeEvent) {
	pm.statsMux.Lock()
	pm.stats.AlertsTriggered++
	pm.statsMux.Unlock()

	// 这里可以实现具体的警报处理逻辑
	// 比如发送通知、记录日志等
	fmt.Printf("🚨 价格警报触发: %s - %s 价格从 %.2f 变为 %.2f (变化: %.2f%%)\n",
		alert.ID, event.MarketHashName, event.OldPrice, event.NewPrice, event.ChangePercent)
}

// notifySubscribers 通知订阅者
func (pm *PriceMonitor) notifySubscribers(event *PriceChangeEvent) {
	pm.subsMux.RLock()
	subscribers := make([]PriceChangeSubscriber, len(pm.subscribers))
	copy(subscribers, pm.subscribers)
	pm.subsMux.RUnlock()

	for _, subscriber := range subscribers {
		go func(sub PriceChangeSubscriber) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("订阅者处理价格变化事件时发生panic: %v\n", r)
				}
			}()

			if err := sub.OnPriceChange(event); err != nil {
				fmt.Printf("订阅者处理价格变化事件失败: %v\n", err)
			}
		}(subscriber)
	}
}

// recordEvent 记录事件到缓存
func (pm *PriceMonitor) recordEvent(event *PriceChangeEvent) {
	if pm.cacheManager == nil {
		return
	}

	key := fmt.Sprintf("price_event:%s:%s:%s", event.Game, event.Type, event.MarketHashName)

	// 记录最新的价格变化事件
	pm.cacheManager.Set(key, event, 24*time.Hour)

	// 记录到历史列表（可选）
	historyKey := fmt.Sprintf("price_history:%s:%s:%s", event.Game, event.Type, event.MarketHashName)
	pm.cacheManager.Set(historyKey, event, 7*24*time.Hour)
}

// updateStats 更新统计信息
func (pm *PriceMonitor) updateStats(event *PriceChangeEvent) {
	pm.statsMux.Lock()
	defer pm.statsMux.Unlock()

	pm.stats.TotalEvents++
	pm.stats.LastEventTime = event.Timestamp
}

// generateAlertID 生成警报ID
func (pm *PriceMonitor) generateAlertID() string {
	return fmt.Sprintf("alert_%d", time.Now().UnixNano())
}

// GetRecentEvents 获取最近的价格变化事件
func (pm *PriceMonitor) GetRecentEvents(game, itemType string, limit int) ([]*PriceChangeEvent, error) {
	if pm.cacheManager == nil {
		return nil, fmt.Errorf("cache manager not available")
	}

	// 这里可以实现从缓存中获取最近事件的逻辑
	// 为了简化，返回空列表
	return []*PriceChangeEvent{}, nil
}

// CreateThresholdAlert 创建阈值警报
func (pm *PriceMonitor) CreateThresholdAlert(marketHashName, game, itemType string, threshold float64) *PriceAlert {
	return &PriceAlert{
		MarketHashName: marketHashName,
		Game:           game,
		Type:           itemType,
		Condition:      "threshold",
		Threshold:      threshold,
		IsActive:       true,
	}
}

// CreatePercentageAlert 创建百分比变化警报
func (pm *PriceMonitor) CreatePercentageAlert(marketHashName, game, itemType, condition string, percentage float64) *PriceAlert {
	return &PriceAlert{
		MarketHashName: marketHashName,
		Game:           game,
		Type:           itemType,
		Condition:      condition, // "increase" or "decrease"
		Percentage:     percentage,
		IsActive:       true,
	}
}
