package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
	"time"
)

type BuffData struct {
	Code   string `json:"code"`
	Result Result `json:"data"`
	Msg    string `json:"msg"`
	Error  string `json:"error"`
	Extra  string `json:"extra"`
}

type Result struct {
	Items      []BuffGoods `json:"items"`
	PageNum    int         `json:"page_num"`
	PageSize   int         `json:"page_size"`
	TotalCount int         `json:"total_count"`
	TotalPage  int         `json:"total_page"`
}
type BuffGoodsInfo struct {
	IconUrl         string      `json:"icon_url"`
	ItemId          interface{} `json:"item_id"`
	OriginalIconUrl string      `json:"original_icon_url"`
	SteamPrice      string      `json:"steam_price"`
	SteamPriceCny   string      `json:"steam_price_cny"`
}
type BuffGoods struct {
	Appid                 int         `json:"appid"`
	Bookmarked            bool        `json:"bookmarked"`
	BuyMaxPrice           string      `json:"buy_max_price"`
	BuyNum                int         `json:"buy_num"`
	CanBargain            bool        `json:"can_bargain"`
	CanSearchByTournament bool        `json:"can_search_by_tournament"`
	Description           interface{} `json:"description"`
	Game                  string      `json:"game"`
	GoodsInfo             GoodInfo    `json:"goods_info"`
	HasBuffPriceHistory   bool        `json:"has_buff_price_history"`
	Id                    int         `json:"id"`
	MarketHashName        string      `json:"market_hash_name"`
	MarketMinPrice        string      `json:"market_min_price"`
	Name                  string      `json:"name"`
	QuickPrice            string      `json:"quick_price"`
	SellMinPrice          string      `json:"sell_min_price"`
	SellNum               int         `json:"sell_num"`
	SellReferencePrice    string      `json:"sell_reference_price"`
	ShortName             string      `json:"short_name"`
	SteamMarketUrl        string      `json:"steam_market_url"`
	TransactedNum         int         `json:"transacted_num"`
}
type GoodInfo struct {
	IconUrl         string      `json:"icon_url"`
	ItemId          interface{} `json:"item_id"`
	OriginalIconUrl string      `json:"original_icon_url"`
	SteamPrice      string      `json:"steam_price"`
	SteamPriceCny   string      `json:"steam_price_cny"`
}

