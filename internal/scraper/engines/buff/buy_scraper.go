package buff

import (
	"buff-go/global"
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"buff-go/internal/scraper/core/base"
	"buff-go/internal/scraper/interfaces"
	"buff-go/pkg/gredis"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// BuffBuyData Buff买入数据结构
type BuffBuyData struct {
	Code   string `json:"code"`
	Result struct {
		Items []BuffBuyItem `json:"items"`
	} `json:"data"`
}

// BuffBuyItem Buff买入商品项
type BuffBuyItem struct {
	Id             int    `json:"id"`
	Name           string `json:"name"`
	MarketHashName string `json:"market_hash_name"`
	BuyMaxPrice    string `json:"buy_max_price"`
	BuyNum         int    `json:"buy_num"`
	Appid          int    `json:"appid"`
}

// BuffBuyScraper Buff买入抓取器
type BuffBuyScraper struct {
	*base.BaseScraper
	dao           *dao.Dao
	config        *BuffBuyConfig
	accountTasks  map[int64]*AccountTask // 账号任务映射，使用int64匹配model.BuffUser.ID
	tasksMux      sync.RWMutex           // 任务锁
	proxyManager  interfaces.IProxyManager
	clientManager interfaces.IHTTPClientManager
}

// AccountTask 账号抓取任务
type AccountTask struct {
	Account    *model.BuffUser
	ProxyInfo  *interfaces.ProxyInfo
	Client     *http.Client
	IsRunning  bool
	LastActive time.Time
	ProxyKey   string
	Cancel     context.CancelFunc
}

// BuffBuyConfig Buff买入抓取器配置
type BuffBuyConfig struct {
	Game     string  `json:"game"`
	AppID    int     `json:"app_id"`
	MinPrice float64 `json:"min_price"`
	MaxPrice float64 `json:"max_price"`
	PageNum  int     `json:"page_num"`
}

// NewBuffBuyScraper 创建Buff买入抓取器
func NewBuffBuyScraper(dao *dao.Dao) *BuffBuyScraper {
	baseScraper := base.NewBaseScraper("buff_buy")
	return &BuffBuyScraper{
		BaseScraper:  baseScraper,
		dao:          dao,
		accountTasks: make(map[int64]*AccountTask),
		config: &BuffBuyConfig{
			Game:     "csgo",
			AppID:    730,
			MinPrice: 0.01,
			MaxPrice: 1000.0,
			PageNum:  10,
		},
	}
}

// Initialize 初始化抓取器
func (bs *BuffBuyScraper) Initialize(config *interfaces.ScraperConfig) error {
	if err := bs.BaseScraper.Initialize(config); err != nil {
		return err
	}

	// 可以在这里设置特定的配置
	return nil
}

// SetManagers 设置管理器
func (bs *BuffBuyScraper) SetManagers(
	httpManager interfaces.IHTTPClientManager,
	proxyManager interfaces.IProxyManager,
	configManager interfaces.IConfigManager,
	cacheManager interfaces.ICacheManager,
	errorHandler interfaces.IErrorHandler,
	taskManager interfaces.ITaskManager,
) {
	bs.BaseScraper.SetManagers(httpManager, proxyManager, configManager, cacheManager, errorHandler, taskManager)
	// 保存管理器引用用于多账号抓取
	bs.proxyManager = proxyManager
	bs.clientManager = httpManager
	// 设置任务处理器为自己，这样worker就会调用BuffBuyScraper的ProcessTask方法
	bs.BaseScraper.SetTaskProcessor(bs)
}

// Start 开始抓取
func (bs *BuffBuyScraper) Start(ctx context.Context) error {
	if err := bs.BaseScraper.Start(ctx); err != nil {
		return err
	}

	// 启动多账号抓取逻辑
	go bs.startMultiAccountScraping()
	return nil
}

// ProcessTask 处理抓取任务
func (bs *BuffBuyScraper) ProcessTask(task *interfaces.ScrapingTask) (*interfaces.ScrapingResult, error) {
	// 执行HTTP请求
	result, err := bs.BaseScraper.ProcessTask(task)
	if err != nil {
		return result, err
	}

	// 处理响应数据
	if err := bs.processResponse(result); err != nil {
		result.Error = err
		return result, err
	}

	return result, nil
}

// startMultiAccountScraping 开始多账号抓取
func (bs *BuffBuyScraper) startMultiAccountScraping() {
	ticker := time.NewTicker(10 * time.Second) // 检查账号状态的间隔
	defer ticker.Stop()

	global.Logger.WithFields(map[string]interface{}{
		"scraper":   "buff_buy",
		"interval":  "10s",
		"pages":     bs.config.PageNum,
		"game":      bs.config.Game,
		"min_price": bs.config.MinPrice,
		"max_price": bs.config.MaxPrice,
	}).Info("[BuffBuyScraper] 开始多账号抓取循环")

	for {
		select {
		case <-bs.BaseScraper.Ctx.Done():
			global.Logger.WithFields(map[string]interface{}{
				"scraper": "buff_buy",
			}).Info("[BuffBuyScraper] 收到停止信号，停止所有账号抓取任务")
			bs.stopAllAccountTasks()
			return
		case <-ticker.C:
			bs.manageAccountTasks()
		}
	}
}

// manageAccountTasks 管理账号抓取任务
func (bs *BuffBuyScraper) manageAccountTasks() {
	// 获取所有可用账号
	accounts, err := bs.dao.GetBuffUserList()
	if err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper": "buff_buy",
			"error":   err.Error(),
		}).Error("[BuffBuyScraper] 获取账号列表失败")
		return
	}

	global.Logger.WithFields(map[string]interface{}{
		"scraper":        "buff_buy",
		"total_accounts": len(accounts),
	}).Debug("[BuffBuyScraper] 开始管理账号任务")

	bs.tasksMux.Lock()
	defer bs.tasksMux.Unlock()

	// 为每个状态为0（空闲）的账号创建抓取任务
	for _, account := range accounts {
		if account.Status == 0 { // 账号空闲
			if _, exists := bs.accountTasks[account.ID]; !exists {
				// 创建新的账号抓取任务
				if err := bs.createAccountTask(account); err != nil {
					global.Logger.WithFields(map[string]interface{}{
						"scraper":    "buff_buy",
						"account_id": account.ID,
						"account":    account.Account,
						"error":      err.Error(),
					}).Error("[BuffBuyScraper] 创建账号抓取任务失败")
				}
			}
		}
	}

	// 清理已完成或失效的任务
	bs.cleanupInactiveTasks()
}

