package buff

import (
	"buff-go/global"
	"buff-go/internal/dao"
	"buff-go/internal/model"
	"buff-go/internal/scraper/core/base"
	"buff-go/internal/scraper/interfaces"
	"buff-go/internal/service"
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
	scraper := &BuffBuyScraper{
		BaseScraper:  baseScraper,
		dao:          dao,
		accountTasks: make(map[int64]*AccountTask),
		config:       &BuffBuyConfig{}, // 初始化空配置，稍后从系统配置加载
	}

	// 从系统配置加载初始配置
	scraper.loadInitialConfig()

	// 注册配置变更监听器
	scraper.setupConfigListener()

	return scraper
}

// loadInitialConfig 加载初始配置
func (bs *BuffBuyScraper) loadInitialConfig() {
	// 获取当前活跃的游戏配置
	config, game, err := service.GetCurrentGameConfig()
	if err != nil {
		fmt.Printf("获取系统配置失败，使用默认CSGO配置 平台：%s 错误：%v\n", bs.GetPlatform(), err)
		// 使用默认CSGO配置作为fallback
		bs.config.Game = "csgo"
		bs.config.AppID = 730
		bs.config.MinPrice = 0.01
		bs.config.MaxPrice = 1000.0
		bs.config.PageNum = 10
		return
	}

	// 使用系统配置更新本地配置
	bs.updateLocalConfig(config, game)
	fmt.Printf("初始配置加载完成 平台：%s 游戏：%s 页面数：%d 价格范围：%.2f-%.2f\n",
		bs.GetPlatform(), game, bs.config.PageNum, bs.config.MinPrice, bs.config.MaxPrice)
}

// setupConfigListener 设置配置变更监听器
func (bs *BuffBuyScraper) setupConfigListener() {
	fmt.Println("配置变更监听器已设置 平台：", bs.GetPlatform())

	// 注册CSGO配置变更监听器
	configManager := service.GetConfigManager()
	configManager.AddConfigChangeListener(1, func(configID int64) {
		fmt.Printf("收到CSGO配置变更通知 平台：%s 配置ID：%d\n", bs.GetPlatform(), configID)
		bs.onConfigChanged(configID)
	})

	// 注册DOTA2配置变更监听器
	configManager.AddConfigChangeListener(2, func(configID int64) {
		fmt.Printf("收到DOTA2配置变更通知 平台：%s 配置ID：%d\n", bs.GetPlatform(), configID)
		bs.onConfigChanged(configID)
	})
}

// onConfigChanged 处理配置变更事件
func (bs *BuffBuyScraper) onConfigChanged(configID int64) {
	fmt.Printf("开始处理配置变更 平台：%s 配置ID：%d\n", bs.GetPlatform(), configID)

	// 获取变更的具体配置
	configManager := service.GetConfigManager()
	changedConfig := configManager.GetConfig(configID)

	// 验证配置有效性
	if err := service.ValidateConfig(changedConfig); err != nil {
		fmt.Printf("配置验证失败 平台：%s 错误：%v\n", bs.GetPlatform(), err)
		return
	}

	// 获取当前活跃配置
	activeConfig, activeGame, err := service.GetCurrentGameConfig()
	if err != nil {
		fmt.Printf("获取当前活跃配置失败 平台：%s 错误：%v\n", bs.GetPlatform(), err)
		return
	}

	// 更新本地配置为当前活跃配置
	bs.updateLocalConfig(activeConfig, activeGame)

	// 如果当前活跃配置的Buff买入功能被禁用，停止所有任务
	if activeConfig.BuffBuyStatus == 0 {
		fmt.Printf("当前活跃配置的Buff买入功能已禁用，停止所有抓取任务 平台：%s 游戏：%s\n", bs.GetPlatform(), activeGame)
		bs.stopAllAccountTasks()
	} else {
		fmt.Printf("配置已更新，抓取任务将使用新配置 平台：%s 游戏：%s 配置ID：%d\n", bs.GetPlatform(), activeGame, activeConfig.ID)
	}
}