func GetBuff() {
	//每5秒扫描一次 查询是否有可用代理 和 可用账号 如果有则启动一个协程
	go func() {
		for {
			//查询buff抓取是否开启
			config := myDao.GetOneSystemConfig(1)
			if config.StartBuff == 0 {
				fmt.Println("buff抓取未开启")
				time.Sleep(10 * time.Second)
				continue
			}
			//查询本地代理和账号是否可用 可用则启动一个本地协程
			//查询本地代理是否可用
			buffLocalKey := rediskey.GetBuffLocalKey()
			buffLocalResult := gredis.Get(buffLocalKey)
			//查询账号是否可用
			buffUser, err := myDao.GetOneBuffUser()
			if err != nil {
				time.Sleep(10 * time.Second)
				continue
			}
			buffAccountKey := rediskey.GetBuffAccountKey(int(buffUser.ID))
			buffAccountResult := gredis.Get(buffAccountKey)
			if buffLocalResult == "" && buffAccountResult == "" {
				//开启本地代理
				go GetBuffData(0, "", buffUser)
				time.Sleep(2 * time.Second)
			} else {
				fmt.Println("buff本地代理正在抓取中...")
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

func GetBuffData(isProxy int, proxy string, account model.BuffUser) {
	//设置本地代理正在抓取中
	buffLocalKey := rediskey.GetBuffLocalKey()
	gredis.Set(buffLocalKey, "1", 0)
	//设置账号正在抓取中
	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
	gredis.Set(buffAccountKey, "1", 0)
	//修改状态
	myDao.UpdateBuffUserStatus(int(account.ID), 1)
	for {
		fmt.Println("buff start:", time.Now().Format("2006-01-02 15:04:05"))
		//循环N次 获取buff数据
		config := myDao.GetOneSystemConfig(1) //系统配置
		for i := 1; i <= config.BuffPageNum; i++ {
			geturl := fmt.Sprintf("https://buff.163.com/api/market/goods/buying?game=csgo&page_num=%v&min_price=%v&max_price=%v&sort_by=price.desc&page_size=80&use_suggestion=0&_=%v", i, config.BuffMinPrice, config.BuffMaxPrice, time.Now().UnixNano()/1e6)
			client := &http.Client{}
			//isProxy 设置代理
			if isProxy == 1 {
				proxyURL, _ := url.Parse(proxy)
				client.Transport = &http.Transport{
					Proxy: http.ProxyURL(proxyURL),
				}
			}
			req, err := http.NewRequest("GET", geturl, nil)
			if err != nil {
				fmt.Println("buff err: 发起请求失败", err)
				//结束协程
				fmt.Println("结束协程")
				//设置本地代理抓取结束
				gredis.Del(buffLocalKey)
				//设置账号抓取结束
				gredis.Del(buffAccountKey)
				myDao.UpdateBuffUserStatus(int(account.ID), 0)
				runtime.Goexit()
				return
			}
			req.AddCookie(&http.Cookie{Name: "Device-Id", Value: account.DeviceId})
			req.AddCookie(&http.Cookie{Name: "Locale-Supported", Value: "zh-Hans"})
			req.AddCookie(&http.Cookie{Name: "csrf_token", Value: account.CsrfToken})
			req.AddCookie(&http.Cookie{Name: "game", Value: "csgo"})
			req.AddCookie(&http.Cookie{Name: "remember_me", Value: account.RememberMe})
			req.AddCookie(&http.Cookie{Name: "session", Value: account.Sessionid})
			resp, err := client.Do(req)
			if err != nil {
				fmt.Println("buff err2:", err)
				endTask2(account, 0, 1)
				return
			}
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("buff err3:", err)
				//结束协程
				endTask(resp, account, 0, 1)
				return
			}
			var buffData BuffData
			err = json.Unmarshal(body, &buffData)
			if err != nil {
				fmt.Println("buff err4:", err)
				endTask(resp, account, 0, 1)
				return
			}
			if buffData.Code == "Action Forbidden" {
				fmt.Println("buff err6:", buffData.Code)
				endTask(resp, account, 3, 1)
			}
			if buffData.Code == "Login Required" {
				fmt.Println("buff err7:", buffData)
				endTask(resp, account, 2, 1)
			}
			if buffData.Code == "OK" {
				go handleBuffData(buffData)
			} else {
				fmt.Println("buff err6:", buffData)
				//结束协程
				fmt.Println("结束协程")
				endTask(resp, account, 0, 1)
			}
			config = myDao.GetOneSystemConfig(1) //系统配置
			if config.StartBuff == 0 {
				endTask(resp, account, 0, 1)
				return
			}
			//每次请求间隔
			delay := time.Duration(config.BuffDelay)
			time.Sleep(time.Second * delay)
		}
	}

}

// 处理buff数据
func handleBuffData(buffData BuffData) {
	//批量更新buff数据 不存在的话就插入
	//查询缓存是否存在该商品
	var InfoList []*model.Info
	for _, v := range buffData.Result.Items {
		key := rediskey.GetCacheKey(v.MarketHashName)
		//查询缓存
		value, _ := gredis.HGetAll(key)
		if len(value) == 0 {
			//缓存不存在
			//查询数据库
			_, err := myDao.GetOneInfoByMarketHashName(v.MarketHashName)
			if err != nil {
				//数据库不存在
				var Info model.Info
				Info.Appid = v.Appid
				Info.GoodsId = v.Id
				Info.MarketHashName = v.MarketHashName
				Info.BuffBuyPrice = util.StringToFloat64(v.BuyMaxPrice)   //buff 购买价格
				Info.BuffBuyNum = v.BuyNum                                //buff 购买数量
				Info.BuffSellPrice = util.StringToFloat64(v.SellMinPrice) //buff 出售价格
				Info.BuffSellNum = v.SellNum                              //buff 出售数量
				Info.Game = v.Game
				Info.Name = v.Name
				Info.SteamMarketUrl = v.SteamMarketUrl //steam市场链接
				Info.IconUrl = v.GoodsInfo.OriginalIconUrl
				//插入数据库
				myDao.CreateInfo(&Info)
			}
		} else {
			//缓存存在
			//批量更新数据
			var Info model.Info
			Info.Name = v.Name
			Info.MarketHashName = v.MarketHashName
			Info.BuffBuyPrice = util.StringToFloat64(v.BuyMaxPrice)   //buff 购买价格
			Info.BuffBuyNum = v.BuyNum                                //buff 购买数量
			Info.BuffSellPrice = util.StringToFloat64(v.SellMinPrice) //buff 出售价格
			Info.BuffSellNum = v.SellNum                              //buff 出售数量
			CheckPriceChange(key, 1, Info)

			InfoList = append(InfoList, &Info)

		}
		//插入缓存
		gredis.Hset(key, "buff_buy_max_price", v.BuyMaxPrice)
		gredis.Hset(key, "buff_buy_num", v.BuyNum)
		gredis.Hset(key, "buff_sell_min_price", v.SellMinPrice)
		gredis.Hset(key, "buff_sell_num", v.SellNum)
	}
	if len(InfoList) > 0 {
		//更新数据库
		myDao.BatchBuffUpdateInfo(InfoList)
	}
}

// 结束任务
func endTask(resp *http.Response, account model.BuffUser, status int, taskType int) {
	buffLocalKey := rediskey.GetBuffLocalKey()
	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
	resp.Body.Close()
	//设置本地代理抓取结束
	gredis.Del(buffLocalKey)
	//设置账号抓取结束
	gredis.Del(buffAccountKey)
	//设置账号状态
	myDao.UpdateBuffUserStatus(int(account.ID), status)
	if taskType == 1 {
		runtime.Goexit()
	}
}

// 结束任务
func endTask2(account model.BuffUser, status int, taskType int) {
	buffLocalKey := rediskey.GetBuffLocalKey()
	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
	//设置本地代理抓取结束
	gredis.Del(buffLocalKey)
	//设置账号抓取结束
	gredis.Del(buffAccountKey)
	//设置账号状态
	myDao.UpdateBuffUserStatus(int(account.ID), status)
	if taskType == 1 {
		runtime.Goexit()
	}
}