// createAccountTask 创建账号抓取任务
func (bs *BuffBuyScraper) createAccountTask(account *model.BuffUser) error {
	// 获取代理
	proxy, err := bs.proxyManager.GetProxyForPlatform(interfaces.PlatformBuff)
	if err != nil {
		return fmt.Errorf("获取代理失败: %v", err)
	}

	// 创建HTTP客户端配置
	clientConfig := &interfaces.ClientConfig{
		Timeout:         30 * time.Second,
		MaxIdleConns:    10,
		MaxConnsPerHost: 5,
		ProxyURL:        proxy.URL,
		FollowRedirect:  true,
	}

	// 获取HTTP客户端
	client, err := bs.clientManager.GetClient(clientConfig)
	if err != nil {
		bs.proxyManager.ReleaseProxy(proxy)
		return fmt.Errorf("创建HTTP客户端失败: %v", err)
	}

	// 设置账号Cookie
	if err := bs.setAccountCookies(client, account); err != nil {
		bs.proxyManager.ReleaseProxy(proxy)
		return fmt.Errorf("设置账号Cookie失败: %v", err)
	}

	// 创建任务上下文
	taskCtx, cancel := context.WithCancel(bs.BaseScraper.Ctx)

	// 生成代理键
	proxyKey := fmt.Sprintf("buff_proxy_%s_%d", proxy.ID, account.ID)
	gredis.Set(proxyKey, account.ID, 0)

	// 创建账号任务
	accountTask := &AccountTask{
		Account:    account,
		ProxyInfo:  proxy,
		Client:     client,
		IsRunning:  true,
		LastActive: time.Now(),
		ProxyKey:   proxyKey,
		Cancel:     cancel,
	}

	// 保存任务
	bs.accountTasks[account.ID] = accountTask

	// 更新账号状态为使用中
	if err := bs.dao.UpdateBuffUserStatus(int(account.ID), 1); err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"account_id": account.ID,
			"error":      err.Error(),
		}).Warn("[BuffBuyScraper] 更新账号状态失败")
	}

	// 启动账号抓取协程
	go bs.runAccountTask(taskCtx, accountTask)

	global.Logger.WithFields(map[string]interface{}{
		"scraper":    "buff_buy",
		"account_id": account.ID,
		"account":    account.Account,
		"proxy":      proxy.URL,
		"proxy_key":  proxyKey,
	}).Info("[BuffBuyScraper] 创建账号抓取任务成功")

	return nil
}

