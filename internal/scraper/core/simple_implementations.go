package core

import (
	"fmt"
	"net/http"
	"time"
)

// 简单实现，用于解决循环导入问题

type simpleProxyManager struct{}

func (s *simpleProxyManager) GetProxy() (*ProxyInfo, error) {
	return nil, fmt.Errorf("proxy not available")
}

func (s *simpleProxyManager) ReleaseProxy(proxy *ProxyInfo) {}

func (s *simpleProxyManager) MarkProxyFailed(proxy *ProxyInfo, reason string) {}

func (s *simpleProxyManager) GetPoolStatus() *ProxyPoolStatus {
	return &ProxyPoolStatus{}
}

func (s *simpleProxyManager) RefreshProxies() error {
	return nil
}

type simpleErrorHandler struct{}

func (s *simpleErrorHandler) HandleError(err error, context map[string]interface{}) *ScrapingError {
	return &ScrapingError{
		Type:      ErrorTypeSystem,
		Message:   err.Error(),
		Retryable: false,
	}
}

func (s *simpleErrorHandler) IsRetryable(err error) bool {
	return false
}

func (s *simpleErrorHandler) GetRetryDelay(retryCount int) time.Duration {
	return time.Second
}

func (s *simpleErrorHandler) LogError(err *ScrapingError) {}

type simpleHTTPClientManager struct{}

func (s *simpleHTTPClientManager) GetClient(config *ClientConfig) (*http.Client, error) {
	return &http.Client{}, nil
}

func (s *simpleHTTPClientManager) DoRequest(req *http.Request, options ...RequestOption) (*http.Response, error) {
	client := &http.Client{}
	return client.Do(req)
}

func (s *simpleHTTPClientManager) ReleaseClient(client *http.Client) {}

func (s *simpleHTTPClientManager) GetStats() *ClientStats {
	return &ClientStats{}
}

type simpleConfigManager struct{}

func (s *simpleConfigManager) Get(key string) (interface{}, error) {
	return nil, fmt.Errorf("config not found")
}

func (s *simpleConfigManager) GetString(key string) (string, error) {
	return "", fmt.Errorf("config not found")
}

func (s *simpleConfigManager) GetInt(key string) (int, error) {
	return 0, fmt.Errorf("config not found")
}

func (s *simpleConfigManager) GetBool(key string) (bool, error) {
	return false, fmt.Errorf("config not found")
}

func (s *simpleConfigManager) GetFloat64(key string) (float64, error) {
	return 0, fmt.Errorf("config not found")
}

func (s *simpleConfigManager) GetDuration(key string) (time.Duration, error) {
	return 0, fmt.Errorf("config not found")
}

func (s *simpleConfigManager) Set(key string, value interface{}) error {
	return nil
}

func (s *simpleConfigManager) Watch(key string, callback func(oldValue, newValue interface{})) error {
	return nil
}

func (s *simpleConfigManager) Reload() error {
	return nil
}

type simpleCacheManager struct{}

func (s *simpleCacheManager) Get(key string) (interface{}, error) {
	return nil, fmt.Errorf("cache miss")
}

func (s *simpleCacheManager) Set(key string, value interface{}, ttl time.Duration) error {
	return nil
}

func (s *simpleCacheManager) Delete(key string) error {
	return nil
}

func (s *simpleCacheManager) Exists(key string) bool {
	return false
}

func (s *simpleCacheManager) Clear() error {
	return nil
}

func (s *simpleCacheManager) GetStats() *CacheStats {
	return &CacheStats{}
}

type simpleTaskManager struct{}

func (s *simpleTaskManager) AddTask(task *ScrapingTask) error {
	return nil
}

func (s *simpleTaskManager) GetTask() (*ScrapingTask, error) {
	return nil, fmt.Errorf("no tasks available")
}

func (s *simpleTaskManager) CompleteTask(taskID string, result *ScrapingResult) error {
	return nil
}

func (s *simpleTaskManager) FailTask(taskID string, err error) error {
	return nil
}

func (s *simpleTaskManager) GetTaskStatus(taskID string) (TaskStatus, error) {
	return TaskStatusPending, nil
}

func (s *simpleTaskManager) GetQueueSize() int {
	return 0
}

func (s *simpleTaskManager) Clear() error {
	return nil
}
