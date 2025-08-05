package monitor

import (
	"buff-go/internal/dao"
	"buff-go/internal/scraper_new/interfaces"
	"sync"
	"time"
)

// PriceMonitor 价格监控器
type PriceMonitor struct {
	dao          *dao.Dao
	cacheManager interfaces.ICacheManager
	alerts       []PriceAlert
	alertsMux    sync.RWMutex
	running      bool
	runningMux   sync.RWMutex
}

// PriceAlert 价格警报
type PriceAlert struct {
	ID             string    `json:"id"`
	ItemName       string    `json:"item_name"`
	MarketHashName string    `json:"market_hash_name"`
	TargetPrice    float64   `json:"target_price"`
	AlertType      string    `json:"alert_type"` // "above", "below"
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
	LastTriggered  time.Time `json:"last_triggered"`
}

// PriceChange 价格变化
type PriceChange struct {
	ItemName       string    `json:"item_name"`
	MarketHashName string    `json:"market_hash_name"`
	OldPrice       float64   `json:"old_price"`
	NewPrice       float64   `json:"new_price"`
	ChangePercent  float64   `json:"change_percent"`
	Timestamp      time.Time `json:"timestamp"`
}

// NewPriceMonitor 创建价格监控器
func NewPriceMonitor(dao *dao.Dao, cacheManager interfaces.ICacheManager) *PriceMonitor {
	return &PriceMonitor{
		dao:          dao,
		cacheManager: cacheManager,
		alerts:       make([]PriceAlert, 0),
	}
}

// Start 启动价格监控
func (pm *PriceMonitor) Start() error {
	pm.runningMux.Lock()
	defer pm.runningMux.Unlock()

	if pm.running {
		return nil
	}

	pm.running = true
	go pm.monitorLoop()
	return nil
}

// Stop 停止价格监控
func (pm *PriceMonitor) Stop() error {
	pm.runningMux.Lock()
	defer pm.runningMux.Unlock()

	pm.running = false
	return nil
}

// AddAlert 添加价格警报
func (pm *PriceMonitor) AddAlert(alert PriceAlert) error {
	pm.alertsMux.Lock()
	defer pm.alertsMux.Unlock()

	alert.CreatedAt = time.Now()
	alert.IsActive = true
	pm.alerts = append(pm.alerts, alert)
	return nil
}

// RemoveAlert 移除价格警报
func (pm *PriceMonitor) RemoveAlert(alertID string) error {
	pm.alertsMux.Lock()
	defer pm.alertsMux.Unlock()

	for i, alert := range pm.alerts {
		if alert.ID == alertID {
			pm.alerts = append(pm.alerts[:i], pm.alerts[i+1:]...)
			return nil
		}
	}

	return nil
}

// GetAlerts 获取所有警报
func (pm *PriceMonitor) GetAlerts() []PriceAlert {
	pm.alertsMux.RLock()
	defer pm.alertsMux.RUnlock()

	alerts := make([]PriceAlert, len(pm.alerts))
	copy(alerts, pm.alerts)
	return alerts
}

// CheckPriceChanges 检查价格变化
func (pm *PriceMonitor) CheckPriceChanges(itemName string, currentPrice float64) []PriceChange {
	changes := make([]PriceChange, 0)

	// 从缓存获取历史价格
	cacheKey := "price_history:" + itemName
	if cachedPrice, err := pm.cacheManager.Get(cacheKey); err == nil {
		if oldPrice, ok := cachedPrice.(float64); ok {
			if oldPrice != currentPrice {
				changePercent := ((currentPrice - oldPrice) / oldPrice) * 100
				change := PriceChange{
					ItemName:      itemName,
					OldPrice:      oldPrice,
					NewPrice:      currentPrice,
					ChangePercent: changePercent,
					Timestamp:     time.Now(),
				}
				changes = append(changes, change)
			}
		}
	}

	// 更新缓存中的价格
	pm.cacheManager.Set(cacheKey, currentPrice, 24*time.Hour)

	return changes
}

// TriggerAlerts 触发警报检查
func (pm *PriceMonitor) TriggerAlerts(itemName string, currentPrice float64) []PriceAlert {
	pm.alertsMux.RLock()
	defer pm.alertsMux.RUnlock()

	triggeredAlerts := make([]PriceAlert, 0)

	for i, alert := range pm.alerts {
		if !alert.IsActive {
			continue
		}

		if alert.ItemName != itemName && alert.MarketHashName != itemName {
			continue
		}

		shouldTrigger := false
		switch alert.AlertType {
		case "above":
			shouldTrigger = currentPrice >= alert.TargetPrice
		case "below":
			shouldTrigger = currentPrice <= alert.TargetPrice
		}

		if shouldTrigger {
			pm.alerts[i].LastTriggered = time.Now()
			triggeredAlerts = append(triggeredAlerts, pm.alerts[i])
		}
	}

	return triggeredAlerts
}

// monitorLoop 监控循环
func (pm *PriceMonitor) monitorLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		pm.runningMux.RLock()
		running := pm.running
		pm.runningMux.RUnlock()

		if !running {
			break
		}

		select {
		case <-ticker.C:
			pm.performMonitoring()
		}
	}
}

// performMonitoring 执行监控
func (pm *PriceMonitor) performMonitoring() {
	// 这里可以实现具体的价格监控逻辑
	// 比如从数据库获取最新价格，检查变化，触发警报等
}

// GetPriceHistory 获取价格历史
func (pm *PriceMonitor) GetPriceHistory(itemName string, days int) ([]PricePoint, error) {
	// 这里可以实现从数据库获取价格历史的逻辑
	return []PricePoint{}, nil
}

// PricePoint 价格点
type PricePoint struct {
	Price     float64   `json:"price"`
	Timestamp time.Time `json:"timestamp"`
}

// GetMonitorStats 获取监控统计信息
func (pm *PriceMonitor) GetMonitorStats() map[string]interface{} {
	pm.alertsMux.RLock()
	defer pm.alertsMux.RUnlock()

	activeAlerts := 0
	for _, alert := range pm.alerts {
		if alert.IsActive {
			activeAlerts++
		}
	}

	return map[string]interface{}{
		"total_alerts":  len(pm.alerts),
		"active_alerts": activeAlerts,
		"is_running":    pm.running,
		"last_check":    time.Now(),
	}
}

// UpdateAlert 更新警报
func (pm *PriceMonitor) UpdateAlert(alertID string, updates map[string]interface{}) error {
	pm.alertsMux.Lock()
	defer pm.alertsMux.Unlock()

	for i, alert := range pm.alerts {
		if alert.ID == alertID {
			if targetPrice, ok := updates["target_price"].(float64); ok {
				pm.alerts[i].TargetPrice = targetPrice
			}
			if alertType, ok := updates["alert_type"].(string); ok {
				pm.alerts[i].AlertType = alertType
			}
			if isActive, ok := updates["is_active"].(bool); ok {
				pm.alerts[i].IsActive = isActive
			}
			return nil
		}
	}

	return nil
}

// IsRunning 检查是否正在运行
func (pm *PriceMonitor) IsRunning() bool {
	pm.runningMux.RLock()
	defer pm.runningMux.RUnlock()
	return pm.running
}