// setAccountCookies 为HTTP客户端设置账号Cookie
func (bs *BuffBuyScraper) setAccountCookies(client *http.Client, account *model.BuffUser) error {
	if client.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return fmt.Errorf("创建Cookie Jar失败: %v", err)
		}
		client.Jar = jar
	}

	// 解析Buff网站URL
	buffURL, err := url.Parse("https://buff.163.com")
	if err != nil {
		return fmt.Errorf("解析Buff URL失败: %v", err)
	}

	// 设置账号相关的Cookie
	cookies := []*http.Cookie{
		{Name: "Device-Id", Value: account.DeviceId, Domain: ".buff.163.com"},
		{Name: "Locale-Supported", Value: "zh-Hans", Domain: ".buff.163.com"},
		{Name: "csrf_token", Value: account.CsrfToken, Domain: ".buff.163.com"},
		{Name: "game", Value: "csgo", Domain: ".buff.163.com"},
		{Name: "remember_me", Value: account.RememberMe, Domain: ".buff.163.com"},
		{Name: "session", Value: account.Sessionid, Domain: ".buff.163.com"},
	}

	client.Jar.SetCookies(buffURL, cookies)

	global.Logger.WithFields(map[string]interface{}{
		"scraper":    "buff_buy",
		"account_id": account.ID,
		"account":    account.Account,
		"cookies":    len(cookies),
	}).Debug("[BuffBuyScraper] 设置账号Cookie成功")

	return nil
}

// runAccountTask 运行账号抓取任务
func (bs *BuffBuyScraper) runAccountTask(ctx context.Context, task *AccountTask) {
	defer func() {
		// 任务结束时清理资源
		bs.cleanupAccountTask(task)
	}()

	ticker := time.NewTicker(60 * time.Second) // 每60秒抓取一轮
	defer ticker.Stop()

	global.Logger.WithFields(map[string]interface{}{
		"scraper":    "buff_buy",
		"account_id": task.Account.ID,
		"account":    task.Account.Account,
		"proxy":      task.ProxyInfo.URL,
	}).Info("[BuffBuyScraper] 开始账号抓取任务")

	for {
		select {
		case <-ctx.Done():
			global.Logger.WithFields(map[string]interface{}{
				"scraper":    "buff_buy",
				"account_id": task.Account.ID,
				"account":    task.Account.Account,
			}).Info("[BuffBuyScraper] 账号抓取任务收到停止信号")
			return
		case <-ticker.C:
			// 检查账号状态是否仍然有效
			if !bs.isAccountTaskValid(task) {
				global.Logger.WithFields(map[string]interface{}{
					"scraper":    "buff_buy",
					"account_id": task.Account.ID,
					"account":    task.Account.Account,
				}).Info("[BuffBuyScraper] 账号任务已失效，停止抓取")
				return
			}

			// 执行抓取
			bs.performAccountScraping(task)
			task.LastActive = time.Now()
		}
	}
}

// performAccountScraping 执行账号抓取
func (bs *BuffBuyScraper) performAccountScraping(task *AccountTask) {
	startTime := time.Now()

	global.Logger.WithFields(map[string]interface{}{
		"scraper":    "buff_buy",
		"account_id": task.Account.ID,
		"account":    task.Account.Account,
		"start_time": startTime.Format("2006-01-02 15:04:05.000"),
	}).Info("[BuffBuyScraper] 开始账号抓取")

	// 获取系统配置
	system := bs.dao.GetOneSystem(1)
	var config model.Config
	var game string

	if system.SystemType == 1 {
		config = bs.dao.GetOneSystemConfig(1) // csgo
		game = "csgo"
	} else {
		config = bs.dao.GetOneSystemConfig(2) // dota2
		game = "dota2"
	}

	// 并发抓取多个页面
	var wg sync.WaitGroup
	for i := 1; i <= config.BuffPageNum; i++ {
		// 检查系统配置是否变更
		currentSystem := bs.dao.GetOneSystem(1)
		if currentSystem.SystemType != config.ID {
			global.Logger.WithFields(map[string]interface{}{
				"scraper":    "buff_buy",
				"account_id": task.Account.ID,
			}).Info("[BuffBuyScraper] 系统配置已变更，停止当前抓取")
			break
		}

		wg.Add(1)
		go func(pageNum int) {
			defer wg.Done()
			bs.scrapePageForAccount(task, game, pageNum, config)
		}(i)

		// 控制并发数，避免过多请求
		if i%3 == 0 {
			time.Sleep(1 * time.Second)
		}
	}

	wg.Wait()

	duration := time.Since(startTime)
	global.Logger.WithFields(map[string]interface{}{
		"scraper":    "buff_buy",
		"account_id": task.Account.ID,
		"account":    task.Account.Account,
		"duration":   duration.String(),
		"end_time":   time.Now().Format("2006-01-02 15:04:05.000"),
	}).Info("[BuffBuyScraper] 账号抓取完成")
}

