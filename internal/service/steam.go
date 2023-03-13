package service

import (
	"buff-go/global"
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"encoding/json"
	"fmt"
	"golang.org/x/net/proxy"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type SteamGoodsInfo struct {
	Success    bool `json:"success"`
	Start      int  `json:"start"`
	Pagesize   int  `json:"pagesize"`
	TotalCount int  `json:"total_count"`
	Searchdata struct {
		Query              string `json:"query"`
		SearchDescriptions bool   `json:"search_descriptions"`
		TotalCount         int    `json:"total_count"`
		Pagesize           int    `json:"pagesize"`
		Prefix             string `json:"prefix"`
		ClassPrefix        string `json:"class_prefix"`
	} `json:"searchdata"`
	Results []struct {
		Name             string `json:"name"`
		HashName         string `json:"hash_name"`
		SellListings     int    `json:"sell_listings"`
		SellPrice        int    `json:"sell_price"`
		SellPriceText    string `json:"sell_price_text"`
		AppIcon          string `json:"app_icon"`
		AppName          string `json:"app_name"`
		AssetDescription struct {
			Appid           int    `json:"appid"`
			Classid         string `json:"classid"`
			Instanceid      string `json:"instanceid"`
			Currency        int    `json:"currency"`
			BackgroundColor string `json:"background_color"`
			IconUrl         string `json:"icon_url"`
			IconUrlLarge    string `json:"icon_url_large"`
			Descriptions    []struct {
				Type  string `json:"type"`
				Value string `json:"value"`
				Color string `json:"color,omitempty"`
			} `json:"descriptions"`
			Tradable int `json:"tradable"`
			Actions  []struct {
				Link string `json:"link"`
				Name string `json:"name"`
			} `json:"actions,omitempty"`
			Name           string `json:"name"`
			NameColor      string `json:"name_color"`
			Type           string `json:"type"`
			MarketName     string `json:"market_name"`
			MarketHashName string `json:"market_hash_name"`
			MarketActions  []struct {
				Link string `json:"link"`
				Name string `json:"name"`
			} `json:"market_actions,omitempty"`
			Commodity                 int `json:"commodity"`
			MarketTradableRestriction int `json:"market_tradable_restriction"`
			Marketable                int `json:"marketable"`
			OwnerDescriptions         []struct {
				Type  string `json:"type"`
				Value string `json:"value"`
				Color string `json:"color,omitempty"`
			} `json:"owner_descriptions,omitempty"`
			Fraudwarnings []string `json:"fraudwarnings,omitempty"`
		} `json:"asset_description"`
		SalePriceText string `json:"sale_price_text"`
	} `json:"results"`
}

func GetSteam() {
	//每5秒扫描一次 查询是否有可用代理 和 可用账号 如果有则启动一个协程
	go func() {
		for {
			//查询steam抓取是否开启
			config := myDao.GetOneSystemConfig(1)
			if config.StartSteamSell == 0 {
				fmt.Println("steam出售抓取未开启")
				time.Sleep(10 * time.Second)
				continue
			}
			//查询本地代理和账号是否可用 可用则启动一个本地协程
			//查询本地代理是否可用
			steamProxyKey := rediskey.GetProxySteamKey("127.0.0.1")
			steamLocalResult := gredis.Get(steamProxyKey)
			//查询账号是否可用
			steamUser, err := myDao.GetOneSteamUser(1)
			if err != nil {
				time.Sleep(10 * time.Second)
				continue
			}
			steamAccountKey := rediskey.GetSteamAccountKey(int(steamUser.ID))
			steamAccountResult := gredis.Get(steamAccountKey)
			if steamLocalResult == "" && steamAccountResult == "" {
				//开启本地代理
				Ip := model.Ip{Ip: "127.0.0.1", Port: 80, Type: 2}
				go GetSteamData(0, &Ip, steamUser)
				time.Sleep(2 * time.Second)
			} else {
				fmt.Println("steam本地代理正在抓取中...")

			}

			//查询是否有可用代理
			oneIp, err := myDao.GetOneIp(2)
			if err != nil {
				fmt.Println("没有可用代理")
				time.Sleep(10 * time.Second)
				continue
			}
			proxyKey := rediskey.GetProxySteamKey(oneIp.Ip + ":" + strconv.Itoa(oneIp.Port))
			proxyKeyResult := gredis.Get(proxyKey)

			//查询是否有可用账号
			account, err := myDao.GetOneSteamUser(1)
			if err != nil {
				fmt.Println("没有可用账号")
				time.Sleep(10 * time.Second)
				continue
			}
			accountKey := rediskey.GetSteamAccountKey(int(account.ID))
			accountKeyResult := gredis.Get(accountKey)

			if proxyKeyResult == "" && accountKeyResult == "" {
				fmt.Println("代理和账号都可用 启动协程", oneIp.Ip, account.Account)
				//启动一个协程 使用代理
				go GetSteamData(1, oneIp, account)
			} else {
				fmt.Println("proxyKey", proxyKey)
				fmt.Println("accountKey", accountKey)
				//fmt.Println("代理或账号正在抓取中", oneIp.Ip, account.Account)
				time.Sleep(10 * time.Second)
				continue
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

func GetSteamData(isProxy int, Ip *model.Ip, account model.SteamUser) {
	p := Ip.Ip + ":" + strconv.Itoa(Ip.Port)
	//设置代理正在抓取中
	steamProxyKey := rediskey.GetProxySteamKey(p)
	gredis.Set(steamProxyKey, "1", 0)
	//设置账号正在抓取中
	steamAccountKey := rediskey.GetSteamAccountKey(int(account.ID))
	gredis.Set(steamAccountKey, "1", 0)
	//修改状态
	myDao.UpdateSteamUserStatus(int(account.ID), 1)
	for {
		fmt.Println("steam start:", time.Now().Format("2006-01-02 15:04:05"))
		config := myDao.GetOneSystemConfig(1)
		//循环N次 获取steam数据
		var start = 0
		for i := 1; i <= config.SteamPageNum; i++ {
			geturl := fmt.Sprintf("https://steamcommunity.com/market/search/render/?query=&start=%v&count=100&search_descriptions=0&sort_column=price&sort_dir=desc&appid=730&norender=1&currency=23", start)
			start = start + 100
			client := &http.Client{}
			//isProxy 设置代理
			if isProxy == 1 {
				if Ip.ProxyType == 1 {
					proxyURL, _ := url.Parse(p)
					client.Transport = &http.Transport{
						Proxy: http.ProxyURL(proxyURL),
					}
				} else {
					//socks5
					str := "socks5://" + Ip.Name + ":" + Ip.PassWord + "@" + Ip.Ip + ":" + strconv.Itoa(Ip.Port)
					proxyUrl, _ := url.Parse(str)
					dialer, _ := proxy.FromURL(proxyUrl, proxy.Direct)
					// 创建 HTTP 客户端，并指定代理
					client.Transport = &http.Transport{
						Dial: dialer.Dial,
					}
				}
			}
			req, err := http.NewRequest("GET", geturl, nil)
			if err != nil {
				fmt.Println("steam err: 发起请求失败", err)
				//结束协程
				fmt.Println("结束协程")
				//设置代理抓取结束
				gredis.Del(steamProxyKey)
				//设置账号抓取结束
				gredis.Del(steamAccountKey)
				myDao.UpdateSteamUserStatus(int(account.ID), 0)
				runtime.Goexit()
				return
			}
			req.AddCookie(&http.Cookie{Name: "steamCountry", Value: account.SteamCountry})
			req.AddCookie(&http.Cookie{Name: "timezoneOffset", Value: "28800,0"})
			req.AddCookie(&http.Cookie{Name: "browserid", Value: account.BrowserId})
			req.AddCookie(&http.Cookie{Name: "Steam_Language", Value: "schinese"})
			req.AddCookie(&http.Cookie{Name: "steamLoginSecure", Value: account.SteamLoginSecure})
			req.AddCookie(&http.Cookie{Name: "sessionid", Value: account.SessionId})
			resp, err := client.Do(req)
			if err != nil {
				fmt.Println("steam err2:", err)
				endSteamTask(resp, account, 0, 1, p)
				return
			}
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("steam err3:", err)
				//结束协程
				endSteamTask(resp, account, 0, 1, p)
				return
			}
			var SteamData SteamGoodsInfo
			err = json.Unmarshal(body, &SteamData)
			if err != nil {
				fmt.Println("steam err4:", err)
				endSteamTask(resp, account, 0, 1, p)
				return
			}
			if SteamData.Success == false {
				fmt.Println("steam err6:", SteamData)
				endSteamTask(resp, account, 2, 1, p)
			}

			if SteamData.Success {
				go func() {
					a := handleSteamData(SteamData)
					if a {
						//结束协程
						endSteamTask(resp, account, 2, 1, p)
					}
				}()
			} else {
				fmt.Println("steam err7:", SteamData)
				//结束协程
				fmt.Println("结束协程")
				endSteamTask(resp, account, 3, 1, p)
			}
			config = myDao.GetOneSystemConfig(1)
			if config.StartSteamSell == 0 {
				//结束协程
				fmt.Println("结束协程")
				endSteamTask(resp, account, 0, 1, p)
				return
			}
			//每次请求间隔
			delay := time.Duration(config.SteamDelay)
			time.Sleep(time.Second * delay)
		}
	}
}

// 处理steam数据
func handleSteamData(steamData SteamGoodsInfo) bool {
	//批量更新steam数据 不存在的话就插入
	isUpdateCookie := false
	for _, v := range steamData.Results {
		global.Logger.Println("steam - 商品名称:", v.HashName, "商品价格分:", v.SellPrice, "商品价格text:", v.SellPriceText)
		fmt.Println("steam - 商品名称:", v.HashName, "商品价格分:", v.SellPrice, "商品价格text:", v.SellPriceText)
		//检查是否有 ¥ 符号
		if strings.Contains(v.SellPriceText, "¥") {
			//查找是否存在 存在更新steam出售价格
			goods, err := myDao.GetGoodsBySteamItemNameId(v.HashName)
			if err != nil {
				fmt.Println("查询商品错误:", err)
				continue
			}
			if goods.MarketHashName == "" {
				fmt.Println("商品不存在")
				continue
			}
			//更新商品价格
			err = myDao.UpdateGoodsPrice(goods, v.SellPrice)
			if err != nil {
				fmt.Println("更新商品价格错误:", err)
				continue
			}
			//判断商品价格是否变动 如果变动就通知telegram
			isNeedUpdateSteamData(goods, v.SellPrice)
		} else {
			fmt.Println("不是人民币 需要重新获取cookie")
			isUpdateCookie = true
			return true
		}
	}
	return isUpdateCookie
}

// 是否需要更新steam数据 通知 更新比例
func isNeedUpdateSteamData(info *model.Goods, steamSellPrice int) {
	//查询缓存中的steam数据 与数据库中的steam数据对比 有变化的话就发送telegram消息 更新比例和更新缓存
	//查询缓存中的steam数据
	goodsCacheKey := rediskey.GetCacheKey(info.MarketHashName)
	SteamSellPrice := gredis.Hget(goodsCacheKey, "steam_sell_price")
	buyMaxPrice := gredis.Hget(goodsCacheKey, "buy_max_price")
	if buyMaxPrice == "" {
		gredis.Hset(goodsCacheKey, "buy_max_price", info.BuyMaxPrice)
	}
	if SteamSellPrice == "" {
		//缓存中没有数据
		//更新缓存
		gredis.Hset(goodsCacheKey, "steam_sell_price", util.IntToFloat64(steamSellPrice)/100)
	} else {
		//缓存中有数据
		//比较价格
		if util.StringToFloat64(SteamSellPrice) != util.IntToFloat64(steamSellPrice)/100 {
			//价格变化
			//更新缓存
			gredis.Hset(goodsCacheKey, "steam_sell_price", util.IntToFloat64(steamSellPrice)/100)
			//更新比例
			P := info.BuyMaxPrice / (util.IntToFloat64(steamSellPrice) / 100)
			err := myDao.UpdateGoodsRatioByGoodsId(info.GoodsId, P)
			if err != nil {
				return
			}
			//发送telegram消息
			config := myDao.GetOneSystemConfig(1)
			info.Proportion = P
			if info.Proportion >= config.BotProportion {
				info.SteamSellPrice = float64(steamSellPrice) / 100
				sendTelegram(info, 2)
			}
		}
	}

}

// 结束steam任务
func endSteamTask(resp *http.Response, account model.SteamUser, status int, taskType int, proxy string) {
	steamProxyKey := rediskey.GetProxySteamKey(proxy)
	steamAccountKey := rediskey.GetSteamAccountKey(int(account.ID))
	resp.Body.Close()
	//设置本地代理抓取结束
	gredis.Del(steamProxyKey)
	//设置账号抓取结束
	gredis.Del(steamAccountKey)
	//设置账号状态
	myDao.UpdateSteamUserStatus(int(account.ID), status)
	if taskType == 1 {
		runtime.Goexit()
	}
}
