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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var itemChan = make(chan *model.Info, 20000)

// steam 出售
func GetSteamSell() {
	//每5秒扫描一次 查询是否有可用代理 和 可用账号 如果有则启动一个协程
	for {
		//查询steam抓取是否开启
		config := myDao.GetOneSystemConfig(1)
		if config.SteamSellStatus == 0 {
			fmt.Println("steam出售抓取未开启")
			time.Sleep(10 * time.Second)
			continue
		}

		//查询是否有可用账号
		steamUser, err := myDao.GetOneSteamUser(1)
		if err != nil {
			fmt.Println("没有可用账号")
			time.Sleep(10 * time.Second)
			continue
		}
		//获取一个代理
		ip, address, key, _ := GetOneProxy("steam")
		if address != "" {
			myDao.UpdateSteamUserStatus(int(steamUser.ID), 1)
			fmt.Println("账号", steamUser.Account)
			gredis.Set(key, steamUser.ID, 0)
			fmt.Println("代理", ip)
			go GetSteamSellData(1, ip, steamUser)
		} else {
			fmt.Println("无可用代理")
			time.Sleep(10 * time.Second)
		}

	}

}

func GetSteamSellData(isProxy int, Ip *model.Ip, account model.SteamUser) {
	address := Ip.Ip + ":" + strconv.Itoa(Ip.Port)
	key := rediskey.GetProxyMapKey(address, "steam")
	for {
		fmt.Println("steam start:", time.Now().Format("2006-01-02 15:04:05"), "isProxy:", isProxy, "Ip:", address, "account:", account.Account)
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
					proxyURL, _ := url.Parse(Ip.Ip + ":" + strconv.Itoa(Ip.Port))
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
				gredis.Del(key)

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
				endSteamTask(resp, account, 0, 1, address, 1, 0)
				return
			}
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("steam err3:", err)
				//结束协程
				endSteamTask(resp, account, 0, 1, address, 0, 0)
				return
			}
			var SteamData SteamGoodsInfo
			err = json.Unmarshal(body, &SteamData)
			if err != nil {
				fmt.Println("steam err4 代理:", address, "账号:", account.Account, "获取数据失败", "data:", SteamData)
				endSteamTask(resp, account, 0, 1, address, 0, 0)
				return
			}
			if SteamData.Success == false {
				fmt.Println("steam err6 代理:", address, "账号:", account.Account, "获取数据失败", "body:", string(body))
				endSteamTask(resp, account, 0, 1, address, 0, 1)

			}

			if SteamData.Success {
				if len(SteamData.Results) > 0 && strings.Contains(SteamData.Results[0].SellPriceText, "¥") {
					go handleSteamSellData(SteamData)
				} else {
					if len(SteamData.Results) > 0 && strings.Contains(SteamData.Results[0].SellPriceText, "$") {
						fmt.Println("steam err5:", "不是人民币->data", SteamData)
						//结束协程
						fmt.Println("结束协程")
						endSteamTask(resp, account, 2, 1, address, 0, 0)
					} else {
						fmt.Println("steam err5-2:", "data:", SteamData)
						continue
					}
				}
			} else {
				fmt.Println("steam err7 代理:", address, "账号:", account.Account, "获取数据失败", "data:", SteamData)
				//结束协程
				fmt.Println("结束协程")
				endSteamTask(resp, account, 0, 1, address, 0, 0)
			}
			config = myDao.GetOneSystemConfig(1)
			if config.SteamSellStatus == 0 {
				//结束协程
				fmt.Println("结束协程")
				endSteamTask(resp, account, 0, 1, address, 0, 0)
				return
			}
			//每次请求间隔
			delay := time.Duration(config.SteamSellDelay)
			time.Sleep(time.Second * delay)
		}
	}
}

// 处理steam数据
func handleSteamSellData(steamData SteamGoodsInfo) {

	for _, v := range steamData.Results {

		fmt.Println("steam -sell - name:", v.Name, "| 价格:", v.SellPriceText, "| 数量:", v.SellListings)
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
				Info.SteamSellPrice = float64(v.SellPrice) / 100 //steam 出售价格
				Info.SteamSellNum = v.SellListings               //steam 出售数量
				Info.Name = v.Name
				//插入数据库
				myDao.CreateInfo(&Info)
			}
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
		} else {
			//缓存存在
			//更新缓存
			gredis.Hset(key, "steam_sell_price", util.IntToFloat64(v.SellPrice)/100)
			gredis.Hset(key, "steam_sell_num", v.SellListings)
			gredis.Hset(key, "name", v.Name)
			gredis.Hset(key, "market_hash_name", v.AssetDescription.MarketHashName)
			gredis.Hset(key, "steam_sell_update", time.Now().Unix())
		}
		if len(value) > 0 {
			//lpush 价格变动队列
			listKey := rediskey.CheckPriceList()
			a, _ := json.Marshal(value)
			var data map[string]interface{}
			data = make(map[string]interface{})
			data["key"] = key
			data["type"] = 4
			data["value"] = a
			jsonStr, _ := json.Marshal(data)
			gredis.LPush(listKey, jsonStr)
		}

	}
}