// scrapePageForAccount 为指定账号抓取单个页面
func (bs *BuffBuyScraper) scrapePageForAccount(task *AccountTask, game string, pageNum int, config model.Config) {
	url := fmt.Sprintf("https://buff.163.com/api/market/goods/buying?game=%s&page_num=%d&min_price=%v&max_price=%v&sort_by=price.desc&page_size=80&use_suggestion=0&_=%d",
		game, pageNum, config.MinPrice, config.MaxPrice, time.Now().UnixNano()/1e6)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"account_id": task.Account.ID,
			"page":       pageNum,
			"error":      err.Error(),
		}).Error("[BuffBuyScraper] 创建HTTP请求失败")
		return
	}

	// 设置请求头
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://buff.163.com/")

	// 执行请求
	resp, err := task.Client.Do(req)
	if err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"account_id": task.Account.ID,
			"page":       pageNum,
			"error":      err.Error(),
		}).Error("[BuffBuyScraper] HTTP请求失败")

		// 标记代理失败
		if bs.proxyManager != nil {
			bs.proxyManager.MarkProxyFailedForPlatform(task.ProxyInfo, interfaces.PlatformBuff, err.Error())
		}
		return
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"account_id": task.Account.ID,
			"page":       pageNum,
			"error":      err.Error(),
		}).Error("[BuffBuyScraper] 读取响应失败")
		return
	}

	// 处理响应数据
	result := &interfaces.ScrapingResult{
		TaskID:      fmt.Sprintf("buff_buy_account_%d_page_%d", task.Account.ID, pageNum),
		StatusCode:  resp.StatusCode,
		Body:        body,
		Headers:     resp.Header,
		CompletedAt: time.Now(),
		ProxyUsed:   task.ProxyInfo.URL,
	}

	if err := bs.processResponse(result); err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"account_id": task.Account.ID,
			"page":       pageNum,
			"error":      err.Error(),
		}).Error("[BuffBuyScraper] 处理响应数据失败")
	} else {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"account_id": task.Account.ID,
			"account":    task.Account.Account,
			"page":       pageNum,
			"status":     resp.StatusCode,
		}).Debug("[BuffBuyScraper] 页面抓取成功")
	}
}

// isAccountTaskValid 检查账号任务是否仍然有效
func (bs *BuffBuyScraper) isAccountTaskValid(task *AccountTask) bool {
	// 检查代理键是否仍然存在
	value := gredis.Get(task.ProxyKey)
	if value == "" {
		return false
	}

	// 检查账号状态
	account, err := bs.dao.GetOneBuffUser()
	if err != nil || account.ID != task.Account.ID || account.Status != 1 {
		return false
	}

	return true
}

// cleanupAccountTask 清理账号任务资源
func (bs *BuffBuyScraper) cleanupAccountTask(task *AccountTask) {
	global.Logger.WithFields(map[string]interface{}{
		"scraper":    "buff_buy",
		"account_id": task.Account.ID,
		"account":    task.Account.Account,
	}).Info("[BuffBuyScraper] 清理账号任务资源")

	// 删除代理键
	if task.ProxyKey != "" {
		gredis.Del(task.ProxyKey)
	}

	// 释放代理
	if bs.proxyManager != nil && task.ProxyInfo != nil {
		bs.proxyManager.ReleaseProxy(task.ProxyInfo)
	}

	// 更新账号状态为空闲
	if err := bs.dao.UpdateBuffUserStatus(int(task.Account.ID), 0); err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"account_id": task.Account.ID,
			"error":      err.Error(),
		}).Warn("[BuffBuyScraper] 更新账号状态失败")
	}

	// 取消任务上下文
	if task.Cancel != nil {
		task.Cancel()
	}

	// 从任务映射中移除
	bs.tasksMux.Lock()
	delete(bs.accountTasks, task.Account.ID)
	bs.tasksMux.Unlock()

	task.IsRunning = false
}

