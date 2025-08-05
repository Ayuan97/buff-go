package framework

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// BaseScraper 基础抓取器实现
type BaseScraper struct {
	name          string
	config        *ScraperConfig
	status        int32 // 使用atomic操作
	HttpManager   IHTTPClientManager
	ProxyManager  IProxyManager
	ConfigManager IConfigManager
	CacheManager  ICacheManager
	ErrorHandler  IErrorHandler
	TaskManager   ITaskManager

	// 控制相关
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 统计信息
	stats    *ScraperStats
	statsMux sync.RWMutex
}

// ScraperStats 抓取器统计信息
type ScraperStats struct {
	StartTime      time.Time     `json:"start_time"`
	TotalTasks     int64         `json:"total_tasks"`
	CompletedTasks int64         `json:"completed_tasks"`
	FailedTasks    int64         `json:"failed_tasks"`
	SuccessRate    float64       `json:"success_rate"`
	AverageLatency time.Duration `json:"average_latency"`
	TasksPerSecond float64       `json:"tasks_per_second"`
}

// NewBaseScraper 创建基础抓取器
func NewBaseScraper(name string) *BaseScraper {
	return &BaseScraper{
		name:  name,
		stats: &ScraperStats{},
	}
}

// Initialize 初始化抓取器
func (bs *BaseScraper) Initialize(config *ScraperConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	bs.config = config
	bs.name = config.Name

	// 初始化统计信息
	bs.stats.StartTime = time.Now()

	return nil
}

// SetManagers 设置管理器
func (bs *BaseScraper) SetManagers(
	httpManager IHTTPClientManager,
	proxyManager IProxyManager,
	configManager IConfigManager,
	cacheManager ICacheManager,
	errorHandler IErrorHandler,
	taskManager ITaskManager,
) {
	bs.HttpManager = httpManager
	bs.ProxyManager = proxyManager
	bs.ConfigManager = configManager
	bs.CacheManager = cacheManager
	bs.ErrorHandler = errorHandler
	bs.TaskManager = taskManager
}

// Start 开始抓取
func (bs *BaseScraper) Start(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&bs.status, int32(StatusStopped), int32(StatusRunning)) {
		return fmt.Errorf("scraper is already running")
	}

	bs.ctx, bs.cancel = context.WithCancel(ctx)

	// 启动工作协程
	for i := 0; i < bs.config.MaxConcurrency; i++ {
		bs.wg.Add(1)
		go bs.worker(i)
	}

	return nil
}

// Stop 停止抓取
func (bs *BaseScraper) Stop() error {
	if !atomic.CompareAndSwapInt32(&bs.status, int32(StatusRunning), int32(StatusStopped)) {
		return fmt.Errorf("scraper is not running")
	}

	if bs.cancel != nil {
		bs.cancel()
	}

	// 等待所有工作协程结束
	bs.wg.Wait()

	return nil
}

// GetStatus 获取抓取状态
func (bs *BaseScraper) GetStatus() ScraperStatus {
	return ScraperStatus(atomic.LoadInt32(&bs.status))
}

// GetName 获取抓取器名称
func (bs *BaseScraper) GetName() string {
	return bs.name
}