// 结束steam任务
func endSteamTask(resp *http.Response, account model.SteamUser, status int, taskType int, proxy string, p int, proxyStatus int) {
	steamProxyKey := rediskey.GetProxyMapKey(proxy, "steam")
	if p == 1 {
		resp.Body.Close()
	}
	if proxyStatus == 0 {
		//设置本地代理抓取结束
		gredis.Del(steamProxyKey)
	} else if proxyStatus == 1 {
		//代理失效 设置半小时 不可用
		gredis.Set(steamProxyKey, "请求频繁", 1800)
	}

	//设置账号状态
	myDao.UpdateSteamUserStatus(int(account.ID), status)
	if taskType == 1 {
		runtime.Goexit()
	}
}

func GetSteamBuy() {
	go func() {
		for {
			if len(itemChan) <= 500 {
				//读取所有商品 写入channel
				result, _ := myDao.GetAllInfoBySteamItemId()
				for _, v := range result {
					if v.SteamSellPrice >= 80 {
						itemChan <- v
					}
				}
				fmt.Println("steam buy 通道写入完成 写入数量：", len(itemChan))
			}
			time.Sleep(time.Second * 2)
		}
	}()

	for {
		config := myDao.GetOneSystemConfig(1)
		if config.SteamBuyStatus == 0 {
			fmt.Println("steam 求购任务已关闭")
			time.Sleep(time.Second * 10)
			continue
		}
		info := <-itemChan
		if info == nil {
			fmt.Println("求购通道没有商品")
			time.Sleep(time.Second * 10)
			continue
		}
		go GetSteamBuyData(info)
		delay := time.Duration(config.SteamBuyDelay)
		time.Sleep(time.Millisecond * delay)
	}

}

// steam 求购
func GetSteamBuyData(info *model.Info) {
	getUrl := fmt.Sprintf("https://steamcommunity.com/market/itemordershistogram?country=CN&language=schinese&currency=23&item_nameid=%s&two_factor=0", info.SteamItemNameId)
	urli := url.URL{}
	proxyURL := fmt.Sprintf("http://%s", "proxy.ipidea.io:2336")
	urlProxy, _ := urli.Parse(proxyURL)
	urlProxy.User = url.UserPassword("zhaochengyuan-zone-static", "zhaochengyuan")

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(urlProxy),
		},
	}
	req, err := http.NewRequest("GET", getUrl, nil)
	if err != nil {
		//fmt.Println("buff err1:", err)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("steam err2:", err)
		return
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("steam err3:", err)
		return
	}
	var steamResp SteamSell
	err = json.Unmarshal(body, &steamResp)
	if err != nil {
		fmt.Println("steam err4:", err)
		return
	}
	if steamResp.Success == 1 {
		key := rediskey.GetCacheKey(info.MarketHashName)
		oldCache, _ := gredis.HGetAll(key)
		info.SteamSellPrice = util.StringToFloat64(steamResp.LowestSellOrder) / 100
		info.SteamBuyPrice = util.StringToFloat64(steamResp.HighestBuyOrder) / 100
		info.SteamBuyUpdate = int(time.Now().Unix())
		gredis.Hset(key, "steam_sell_price", info.SteamSellPrice)
		gredis.Hset(key, "steam_buy_price", info.SteamBuyPrice)
		//根据id更新steam数据
		myDao.UpdateInfo(info)
		//检查价格变动
		CheckPriceChange(key, 3, oldCache)
		fmt.Println("steam - buy - name:", info.MarketHashName, "| 出售价格:", info.SteamSellPrice, "| 求购价", info.SteamBuyPrice)
	}
}

func getItemId(info *model.Info) {

	getUrl := fmt.Sprintf("https://steamcommunity.com/market/listings/730/%v", info.MarketHashName)
	urli := url.URL{}
	proxyURL := fmt.Sprintf("http://%s", "proxy.ipidea.io:2336")
	urlProxy, _ := urli.Parse(proxyURL)
	urlProxy.User = url.UserPassword("zhaochengyuan-zone-static", "zhaochengyuan")

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(urlProxy),
		},
	}
	req, err := http.NewRequest("GET", getUrl, nil)
	if err != nil {
		//fmt.Println("buff err1:", err)
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("buff err2:", err)
		return
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("buff err3:", err)
		return
	}
	re1 := regexp.MustCompile("Market_LoadOrderSpread\\(\\s*(\\d+)\\s*\\)")
	match := re1.FindString(string(body))
	re2 := regexp.MustCompile("[0-9]+")
	match2 := re2.FindAllString(match, -1)
	if len(match2) == 0 {
		fmt.Println("获取商品id失败", getUrl)
		return
	}
	i := match2[0]
	info.SteamItemNameId = i
	myDao.UpdateInfoBySteamItemId(info)
	fmt.Println("获取商品id成功", info.MarketHashName, "id:", i)
}