// cleanupInactiveTasks 清理不活跃的任务
func (bs *BuffBuyScraper) cleanupInactiveTasks() {
	now := time.Now()
	inactiveTasks := make([]*AccountTask, 0)

	// 查找不活跃的任务
	for _, task := range bs.accountTasks {
		if !task.IsRunning || now.Sub(task.LastActive) > 5*time.Minute {
			inactiveTasks = append(inactiveTasks, task)
		}
	}

	// 清理不活跃的任务
	for _, task := range inactiveTasks {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":     "buff_buy",
			"account_id":  task.Account.ID,
			"account":     task.Account.Account,
			"last_active": task.LastActive.Format("2006-01-02 15:04:05"),
		}).Info("[BuffBuyScraper] 清理不活跃的账号任务")

		bs.cleanupAccountTask(task)
	}
}

// stopAllAccountTasks 停止所有账号任务
func (bs *BuffBuyScraper) stopAllAccountTasks() {
	bs.tasksMux.Lock()
	defer bs.tasksMux.Unlock()

	global.Logger.WithFields(map[string]interface{}{
		"scraper":    "buff_buy",
		"task_count": len(bs.accountTasks),
	}).Info("[BuffBuyScraper] 停止所有账号抓取任务")

	for _, task := range bs.accountTasks {
		if task.Cancel != nil {
			task.Cancel()
		}
	}

	// 清空任务映射
	bs.accountTasks = make(map[int64]*AccountTask)
}

// processResponse 处理响应数据
func (bs *BuffBuyScraper) processResponse(result *interfaces.ScrapingResult) error {
	startTime := time.Now()

	global.Logger.WithFields(map[string]interface{}{
		"scraper":     "buff_buy",
		"task_id":     result.TaskID,
		"status_code": result.StatusCode,
		"body_size":   len(result.Body),
	}).Info("[BuffBuyScraper] 开始处理响应数据")

	if result.StatusCode != http.StatusOK {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":     "buff_buy",
			"task_id":     result.TaskID,
			"status_code": result.StatusCode,
		}).Error("[BuffBuyScraper] HTTP状态码错误")
		return fmt.Errorf("HTTP error: %d", result.StatusCode)
	}

	var buffData BuffBuyData
	if err := json.Unmarshal(result.Body, &buffData); err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":      "buff_buy",
			"task_id":      result.TaskID,
			"error":        err.Error(),
			"body_preview": string(result.Body[:min(len(result.Body), 200)]),
		}).Error("[BuffBuyScraper] JSON解析失败")
		return fmt.Errorf("JSON解析失败: %v", err)
	}

	if buffData.Code != "OK" {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":  "buff_buy",
			"task_id":  result.TaskID,
			"api_code": buffData.Code,
			"body":     string(result.Body),
		}).Error("[BuffBuyScraper] API返回错误")
		return fmt.Errorf("API返回错误: %s", buffData.Code)
	}

	itemCount := len(buffData.Result.Items)
	global.Logger.WithFields(map[string]interface{}{
		"scraper":    "buff_buy",
		"task_id":    result.TaskID,
		"item_count": itemCount,
		"api_code":   buffData.Code,
	}).Info("[BuffBuyScraper] 响应数据解析成功")

	// 处理商品数据
	err := bs.processItems(buffData.Result.Items)

	duration := time.Since(startTime)
	if err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"task_id":    result.TaskID,
			"item_count": itemCount,
			"duration":   duration.String(),
			"error":      err.Error(),
		}).Error("[BuffBuyScraper] 处理商品数据失败")
	} else {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":    "buff_buy",
			"task_id":    result.TaskID,
			"item_count": itemCount,
			"duration":   duration.String(),
		}).Info("[BuffBuyScraper] 响应数据处理完成")
	}

	return err
}

