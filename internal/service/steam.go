package service

import (
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

func GetSteamBuy() {
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
				go GetSteamBuyData(0, &Ip, steamUser)
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
				go GetSteamBuyData(1, oneIp, account)
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

func GetSteamBuyData(isProxy int, Ip *model.Ip, account model.SteamUser) {
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
				if strings.Contains(SteamData.Results[0].SellPriceText, "¥") {
					go handleSteamBuyData(SteamData)
				} else {
					fmt.Println("steam err5:", "不是人民币")
					//结束协程
					fmt.Println("结束协程")
					endSteamTask(resp, account, 3, 1, p)
				}
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
func handleSteamBuyData(steamData SteamGoodsInfo) {
	var InfoList []*model.Info

	for _, v := range steamData.Results {
		fmt.Println("steam - name:", v.Name, "- price:", v.SellPriceText, "- num:", v.SellListings)
		key := rediskey.GetCacheKey(v.AssetDescription.MarketHashName)
		var Info model.Info
		//查询缓存
		value, _ := gredis.HGetAll(key)
		if len(value) == 0 {

			//缓存不存在
			//查询数据库
			_, err := myDao.GetOneInfoByMarketHashName(v.AssetDescription.MarketHashName)
			if err != nil {
				//数据库不存在
				Info.MarketHashName = v.AssetDescription.MarketHashName
				//Info.SteamBuyPrice = util.StringToFloat64(v.BuyMaxPrice) //steam 购买价格
				//Info.SteamBuyNum = v.BuyNum 						   //steam 购买数量
				Info.SteamSellPrice = float64(v.SellPrice) / 100 //steam 出售价格
				Info.SteamSellNum = v.SellListings               //steam 出售数量
				Info.Name = v.Name
				//插入数据库
				myDao.CreateInfo(&Info)
				//初始化缓存
				c := CacheData{
					Key:            key,
					id:             0,
					name:           v.Name,
					marketHashName: v.AssetDescription.MarketHashName,
					BuffBuyPrice:   0,
					BuffBuyNum:     0,
					BuffSellPrice:  0,
					BuffSellNum:    0,
					SteamBuyPrice:  0,
					SteamBuyNum:    0,
					SteamSellPrice: float64(v.SellPrice) / 100,
					SteamSellNum:   v.SellListings,
				}
				InitGoodCache(c)
			}
		} else {
			//缓存存在
			//批量更新数据
			//Info.SteamBuyPrice = util.StringToFloat64(v.BuyMaxPrice) //buff 购买价格
			//Info.SteamBuyNum = v.BuyNum 						   //buff 购买数量
			Info.SteamSellPrice = util.IntToFloat64(v.SellPrice) / 100 //buff 出售价格
			Info.SteamSellNum = v.SellListings                         //buff 出售数量
			Info.MarketHashName = v.AssetDescription.MarketHashName
			Info.Name = v.Name
			InfoList = append(InfoList, &Info)
			//插入缓存
			gredis.Hset(key, "steam_sell_price", util.IntToFloat64(v.SellPrice)/100)
			gredis.Hset(key, "steam_sell_num", v.SellListings)
		}
		if len(value) > 0 {
			//比对价格是否有变动
			CheckPriceChange(key, 2, value)
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
