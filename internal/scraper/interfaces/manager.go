package interfaces

import (
	"time"
)

// IProxyManager 代理管理器接口
type IProxyManager interface {
	// GetProxy 获取可用代理
	GetProxy() (*ProxyInfo, error)

	// ReleaseProxy 释放代理
	ReleaseProxy(proxy *ProxyInfo)

	// MarkProxyFailed 标记代理失效
	MarkProxyFailed(proxy *ProxyInfo, reason string)

	// GetPoolStatus 获取代理池状态
	GetPoolStatus() *ProxyPoolStatus

	// RefreshProxies 刷新代理池
	RefreshProxies() error
}

// IConfigManager 配置管理器接口
type IConfigManager interface {
	// Get 获取配置值
	Get(key string) (interface{}, error)

	// GetString 获取字符串配置
	GetString(key string) (string, error)

	// GetInt 获取整数配置
	GetInt(key string) (int, error)

	// GetBool 获取布尔配置
	GetBool(key string) (bool, error)

	// GetFloat64 获取浮点数配置
	GetFloat64(key string) (float64, error)

	// GetDuration 获取时间间隔配置
	GetDuration(key string) (time.Duration, error)

	// Set 设置配置值
	Set(key string, value interface{}) error

	// Watch 监听配置变化
	Watch(key string, callback func(oldValue, newValue interface{})) error

	// Reload 重新加载配置
	Reload() error
}

// ICacheManager 缓存管理器接口
type ICacheManager interface {
	// Get 获取缓存值
	Get(key string) (interface{}, error)

	// Set 设置缓存值
	Set(key string, value interface{}, ttl time.Duration) error

	// Delete 删除缓存
	Delete(key string) error

	// Exists 检查缓存是否存在
	Exists(key string) bool

	// Clear 清空缓存
	Clear() error

	// GetStats 获取缓存统计信息
	GetStats() *CacheStats
}

// IErrorHandler 错误处理器接口
type IErrorHandler interface {
	// HandleError 处理错误
	HandleError(err error, context map[string]interface{}) *ScrapingError

	// IsRetryable 判断错误是否可重试
	IsRetryable(err error) bool

	// GetRetryDelay 获取重试延迟
	GetRetryDelay(retryCount int) time.Duration

	// LogError 记录错误
	LogError(err *ScrapingError)
}

// ITaskManager 任务管理器接口
type ITaskManager interface {
	// AddTask 添加任务
	AddTask(task *ScrapingTask) error

	// GetTask 获取任务
	GetTask() (*ScrapingTask, error)

	// CompleteTask 完成任务
	CompleteTask(taskID string, result *ScrapingResult) error

	// FailTask 任务失败
	FailTask(taskID string, err error) error

	// GetTaskStatus 获取任务状态
	GetTaskStatus(taskID string) (TaskStatus, error)

	// GetQueueSize 获取队列大小
	GetQueueSize() int

	// Clear 清空任务队列
	Clear() error
}