// min 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// processItems 处理商品数据
func (bs *BuffBuyScraper) processItems(items []BuffBuyItem) error {
	startTime := time.Now()
	itemCount := len(items)

	global.Logger.WithFields(map[string]interface{}{
		"scraper":     "buff_buy",
		"item_count":  itemCount,
		"concurrency": 10,
	}).Info("[BuffBuyScraper] 开始处理商品数据")

	if itemCount == 0 {
		global.Logger.WithFields(map[string]interface{}{
			"scraper": "buff_buy",
		}).Warn("[BuffBuyScraper] 没有商品数据需要处理")
		return nil
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // 限制并发数
	var successCount, failCount int32

	for i, item := range items {
		wg.Add(1)
		go func(index int, item BuffBuyItem) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			global.Logger.WithFields(map[string]interface{}{
				"scraper":   "buff_buy",
				"item_id":   item.Id,
				"item_name": item.Name,
				"index":     index + 1,
				"total":     itemCount,
				"price":     item.BuyMaxPrice,
			}).Debug("[BuffBuyScraper] 开始处理单个商品")

			if err := bs.processItem(item); err != nil {
				atomic.AddInt32(&failCount, 1)
				global.Logger.WithFields(map[string]interface{}{
					"scraper":   "buff_buy",
					"item_id":   item.Id,
					"item_name": item.Name,
					"error":     err.Error(),
				}).Error("[BuffBuyScraper] 处理商品失败")
			} else {
				atomic.AddInt32(&successCount, 1)
				global.Logger.WithFields(map[string]interface{}{
					"scraper":   "buff_buy",
					"item_id":   item.Id,
					"item_name": item.Name,
				}).Debug("[BuffBuyScraper] 商品处理成功")
			}
		}(i, item)
	}

	wg.Wait()

	duration := time.Since(startTime)
	global.Logger.WithFields(map[string]interface{}{
		"scraper":       "buff_buy",
		"total_items":   itemCount,
		"success_count": int(successCount),
		"fail_count":    int(failCount),
		"duration":      duration.String(),
		"items_per_sec": float64(itemCount) / duration.Seconds(),
	}).Info("[BuffBuyScraper] 商品数据处理完成")

	return nil
}

// processItem 处理单个商品
func (bs *BuffBuyScraper) processItem(item BuffBuyItem) error {
	startTime := time.Now()

	// 转换价格
	price, err := strconv.ParseFloat(item.BuyMaxPrice, 64)
	if err != nil {
		global.Logger.WithFields(map[string]interface{}{
			"scraper":   "buff_buy",
			"item_id":   item.Id,
			"item_name": item.Name,
			"price_str": item.BuyMaxPrice,
			"error":     err.Error(),
		}).Error("[BuffBuyScraper] 价格转换失败")
		return fmt.Errorf("价格转换失败: %v", err)
	}

	// 创建商品数据（简化实现）
	itemData := map[string]interface{}{
		"item_id":          item.Id,
		"name":             item.Name,
		"market_hash_name": item.MarketHashName,
		"buy_max_price":    price,
		"buy_num":          item.BuyNum,
		"appid":            item.Appid,
		"created_on":       time.Now().Unix(),
		"modified_on":      time.Now().Unix(),
	}

	global.Logger.WithFields(map[string]interface{}{
		"scraper":   "buff_buy",
		"item_id":   item.Id,
		"item_name": item.Name,
		"price":     price,
		"buy_num":   item.BuyNum,
		"appid":     item.Appid,
	}).Debug("[BuffBuyScraper] 商品数据创建成功")

	// 这里可以实现具体的数据库保存逻辑
	_ = itemData // 暂时忽略，避免编译错误

	// 缓存到Redis
	cacheKey := fmt.Sprintf("buff_buy:%d", item.Id)
	if data, err := json.Marshal(itemData); err == nil {
		if err := gredis.Set(cacheKey, string(data), 10*time.Minute); err != nil {
			global.Logger.WithFields(map[string]interface{}{
				"scraper":   "buff_buy",
				"item_id":   item.Id,
				"cache_key": cacheKey,
				"error":     err.Error(),
			}).Warn("[BuffBuyScraper] Redis缓存保存失败")
		} else {
			global.Logger.WithFields(map[string]interface{}{
				"scraper":   "buff_buy",
				"item_id":   item.Id,
				"cache_key": cacheKey,
				"ttl":       "10m",
			}).Debug("[BuffBuyScraper] 商品数据已缓存到Redis")
		}
	} else {
		global.Logger.WithFields(map[string]interface{}{
			"scraper": "buff_buy",
			"item_id": item.Id,
			"error":   err.Error(),
		}).Error("[BuffBuyScraper] 商品数据JSON序列化失败")
	}

	duration := time.Since(startTime)
	global.Logger.WithFields(map[string]interface{}{
		"scraper":   "buff_buy",
		"item_id":   item.Id,
		"item_name": item.Name,
		"duration":  duration.String(),
	}).Debug("[BuffBuyScraper] 单个商品处理完成")

	return nil
}