// ProcessTask 处理单个抓取任务
func (bs *BaseScraper) ProcessTask(task *ScrapingTask) (*ScrapingResult, error) {
	if task == nil {
		return nil, fmt.Errorf("task cannot be nil")
	}

	startTime := time.Now()
	result := &ScrapingResult{
		TaskID:      task.ID,
		CompletedAt: time.Now(),
	}

	// 更新统计信息
	atomic.AddInt64(&bs.stats.TotalTasks, 1)

	// 创建HTTP请求
	req, err := bs.createHTTPRequest(task)
	if err != nil {
		result.Error = err
		atomic.AddInt64(&bs.stats.FailedTasks, 1)
		return result, err
	}

	// 执行请求
	options := []RequestOption{
		WithTimeout(bs.config.Timeout),
		WithRetry(bs.config.RetryCount, time.Second),
		WithProxy(bs.config.UseProxy),
	}

	if bs.config.EnableCache {
		cacheKey := fmt.Sprintf("%s:%s", bs.name, task.URL)
		options = append(options, WithCache(cacheKey, bs.config.CacheTTL))
	}

	resp, err := bs.HttpManager.DoRequest(req, options...)
	if err != nil {
		scrapingErr := bs.ErrorHandler.HandleError(err, map[string]interface{}{
			"url":    task.URL,
			"method": task.Method,
		})
		result.Error = scrapingErr
		atomic.AddInt64(&bs.stats.FailedTasks, 1)
		return result, scrapingErr
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := bs.readResponse(resp)
	if err != nil {
		result.Error = err
		atomic.AddInt64(&bs.stats.FailedTasks, 1)
		return result, err
	}

	// 填充结果
	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header
	result.Body = body
	result.Duration = time.Since(startTime)

	// 更新统计信息
	atomic.AddInt64(&bs.stats.CompletedTasks, 1)
	bs.updateStats(result.Duration)

	return result, nil
}

// GetStats 获取统计信息
func (bs *BaseScraper) GetStats() *ScraperStats {
	bs.statsMux.RLock()
	defer bs.statsMux.RUnlock()

	stats := *bs.stats
	stats.TotalTasks = atomic.LoadInt64(&bs.stats.TotalTasks)
	stats.CompletedTasks = atomic.LoadInt64(&bs.stats.CompletedTasks)
	stats.FailedTasks = atomic.LoadInt64(&bs.stats.FailedTasks)

	// 计算成功率
	if stats.TotalTasks > 0 {
		stats.SuccessRate = float64(stats.CompletedTasks) / float64(stats.TotalTasks)
	}

	// 计算每秒任务数
	duration := time.Since(stats.StartTime)
	if duration.Seconds() > 0 {
		stats.TasksPerSecond = float64(stats.TotalTasks) / duration.Seconds()
	}

	return &stats
}

// worker 工作协程
func (bs *BaseScraper) worker(workerID int) {
	defer bs.wg.Done()

	for {
		select {
		case <-bs.ctx.Done():
			return
		default:
			// 获取任务
			task, err := bs.TaskManager.GetTask()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}

			// 处理任务
			result, err := bs.ProcessTask(task)

			// 处理结果
			if err != nil {
				bs.TaskManager.FailTask(task.ID, err)
			} else {
				bs.TaskManager.CompleteTask(task.ID, result)
			}

			// 请求延迟
			if bs.config.RequestDelay > 0 {
				time.Sleep(bs.config.RequestDelay)
			}
		}
	}
}

// createHTTPRequest 创建HTTP请求
func (bs *BaseScraper) createHTTPRequest(task *ScrapingTask) (*http.Request, error) {
	req, err := http.NewRequestWithContext(bs.ctx, task.Method, task.URL, bytes.NewReader(task.Body))
	if err != nil {
		return nil, err
	}

	// 设置请求头
	for key, value := range task.Headers {
		req.Header.Set(key, value)
	}

	// 设置默认请求头
	if bs.config.CustomHeaders != nil {
		for key, value := range bs.config.CustomHeaders {
			if req.Header.Get(key) == "" {
				req.Header.Set(key, value)
			}
		}
	}

	// 设置默认User-Agent
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/105.0.0.0 Safari/537.36")
	}

	return req, nil
}

// readResponse 读取响应
func (bs *BaseScraper) readResponse(resp *http.Response) ([]byte, error) {
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	return body, nil
}

// updateStats 更新统计信息
func (bs *BaseScraper) updateStats(duration time.Duration) {
	bs.statsMux.Lock()
	defer bs.statsMux.Unlock()

	// 计算平均延迟（简单实现）
	if bs.stats.AverageLatency == 0 {
		bs.stats.AverageLatency = duration
	} else {
		bs.stats.AverageLatency = (bs.stats.AverageLatency + duration) / 2
	}
}