// updateLocalConfig 更新本地配置
func (bs *BuffBuyScraper) updateLocalConfig(config model.Config, game string) {
	bs.config.Game = game
	bs.config.MinPrice = config.MinPrice
	bs.config.MaxPrice = config.MaxPrice
	bs.config.PageNum = config.BuffPageNum

	// 根据游戏类型设置AppID
	if game == "csgo" {
		bs.config.AppID = 730
	} else if game == "dota2" {
		bs.config.AppID = 570
	}

	fmt.Printf("本地配置已更新 平台：%s 游戏：%s 页面数：%d 价格范围：%.2f-%.2f\n",
		bs.GetPlatform(), game, bs.config.PageNum, bs.config.MinPrice, bs.config.MaxPrice)
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
	// 使用StartWithoutWorkers避免启动无用的worker协程
	if err := bs.BaseScraper.StartWithoutWorkers(ctx); err != nil {
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

	fmt.Println("开始多账号抓取循环 平台：", bs.GetPlatform(), " 游戏：", bs.config.Game, " 最小价格：", bs.config.MinPrice, " 最大价格：", bs.config.MaxPrice, " 页面数量：", bs.config.PageNum)

	for {
		select {
		case <-bs.BaseScraper.Ctx.Done():
			fmt.Println("收到停止信号，停止所有账号抓取任务")
			bs.stopAllAccountTasks()
			return
		case <-ticker.C:
			bs.manageAccountTasks()
		}
	}
}

// manageAccountTasks 管理账号抓取任务
func (bs *BuffBuyScraper) manageAccountTasks() {
	// fmt.Printf("开始管理账号任务检查 平台：%s 时间：%s\n", bs.GetPlatform(), time.Now().Format("2006-01-02 15:04:05.000"))

	// 获取所有可用账号
	accounts, err := bs.dao.GetBuffUserList()
	if err != nil {
		fmt.Printf("获取账号列表失败 平台：%s 错误：%v\n", bs.GetPlatform(), err)
		return
	}

	// fmt.Printf("开始管理账号任务 平台：%s 账号数量：%d 时间：%s\n", bs.GetPlatform(), len(accounts), time.Now().Format("2006-01-02 15:04:05.000"))

	bs.tasksMux.Lock()
	defer bs.tasksMux.Unlock()

	// 显示当前活跃任务数量
	fmt.Printf("当前活跃任务数量：%d\n", len(bs.accountTasks))

	// 为每个状态为0（空闲）的账号创建抓取任务
	for _, account := range accounts {
		// fmt.Printf("检查账号 %d/%d ID：%d 账号：%s 状态：%d\n", i+1, len(accounts), account.ID, account.Account, account.Status)

		if account.Status == 0 { // 账号空闲
			if _, exists := bs.accountTasks[account.ID]; !exists {
				fmt.Printf("为空闲账号创建任务 平台：%s s账号ID：%d 账号：%s\n", bs.GetPlatform(), account.ID, account.Account)
				// 创建新的账号抓取任务
				if err := bs.createAccountTask(account); err != nil {
					fmt.Printf("创建账号抓取任务失败 平台：%s 账号ID：%d 账号：%s 错误：%v\n", bs.GetPlatform(), account.ID, account.Account, err)
				} else {
					// fmt.Printf("成功创建账号抓取任务 平台：%s 账号ID：%d 账号：%s\n", bs.GetPlatform(), account.ID, account.Account)
				}
			} else {
				fmt.Printf("账号已有活跃任务 平台：%s 账号ID：%d 账号：%s\n", bs.GetPlatform(), account.ID, account.Account)
			}
		} else {
			// fmt.Printf("账号状态非空闲，跳过 平台：%s 账号ID：%d 账号：%s 状态：%d\n", bs.GetPlatform(), account.ID, account.Account, account.Status)
		}
	}

	// 清理已完成或失效的任务
	bs.cleanupInactiveTasks()
}

// createAccountTask 创建账号抓取任务
func (bs *BuffBuyScraper) createAccountTask(account *model.BuffUser) error {
	// fmt.Printf("开始为账号创建任务 平台：%s 账号ID：%d 账号：%s\n", bs.GetPlatform(), account.ID, account.Account)

	// 获取代理（添加超时机制）
	proxyCtx, proxyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer proxyCancel()

	var proxy *interfaces.ProxyInfo
	var err error

	// 在goroutine中获取代理，支持超时
	done := make(chan bool, 1)
	go func() {
		proxy, err = bs.proxyManager.GetProxyForPlatform(interfaces.PlatformBuff)
		done <- true
	}()

	select {
	case <-done:
		if err != nil {
			fmt.Printf("获取代理失败 平台：%s 账号ID：%d 错误：%v\n", bs.GetPlatform(), account.ID, err)
			return fmt.Errorf("获取代理失败: %v", err)
		}
	case <-proxyCtx.Done():
		fmt.Printf("获取代理超时 平台：%s 账号ID：%d\n", bs.GetPlatform(), account.ID)
		return fmt.Errorf("获取代理超时")
	}

	fmt.Printf("成功获取代理 平台：%s 账号ID：%d 代理：%s\n", bs.GetPlatform(), account.ID, proxy.URL)

	// 创建HTTP客户端配置
	clientConfig := &interfaces.ClientConfig{
		Timeout:         30 * time.Second,
		MaxIdleConns:    10,
		MaxConnsPerHost: 5,
		ProxyURL:        proxy.URL,
		FollowRedirect:  true,
	}

	// 获取HTTP客户端（添加超时机制）
	// fmt.Printf("开始创建HTTP客户端 平台：%s 账号ID：%d\n", bs.GetPlatform(), account.ID)

	clientCtx, clientCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer clientCancel()

	var client *http.Client
	clientDone := make(chan bool, 1)
	go func() {
		client, err = bs.clientManager.GetClient(clientConfig)
		clientDone <- true
	}()

	select {
	case <-clientDone:
		if err != nil {
			bs.proxyManager.ReleaseProxy(proxy)
			fmt.Printf("创建HTTP客户端失败 平台：%s 账号ID：%d 错误：%v\n", bs.GetPlatform(), account.ID, err)
			return fmt.Errorf("创建HTTP客户端失败: %v", err)
		}
	case <-clientCtx.Done():
		bs.proxyManager.ReleaseProxy(proxy)
		fmt.Printf("创建HTTP客户端超时 平台：%s 账号ID：%d\n", bs.GetPlatform(), account.ID)
		return fmt.Errorf("创建HTTP客户端超时")
	}

	fmt.Printf("成功创建HTTP客户端 平台：%s 账号ID：%d\n", bs.GetPlatform(), account.ID)

	// 设置账号Cookie
	if err := bs.setAccountCookies(client, account); err != nil {
		bs.proxyManager.ReleaseProxy(proxy)
		fmt.Println("设置账号Cookie失败 平台：", bs.GetPlatform(), " 错误：", err)
		return fmt.Errorf("设置账号Cookie失败: %v", err)
	}

	// 创建任务上下文
	taskCtx, cancel := context.WithCancel(bs.BaseScraper.Ctx)

	// 生成代理键
	proxyKey := fmt.Sprintf("buff_proxy_%s_%d", proxy.ID, account.ID)
	fmt.Println("生成代理键 平台：", bs.GetPlatform(), " 代理ID：", proxy.ID, " 账号ID：", account.ID, " 代理键：", proxyKey)
	// 设置代理键，过期时间为10分钟，防止键堆积
	if err := gredis.Set(proxyKey, account.ID, 10*time.Minute); err != nil {
		fmt.Println("设置代理键失败 平台：", bs.GetPlatform(), " 代理键：", proxyKey, " 错误：", err)
	}

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

		fmt.Println("更新账号状态失败 平台：", bs.GetPlatform(), " 账号ID：", account.ID, " 错误：", err)
	}

	// 启动账号抓取协程
	go bs.runAccountTask(taskCtx, accountTask)

	fmt.Println("创建账号抓取任务成功 平台：", bs.GetPlatform(), " 账号ID：", account.ID, " 账号：", account.Account, " 代理：", proxy.URL, " 代理键：", proxyKey)

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

	fmt.Println("开始账号抓取任务 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 账号：", task.Account.Account, " 代理：", task.ProxyInfo.URL)

	for {
		select {
		case <-ctx.Done():
			fmt.Println("账号抓取任务收到停止信号 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 账号：", task.Account.Account)
			return
		case <-ticker.C:
			// 检查账号状态是否仍然有效
			if !bs.isAccountTaskValid(task) {
				fmt.Println("账号任务已失效，停止抓取 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 账号：", task.Account.Account)
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

	fmt.Println("开始账号抓取 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 账号：", task.Account.Account, " 开始时间：", startTime.Format("2006-01-02 15:04:05.000"))

	// 获取当前活跃的游戏配置
	config, game, err := service.GetCurrentGameConfig()
	if err != nil {
		fmt.Println("获取当前游戏配置失败 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 错误：", err)
		// 使用默认CSGO配置
		config = bs.dao.GetOneSystemConfig(1)
		game = "csgo"
	}

	fmt.Println("获取游戏配置 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 游戏类型：", game, " 配置ID：", config.ID)
	fmt.Println("config", config)
	fmt.Println("系统配置获取完成 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 游戏：", game, " 页面数：", config.BuffPageNum, " 价格范围：", config.MinPrice, "-", config.MaxPrice, " 抓取状态：", config.BuffBuyStatus)

	// 检查抓取功能是否启用
	if config.BuffBuyStatus == 0 {
		fmt.Println("Buff买入抓取功能未启用，跳过抓取 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 抓取状态：", config.BuffBuyStatus)
		return
	}

	// 检查配置有效性
	if config.BuffPageNum <= 0 {
		fmt.Println("页面数量配置无效，跳过抓取 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面数量：", config.BuffPageNum)
		return
	}

	fmt.Println("开始顺序抓取页面 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 游戏：", game, " 页面数量：", config.BuffPageNum, " 价格范围：", config.MinPrice, "-", config.MaxPrice, " 延迟间隔：", config.BuffBuyDelay, "秒")

	// 顺序抓取多个页面，使用配置的延迟时间
	for i := 1; i <= config.BuffPageNum; i++ {
		// 检查当前活跃配置是否变更
		currentConfig, currentGame, err := service.GetCurrentGameConfig()
		if err == nil && (currentConfig.ID != config.ID || currentGame != game) {
			fmt.Println("活跃配置已变更，停止当前抓取 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 账号：", task.Account.Account, " 当前游戏：", currentGame, " 当前配置ID：", currentConfig.ID, " 原配置ID：", config.ID)
			break
		}

		fmt.Println("开始抓取页面 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", i, " 总页数：", config.BuffPageNum)
		bs.scrapePageForAccount(task, game, i, config)

		// 如果不是最后一页，则等待配置的延迟时间
		if i < config.BuffPageNum {
			delay := time.Duration(config.BuffBuyDelay) * time.Second
			fmt.Println("页面抓取间隔等待 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", i, " 延迟时间：", delay)
			time.Sleep(delay)
		}
	}

	duration := time.Since(startTime)
	fmt.Println("账号抓取完成 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 账号：", task.Account.Account, " 持续时间：", duration.String(), " 结束时间：", time.Now().Format("2006-01-02 15:04:05.000"))
}

// scrapePageForAccount 为指定账号抓取单个页面
func (bs *BuffBuyScraper) scrapePageForAccount(task *AccountTask, game string, pageNum int, config model.Config) {
	startTime := time.Now()
	url := fmt.Sprintf("https://buff.163.com/api/market/goods/buying?game=%s&page_num=%d&min_price=%v&max_price=%v&sort_by=price.desc&page_size=80&use_suggestion=0&_=%d",
		game, pageNum, config.MinPrice, config.MaxPrice, time.Now().UnixNano()/1e6)

	fmt.Println("准备抓取页面 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", pageNum, " URL：", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Println("创建HTTP请求失败 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", pageNum, " 错误：", err)
		return
	}

	// 设置请求头
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://buff.163.com/")

	fmt.Println("HTTP请求已创建 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", pageNum, " 代理：", task.ProxyInfo.URL)

	// 执行请求
	fmt.Println("开始执行HTTP请求 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", pageNum)
	resp, err := task.Client.Do(req)
	if err != nil {
		fmt.Println("HTTP请求失败 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", pageNum, " 错误：", err)
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

	fmt.Println("HTTP请求成功 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", pageNum, " 状态码：", resp.StatusCode, " 耗时：", time.Since(startTime))

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("读取响应失败 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", pageNum, " 错误：", err)
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
		fmt.Println("处理响应数据失败 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", pageNum, " 错误：", err)
	} else {
		fmt.Println("页面抓取成功 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 页面：", pageNum, " 状态：", resp.StatusCode)
	}
}

// isAccountTaskValid 检查账号任务是否仍然有效
func (bs *BuffBuyScraper) isAccountTaskValid(task *AccountTask) bool {
	// 检查代理键是否仍然存在
	value := gredis.Get(task.ProxyKey)
	if value == "" {
		fmt.Println("代理键不存在，任务失效 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 代理键：", task.ProxyKey)
		return false
	}

	// 检查当前账号的状态（应该通过ID查询特定账号，而不是查询状态为0的账号）
	accounts, err := bs.dao.GetBuffUserList()
	if err != nil {
		fmt.Println("获取账号列表失败，任务失效 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 错误：", err)
		return false
	}

	// 查找当前任务对应的账号
	for _, account := range accounts {
		if account.ID == task.Account.ID {
			if account.Status != 1 {
				fmt.Println("账号状态不是使用中，任务失效 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 当前状态：", account.Status, " 期望状态：1")
				return false
			}
			// 账号状态正常
			return true
		}
	}

	fmt.Println("未找到对应账号，任务失效 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID)
	return false
}

// cleanupAccountTask 清理账号任务资源
func (bs *BuffBuyScraper) cleanupAccountTask(task *AccountTask) {
	fmt.Println("清理账号任务资源 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 账号：", task.Account.Account)

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
		fmt.Println("更新账号状态失败 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 错误：", err)
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
		fmt.Println("清理不活跃的账号任务 平台：", bs.GetPlatform(), " 账号ID：", task.Account.ID, " 账号：", task.Account.Account, " 最后活跃时间：", task.LastActive.Format("2006-01-02 15:04:05"))

		bs.cleanupAccountTask(task)
	}
}

// stopAllAccountTasks 停止所有账号任务
func (bs *BuffBuyScraper) stopAllAccountTasks() {
	bs.tasksMux.Lock()
	defer bs.tasksMux.Unlock()

	fmt.Println("停止所有账号抓取任务 平台：", bs.GetPlatform(), " 任务数量：", len(bs.accountTasks))

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

	fmt.Println("开始处理响应数据 平台：", bs.GetPlatform(), " 任务ID：", result.TaskID, " 状态码：", result.StatusCode, " 响应体大小：", len(result.Body))

	if result.StatusCode != http.StatusOK {
		fmt.Println("HTTP状态码错误 平台：", bs.GetPlatform(), " 任务ID：", result.TaskID, " 状态码：", result.StatusCode)
		return fmt.Errorf("HTTP error: %d", result.StatusCode)
	}

	var buffData BuffBuyData
	if err := json.Unmarshal(result.Body, &buffData); err != nil {
		fmt.Println("JSON解析失败 平台：", bs.GetPlatform(), " 任务ID：", result.TaskID, " 错误：", err)
		return fmt.Errorf("JSON解析失败: %v", err)
	}

	if buffData.Code != "OK" {
		fmt.Println("API返回错误 平台：", bs.GetPlatform(), " 任务ID：", result.TaskID, " API码：", buffData.Code)
		return fmt.Errorf("API返回错误: %s", buffData.Code)
	}

	itemCount := len(buffData.Result.Items)
	fmt.Println("响应数据解析成功 平台：", bs.GetPlatform(), " 任务ID：", result.TaskID, " 商品数量：", itemCount, " API码：", buffData.Code)

	// 处理商品数据
	err := bs.processItems(buffData.Result.Items)

	duration := time.Since(startTime)
	if err != nil {
		fmt.Println("处理商品数据失败 平台：", bs.GetPlatform(), " 任务ID：", result.TaskID, " 商品数量：", itemCount, " 持续时间：", duration.String(), " 错误：", err)
	} else {
		fmt.Println("响应数据处理完成 平台：", bs.GetPlatform(), " 任务ID：", result.TaskID, " 商品数量：", itemCount, " 持续时间：", duration.String())
	}

	return err
}

// processItems 处理商品数据
func (bs *BuffBuyScraper) processItems(items []BuffBuyItem) error {
	// startTime := time.Now()
	itemCount := len(items)

	fmt.Println("开始处理商品数据 平台：", bs.GetPlatform(), " 商品数量：", itemCount, " 并发数：", 10)

	if itemCount == 0 {
		fmt.Println("没有商品数据需要处理 平台：", bs.GetPlatform())
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

			fmt.Println("开始处理单个商品 平台：", bs.GetPlatform(), " 商品ID：", item.Id, " 商品名称：", item.Name, " 索引：", index+1, " 总数：", itemCount, " 价格：", item.BuyMaxPrice)

			if err := bs.processItem(item); err != nil {
				atomic.AddInt32(&failCount, 1)
				fmt.Println("处理商品失败 平台：", bs.GetPlatform(), " 商品ID：", item.Id, " 商品名称：", item.Name, " 错误：", err)
			} else {
				atomic.AddInt32(&successCount, 1)
				// fmt.Println("商品处理成功 平台：", bs.GetPlatform(), " 商品ID：", item.Id, " 商品名称：", item.Name)
			}
		}(i, item)
	}

	wg.Wait()

	// duration := time.Since(startTime)
	// fmt.Println("商品数据处理完成 平台：", bs.GetPlatform(), " 商品数量：", itemCount, " 成功数：", successCount, " 失败数：", failCount, " 持续时间：", duration.String(), " 每秒处理数：", float64(itemCount)/duration.Seconds())

	return nil
}

// processItem 处理单个商品
func (bs *BuffBuyScraper) processItem(item BuffBuyItem) error {
	// startTime := time.Now()

	// 转换价格
	price, err := strconv.ParseFloat(item.BuyMaxPrice, 64)
	if err != nil {
		fmt.Println("价格转换失败 平台：", bs.GetPlatform(), " 商品ID：", item.Id, " 商品名称：", item.Name, " 价格：", item.BuyMaxPrice, " 错误：", err)
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

	// fmt.Println("商品数据创建成功 平台：", bs.GetPlatform(), " 商品ID：", item.Id, " 商品名称：", item.Name, " 价格：", price, " 购买数量：", item.BuyNum, " AppID：", item.Appid)

	// 这里可以实现具体的数据库保存逻辑
	_ = itemData // 暂时忽略，避免编译错误

	// 缓存到Redis
	cacheKey := fmt.Sprintf("buff_buy:%d", item.Id)
	if data, err := json.Marshal(itemData); err == nil {
		if err := gredis.Set(cacheKey, string(data), 10*time.Minute); err != nil {
			fmt.Println("Redis缓存保存失败 平台：", bs.GetPlatform(), " 商品ID：", item.Id, " 缓存键：", cacheKey, " 错误：", err)
		} else {
			// fmt.Println("商品数据已缓存到Redis 平台：", bs.GetPlatform(), " 商品ID：", item.Id, " 缓存键：", cacheKey, " TTL：", 10*time.Minute)
		}
	} else {
		fmt.Println("商品数据JSON序列化失败 平台：", bs.GetPlatform(), " 商品ID：", item.Id, " 错误：", err)
	}

	// duration := time.Since(startTime)
	// fmt.Println("单个商品处理完成 平台：", bs.GetPlatform(), " 商品ID：", item.Id, " 商品名称：", item.Name, " 持续时间：", duration.String())

	return nil
}
