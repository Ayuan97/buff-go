package base

import (
	"buff-go/global"
	"buff-go/internal/scraper/interfaces"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ScraperStats 抓取器统计信息
type ScraperStats struct {
	StartTime      time.Time     `json:"start_time"`      // 开始时间
	TotalTasks     int64         `json:"total_tasks"`     // 总任务数
	CompletedTasks int64         `json:"completed_tasks"` // 完成任务数
	FailedTasks    int64         `json:"failed_tasks"`    // 失败任务数
	AverageLatency time.Duration `json:"average_latency"` // 平均延迟
}

// BaseScraper 基础抓取器实现
type BaseScraper struct {
	name          string                        // 抓取器名称
	config        *interfaces.ScraperConfig     // 抓取器配置
	status        int32                         // 使用atomic操作
	platform      interfaces.Platform           // 抓取器对应的平台
	HttpManager   interfaces.IHTTPClientManager // HTTP客户端管理器
	ProxyManager  interfaces.IProxyManager      // 代理管理器
	ConfigManager interfaces.IConfigManager     // 配置管理器
	CacheManager  interfaces.ICacheManager      // 缓存管理器
	ErrorHandler  interfaces.IErrorHandler      // 错误处理接口
	TaskManager   interfaces.ITaskManager       // 任务管理器

	// 控制相关
	Ctx    context.Context    // 上下文
	Cancel context.CancelFunc // 取消函数
	wg     sync.WaitGroup     // 等待组

	// 统计信息
	stats    *ScraperStats // 统计信息
	statsMux sync.RWMutex  // 读写锁

	// 任务处理器接口，用于支持多态调用
	taskProcessor interfaces.IScraper // 任务处理器接口
}

// NewBaseScraper 创建基础抓取器
func NewBaseScraper(name string) *BaseScraper {
	return &BaseScraper{
		name:     name,                            // 设置抓取器名称
		platform: interfaces.PlatformBuff,         // 默认平台
		status:   int32(interfaces.StatusStopped), // 设置抓取器状态
		stats:    &ScraperStats{},                 // 设置统计信息
	}
}

// Initialize 初始化抓取器
func (bs *BaseScraper) Initialize(config *interfaces.ScraperConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	bs.config = config    // 设置抓取器配置
	bs.name = config.Name // 设置抓取器名称

	// 初始化统计信息
	bs.stats.StartTime = time.Now()

	return nil
}

// SetManagers 设置管理器
func (bs *BaseScraper) SetManagers(
	httpManager interfaces.IHTTPClientManager, // HTTP客户端管理器
	proxyManager interfaces.IProxyManager, // 代理管理器
	configManager interfaces.IConfigManager, // 配置管理器
	cacheManager interfaces.ICacheManager, // 缓存管理器
	errorHandler interfaces.IErrorHandler, // 错误处理接口
	taskManager interfaces.ITaskManager, // 任务管理器
) {
	bs.HttpManager = httpManager     // 设置HTTP客户端管理器
	bs.ProxyManager = proxyManager   // 设置代理管理器
	bs.ConfigManager = configManager // 设置配置管理器
	bs.CacheManager = cacheManager   // 设置缓存管理器
	bs.ErrorHandler = errorHandler   // 设置错误处理接口
	bs.TaskManager = taskManager     // 设置任务管理器
}

// SetTaskProcessor 设置任务处理器
func (bs *BaseScraper) SetTaskProcessor(processor interfaces.IScraper) {
	bs.taskProcessor = processor // 设置任务处理器
}

// SetPlatform 设置抓取器平台
func (bs *BaseScraper) SetPlatform(platform interfaces.Platform) {
	bs.platform = platform
}

// GetPlatform 获取抓取器平台
func (bs *BaseScraper) GetPlatform() interfaces.Platform {
	return bs.platform
}

// Start 开始抓取
func (bs *BaseScraper) Start(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&bs.status, int32(interfaces.StatusStopped), int32(interfaces.StatusRunning)) {
		return fmt.Errorf("scraper is already running")
	}

	bs.Ctx, bs.Cancel = context.WithCancel(ctx) // 设置上下文和取消函数

	// 启动工作协程
	for i := 0; i < bs.config.MaxConcurrency; i++ {
		bs.wg.Add(1)    // 添加工作协程
		go bs.worker(i) // 启动工作协程
	}

	return nil
}

// Stop 停止抓取
func (bs *BaseScraper) Stop() error {
	// 如果抓取器正在运行，则停止抓取
	if !atomic.CompareAndSwapInt32(&bs.status, int32(interfaces.StatusRunning), int32(interfaces.StatusStopped)) {
		return fmt.Errorf("scraper is not running")
	}

	if bs.Cancel != nil { // 如果取消函数不为空，则调用取消函数
		bs.Cancel()
	}

	bs.wg.Wait()
	return nil
}

// GetStatus 获取抓取状态
func (bs *BaseScraper) GetStatus() interfaces.ScraperStatus {
	return interfaces.ScraperStatus(atomic.LoadInt32(&bs.status))
}

// GetName 获取抓取器名称
func (bs *BaseScraper) GetName() string {
	return bs.name
}

