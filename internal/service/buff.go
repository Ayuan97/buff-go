package service

//
//import (
//	"buff-go/global"
//	"buff-go/internal/model"
//	"buff-go/pkg/gredis"
//	"buff-go/pkg/rediskey"
//	"buff-go/pkg/util"
//	"encoding/json"
//	"fmt"
//	"io/ioutil"
//	"net/http"
//	"net/url"
//	"runtime"
//	"time"
//)
//
//type BuffData struct {
//	Code   string `json:"code"`
//	Result Result `json:"data"`
//	Msg    string `json:"msg"`
//	Error  string `json:"error"`
//	Extra  string `json:"extra"`
//}
//
//type Result struct {
//	Items      []BuffGoods `json:"items"`
//	PageNum    int         `json:"page_num"`
//	PageSize   int         `json:"page_size"`
//	TotalCount int         `json:"total_count"`
//	TotalPage  int         `json:"total_page"`
//}
//type BuffGoodsInfo struct {
//	IconUrl         string      `json:"icon_url"`
//	ItemId          interface{} `json:"item_id"`
//	OriginalIconUrl string      `json:"original_icon_url"`
//	SteamPrice      string      `json:"steam_price"`
//	SteamPriceCny   string      `json:"steam_price_cny"`
//}
//type BuffGoods struct {
//	Appid                 int         `json:"appid"`
//	Bookmarked            bool        `json:"bookmarked"`
//	BuyMaxPrice           string      `json:"buy_max_price"`
//	BuyNum                int         `json:"buy_num"`
//	CanBargain            bool        `json:"can_bargain"`
//	CanSearchByTournament bool        `json:"can_search_by_tournament"`
//	Description           interface{} `json:"description"`
//	Game                  string      `json:"game"`
//	GoodsInfo             GoodsInfo   `json:"goods_info"`
//	HasBuffPriceHistory   bool        `json:"has_buff_price_history"`
//	Id                    int         `json:"id"`
//	MarketHashName        string      `json:"market_hash_name"`
//	MarketMinPrice        string      `json:"market_min_price"`
//	Name                  string      `json:"name"`
//	QuickPrice            string      `json:"quick_price"`
//	SellMinPrice          string      `json:"sell_min_price"`
//	SellNum               int         `json:"sell_num"`
//	SellReferencePrice    string      `json:"sell_reference_price"`
//	ShortName             string      `json:"short_name"`
//	SteamMarketUrl        string      `json:"steam_market_url"`
//	TransactedNum         int         `json:"transacted_num"`
//}
//type GoodsInfo struct {
//	IconUrl         string      `json:"icon_url"`
//	ItemId          interface{} `json:"item_id"`
//	OriginalIconUrl string      `json:"original_icon_url"`
//	SteamPrice      string      `json:"steam_price"`
//	SteamPriceCny   string      `json:"steam_price_cny"`
//}
//
//// Buff 获取buff数据
//func Buff() {
//	get()
//}
//func get() {
//	//每5秒扫描一次 查询是否有可用代理 和 可用账号 如果有则启动一个协程
//	go func() {
//		for {
//			//查询buff抓取是否开启
//			config := myDao.GetOneSystemConfig(1)
//			if config.StartBuff == 0 {
//				fmt.Println("buff抓取未开启")
//				time.Sleep(10 * time.Second)
//				continue
//			}
//			//查询本地代理和账号是否可用 可用则启动一个本地协程
//			//查询本地代理是否可用
//			buffLocalKey := rediskey.GetBuffLocalKey()
//			buffLocalResult := gredis.Get(buffLocalKey)
//			//查询账号是否可用
//			buffUser, err := myDao.GetOneBuffUser()
//			if err != nil {
//				time.Sleep(10 * time.Second)
//				continue
//			}
//			buffAccountKey := rediskey.GetBuffAccountKey(int(buffUser.ID))
//			buffAccountResult := gredis.Get(buffAccountKey)
//			if buffLocalResult == "" && buffAccountResult == "" {
//				//开启本地代理
//				go GetBuffData(0, "", buffUser)
//				time.Sleep(2 * time.Second)
//			} else {
//				fmt.Println("buff本地代理正在抓取中...")
//			}
//
//			////查询是否有可用代理
//			//proxy := myDao.GetOneProxy(1)
//			////查询是否有可用账号
//			//account := myDao.GetOneAccount(1)
//			//if proxy != nil && account != nil {
//			//	//启动一个协程 使用代理
//			//	go GetBuffData(1, proxy.Ip)
//			//}
//			time.Sleep(10 * time.Second)
//		}
//	}()
//}
//
//func GetBuffData(isProxy int, proxy string, account model.BuffUser) {
//	//设置本地代理正在抓取中
//	buffLocalKey := rediskey.GetBuffLocalKey()
//	gredis.Set(buffLocalKey, "1", 0)
//	//设置账号正在抓取中
//	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
//	gredis.Set(buffAccountKey, "1", 0)
//	//修改状态
//	myDao.UpdateBuffUserStatus(int(account.ID), 1)
//	for {
//		fmt.Println("buff start:", time.Now().Format("2006-01-02 15:04:05"))
//		//循环N次 获取buff数据
//		config := myDao.GetOneSystemConfig(1) //系统配置
//		for i := 1; i <= config.BuffPageNum; i++ {
//			geturl := fmt.Sprintf("https://buff.163.com/api/market/goods/buying?game=csgo&page_num=%v&min_price=%v&max_price=%v&sort_by=price.desc&page_size=80&use_suggestion=0&_=%v", i, config.BuffMinPrice, config.BuffMaxPrice, time.Now().UnixNano()/1e6)
//			client := &http.Client{}
//			//isProxy 设置代理
//			if isProxy == 1 {
//				proxyURL, _ := url.Parse(proxy)
//				client.Transport = &http.Transport{
//					Proxy: http.ProxyURL(proxyURL),
//				}
//			}
//			req, err := http.NewRequest("GET", geturl, nil)
//			if err != nil {
//				fmt.Println("buff err: 发起请求失败", err)
//				//结束协程
//				fmt.Println("结束协程")
//				//设置本地代理抓取结束
//				gredis.Del(buffLocalKey)
//				//设置账号抓取结束
//				gredis.Del(buffAccountKey)
//				myDao.UpdateBuffUserStatus(int(account.ID), 0)
//				runtime.Goexit()
//				return
//			}
//			req.AddCookie(&http.Cookie{Name: "Device-Id", Value: account.DeviceId})
//			req.AddCookie(&http.Cookie{Name: "Locale-Supported", Value: "zh-Hans"})
//			req.AddCookie(&http.Cookie{Name: "csrf_token", Value: account.CsrfToken})
//			req.AddCookie(&http.Cookie{Name: "game", Value: "csgo"})
//			req.AddCookie(&http.Cookie{Name: "remember_me", Value: account.RememberMe})
//			req.AddCookie(&http.Cookie{Name: "session", Value: account.Sessionid})
//			resp, err := client.Do(req)
//			if err != nil {
//				fmt.Println("buff err2:", err)
//				endTask2(resp, account, 0, 1)
//				return
//			}
//			body, err := ioutil.ReadAll(resp.Body)
//			if err != nil {
//				fmt.Println("buff err3:", err)
//				//结束协程
//				endTask(resp, account, 0, 1)
//				return
//			}
//			var buffData BuffData
//			err = json.Unmarshal(body, &buffData)
//			if err != nil {
//				fmt.Println("buff err4:", err)
//				endTask(resp, account, 0, 1)
//				return
//			}
//			if buffData.Code == "Action Forbidden" {
//				fmt.Println("buff err6:", buffData.Code)
//				endTask(resp, account, 3, 1)
//			}
//			if buffData.Code == "Login Required" {
//				fmt.Println("buff err7:", buffData.Code)
//				endTask(resp, account, 2, 1)
//			}
//			if buffData.Code == "OK" {
//				go handleBuffData(buffData)
//			} else {
//				fmt.Println("buff err6:", buffData)
//				//结束协程
//				fmt.Println("结束协程")
//				endTask(resp, account, 0, 1)
//			}
//			config = myDao.GetOneSystemConfig(1) //系统配置
//			if config.StartBuff == 0 {
//				endTask(resp, account, 0, 1)
//				return
//			}
//			//每次请求间隔
//			delay := time.Duration(config.BuffDelay)
//			time.Sleep(time.Second * delay)
//		}
//	}
//
//}
//
//// 处理buff数据
//func handleBuffData(buffData BuffData) {
//	//批量更新buff数据 不存在的话就插入
//
//	//批量插入数据库
//	for _, v := range buffData.Result.Items {
//		var goodsInfo model.Goods
//		goodsInfo.Appid = v.Appid
//		goodsInfo.BuyMaxPrice = util.StringToFloat64(v.BuyMaxPrice)
//		goodsInfo.BuyNum = v.BuyNum
//		goodsInfo.Game = v.Game
//		goodsInfo.Name = v.Name
//		goodsInfo.MarketHashName = v.MarketHashName
//		goodsInfo.ShortName = v.ShortName
//		goodsInfo.GoodsId = v.Id
//		goodsInfo.IconUrl = v.GoodsInfo.IconUrl
//		goodsInfo.SteamPrice = util.StringToFloat64(v.GoodsInfo.SteamPrice)
//		goodsInfo.SteamPriceCny = util.StringToFloat64(v.GoodsInfo.SteamPriceCny)
//		goodsInfo.QuickPrice = util.StringToFloat64(v.QuickPrice)
//		goodsInfo.SellMinPrice = util.StringToFloat64(v.SellMinPrice)
//		goodsInfo.SellNum = v.SellNum
//		goodsInfo.SellReferencePrice = util.StringToFloat64(v.SellReferencePrice)
//		goodsInfo.SteamMarketUrl = v.SteamMarketUrl
//		goodsInfo.BuffUpdate = time.Now().Unix()
//		myDao.CreateGoods(&goodsInfo)
//		isNeedUpdateBuffData(&goodsInfo)
//		fmt.Println("buff 商品名称:", v.MarketHashName, "价格:", util.StringToFloat64(v.BuyMaxPrice))
//		global.Logger.Info("buff 商品名称:", v.MarketHashName, "价格:", util.StringToFloat64(v.BuyMaxPrice))
//	}
//}
//
//// 是否需要更新buff数据 通知 更新比例
//func isNeedUpdateBuffData(info *model.Goods) {
//	//查询缓存中的buff数据 与数据库中的buff数据对比 有变化的话就发送telegram消息 更新比例和更新缓存
//	//查询缓存中的buff数据
//	config := myDao.GetOneSystemConfig(1)
//	goodsCacheKey := rediskey.GetCacheKey(info.MarketHashName)
//	buyMaxPrice := gredis.Hget(goodsCacheKey, "buy_max_price")
//	SteamSellPrice := gredis.Hget(goodsCacheKey, "steam_sell_price")
//	info.SteamSellPrice = util.StringToFloat64(SteamSellPrice)
//	if util.StringToFloat64(SteamSellPrice) == 0 {
//		return
//	}
//	if SteamSellPrice == "" {
//		gredis.Hset(goodsCacheKey, "steam_sell_price", 0)
//		return
//	}
//	if buyMaxPrice == "" {
//		//缓存中没有数据
//		//更新缓存
//		gredis.Hset(goodsCacheKey, "buy_max_price", info.BuyMaxPrice)
//	} else {
//		//缓存中有数据
//		//比较价格
//		if util.StringToFloat64(buyMaxPrice) != info.BuyMaxPrice {
//			//价格变化
//			//更新缓存
//			gredis.Hset(goodsCacheKey, "buy_max_price", info.BuyMaxPrice)
//			//更新比例
//			P := info.BuyMaxPrice / util.StringToFloat64(SteamSellPrice)
//			err := myDao.UpdateGoodsRatioByGoodsId(info.GoodsId, P)
//			if err != nil {
//				return
//			}
//			//发送telegram消息
//			info.Proportion = P
//			if P >= config.BotProportion && util.StringToFloat64(SteamSellPrice) >= 0 {
//				sendTelegram(info, 1)
//			}
//		}
//	}
//
//}
//
//// 结束任务
//func endTask(resp *http.Response, account model.BuffUser, status int, taskType int) {
//	buffLocalKey := rediskey.GetBuffLocalKey()
//	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
//	resp.Body.Close()
//	//设置本地代理抓取结束
//	gredis.Del(buffLocalKey)
//	//设置账号抓取结束
//	gredis.Del(buffAccountKey)
//	//设置账号状态
//	myDao.UpdateBuffUserStatus(int(account.ID), status)
//	if taskType == 1 {
//		runtime.Goexit()
//	}
//}
//
//// 结束任务
//func endTask2(resp *http.Response, account model.BuffUser, status int, taskType int) {
//	buffLocalKey := rediskey.GetBuffLocalKey()
//	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
//	//设置本地代理抓取结束
//	gredis.Del(buffLocalKey)
//	//设置账号抓取结束
//	gredis.Del(buffAccountKey)
//	//设置账号状态
//	myDao.UpdateBuffUserStatus(int(account.ID), status)
//	if taskType == 1 {
//		runtime.Goexit()
//	}
//}