// ProcessTask 处理单个抓取任务
func (bs *BaseScraper) ProcessTask(task *interfaces.ScrapingTask) (*interfaces.ScrapingResult, error) {
	if task == nil {
		return nil, fmt.Errorf("task cannot be nil")
	}

	startTime := time.Now()

	// 记录任务开始处理
	global.Logger.WithFields(map[string]interface{}{
		"scraper":    bs.name,
		"task_id":    task.ID,
		"url":        task.URL,
		"method":     task.Method,
		"start_time": startTime.Format("2006-01-02 15:04:05.000"),
	}).Info("[BaseScraper] 开始处理任务")

	result := &interfaces.ScrapingResult{
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

		global.Logger.WithFields(map[string]interface{}{
			"scraper": bs.name,
			"task_id": task.ID,
			"error":   err.Error(),
		}).Error("[BaseScraper] 创建HTTP请求失败")

		return result, err
	}

	// 执行请求
	options := []interfaces.RequestOption{
		interfaces.WithTimeout(bs.config.Timeout),               // 设置请求超时时间
		interfaces.WithRetry(bs.config.RetryCount, time.Second), // 设置重试次数和重试间隔
		interfaces.WithProxy(bs.config.UseProxy),                // 设置是否使用代理
	}

	if bs.config.EnableCache {
		cacheKey := fmt.Sprintf("%s:%s", bs.name, task.URL)                           // 设置缓存键
		options = append(options, interfaces.WithCache(cacheKey, bs.config.CacheTTL)) // 设置缓存时间
	}

	global.Logger.WithFields(map[string]interface{}{
		"scraper":     bs.name,
		"task_id":     task.ID,
		"url":         task.URL,
		"timeout":     bs.config.Timeout.String(),
		"retry_count": bs.config.RetryCount,
		"use_proxy":   bs.config.UseProxy,
		"use_cache":   bs.config.EnableCache,
	}).Info("[BaseScraper] 发送HTTP请求")

	resp, err := bs.HttpManager.DoRequest(req, options...)
	if err != nil {
		result.Error = err
		atomic.AddInt64(&bs.stats.FailedTasks, 1)

		global.Logger.WithFields(map[string]interface{}{
			"scraper":  bs.name,
			"task_id":  task.ID,
			"url":      task.URL,
			"error":    err.Error(),
			"duration": time.Since(startTime).String(),
		}).Error("[BaseScraper] HTTP请求失败")

		return result, err
	}
	defer resp.Body.Close()

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Error = err
		atomic.AddInt64(&bs.stats.FailedTasks, 1)

		global.Logger.WithFields(map[string]interface{}{
			"scraper": bs.name,
			"task_id": task.ID,
			"error":   err.Error(),
		}).Error("[BaseScraper] 读取响应体失败")

		return result, err
	}

	result.StatusCode = resp.StatusCode
	result.Headers = resp.Header
	result.Body = body
	result.Duration = time.Since(startTime)

	// 记录请求成功
	global.Logger.WithFields(map[string]interface{}{
		"scraper":     bs.name,
		"task_id":     task.ID,
		"url":         task.URL,
		"status_code": resp.StatusCode,
		"body_size":   len(body),
		"duration":    result.Duration.String(),
		"end_time":    time.Now().Format("2006-01-02 15:04:05.000"),
	}).Info("[BaseScraper] 任务处理完成")

	atomic.AddInt64(&bs.stats.CompletedTasks, 1)
	return result, nil
}

// createHTTPRequest 创建HTTP请求
func (bs *BaseScraper) createHTTPRequest(task *interfaces.ScrapingTask) (*http.Request, error) {
	var body io.Reader
	if len(task.Body) > 0 {
		body = bytes.NewReader(task.Body)
	}

	req, err := http.NewRequest(task.Method, task.URL, body)
	if err != nil {
		return nil, err
	}

	// 设置请求头
	for key, value := range task.Headers {
		req.Header.Set(key, value)
	}

	// 设置配置中的自定义请求头
	if bs.config != nil && bs.config.CustomHeaders != nil {
		for key, value := range bs.config.CustomHeaders {
			req.Header.Set(key, value)
		}
	}

	// 设置平台信息到请求头，用于代理选择
	req.Header.Set("X-Platform", string(bs.platform))

	return req, nil
}

// worker 工作协程
func (bs *BaseScraper) worker(workerID int) {
	defer bs.wg.Done()

	for {
		select {
		case <-bs.Ctx.Done():
			return
		default:
			// 获取任务
			task, err := bs.TaskManager.GetTask()
			if err != nil {
				time.Sleep(time.Second)
				continue
			}

			// 处理任务 - 使用taskProcessor来支持多态调用
			var result *interfaces.ScrapingResult
			if bs.taskProcessor != nil {
				result, err = bs.taskProcessor.ProcessTask(task)
			} else {
				result, err = bs.ProcessTask(task)
			}

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

// GetStats 获取统计信息
func (bs *BaseScraper) GetStats() *ScraperStats {
	bs.statsMux.RLock()
	defer bs.statsMux.RUnlock()

	stats := *bs.stats
	stats.TotalTasks = atomic.LoadInt64(&bs.stats.TotalTasks)
	stats.CompletedTasks = atomic.LoadInt64(&bs.stats.CompletedTasks)
	stats.FailedTasks = atomic.LoadInt64(&bs.stats.FailedTasks)

	return &stats
}
