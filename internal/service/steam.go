package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
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

// CheckSteamStatus 检测steam 状态是否开启
func CheckSteamStatus() bool {
	return true
	config := myDao.GetOneSystemConfig(1)
	if config.SteamCookie == 1 && config.StartSteamSell == 1 {
		return true
	} else {
		return false
	}
}

// Buff 获取buff数据
func Steam() {
	if CheckSteamStatus() {
		getSteam()
	}
}
func getSteam() {

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
			steamLocalKey := rediskey.GetSteamLocalKey()
			steamLocalResult := gredis.Get(steamLocalKey)
			//查询账号是否可用
			steamUser, err := myDao.GetOneSteamUser()
			if err != nil {
				time.Sleep(10 * time.Second)
				continue
			}
			steamAccountKey := rediskey.GetBuffAccountKey(int(steamUser.ID))
			steamAccountResult := gredis.Get(steamAccountKey)
			if steamLocalResult == "" && steamAccountResult == "" {
				//开启本地代理
				go GetSteamData(0, "", steamUser)
				time.Sleep(2 * time.Second)
			} else {
				fmt.Println("steam本地代理正在抓取中...")
			}

			////查询是否有可用代理
			//proxy := myDao.GetOneProxy(1)
			////查询是否有可用账号
			//account := myDao.GetOneAccount(1)
			//if proxy != nil && account != nil {
			//	//启动一个协程 使用代理
			//	go GetBuffData(1, proxy.Ip)
			//}
			time.Sleep(10 * time.Second)
		}
	}()
}

func GetSteamData(isProxy int, proxy string, account model.SteamUser) {
	//设置本地代理正在抓取中
	steamLocalKey := rediskey.GetSteamLocalKey()
	gredis.Set(steamLocalKey, "1", 0)
	//设置账号正在抓取中
	steamAccountKey := rediskey.GetSteamAccountKey(int(account.ID))
	gredis.Set(steamAccountKey, "1", 0)
	//修改状态
	myDao.UpdateSteamUserStatus(int(account.ID), 1)
	for {
		fmt.Println("steam start:", time.Now().Format("2006-01-02 15:04:05"))
		SteamConfig := myDao.GetOneSteamConfig(1)
		//循环N次 获取buff数据
		var start = 0
		for i := 1; i <= 80; i++ {
			geturl := fmt.Sprintf("https://steamcommunity.com/market/search/render/?query=&start=%v&count=100&search_descriptions=0&sort_column=price&sort_dir=desc&appid=730&norender=1&currency=23", start)
			start = start + 100
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
				fmt.Println("steam err: 发起请求失败", err)
				//结束协程
				fmt.Println("结束协程")
				//设置本地代理抓取结束
				gredis.Del(steamLocalKey)
				//设置账号抓取结束
				gredis.Del(steamAccountKey)
				myDao.UpdateSteamUserStatus(int(account.ID), 0)
				runtime.Goexit()
				return
			}
			//req.AddCookie(&http.Cookie{Name: "Device-Id", Value: account.DeviceId})
			//req.AddCookie(&http.Cookie{Name: "Locale-Supported", Value: "zh-Hans"})
			//req.AddCookie(&http.Cookie{Name: "csrf_token", Value: account.CsrfToken})
			//req.AddCookie(&http.Cookie{Name: "game", Value: "csgo"})
			//req.AddCookie(&http.Cookie{Name: "remember_me", Value: account.RememberMe})
			req.AddCookie(&http.Cookie{Name: "session", Value: account.Sessionid})
			resp, err := client.Do(req)
			if err != nil {
				fmt.Println("steam err2:", err)
				endSteamTask(resp, account, 0, 1)
				return
			}
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("steam err3:", err)
				//结束协程
				endSteamTask(resp, account, 0, 1)
				return
			}
			var SteamData SteamGoodsInfo
			err = json.Unmarshal(body, &SteamData)
			if err != nil {
				fmt.Println("steam err4:", err)
				endSteamTask(resp, account, 0, 1)
				return
			}
			if SteamData.Success {
				fmt.Println("buff err6:", SteamData)
				endSteamTask(resp, account, 3, 1)
			}

			if SteamData.Success {
				go handleSteamData(SteamData)
			} else {
				fmt.Println("buff err6:", SteamData)
				//结束协程
				fmt.Println("结束协程")
				endSteamTask(resp, account, 0, 1)
			}
			//每次请求间隔
			delay := time.Duration(SteamConfig.Delay)
			time.Sleep(time.Second * delay)
		}
	}

}

// 处理buff数据
func handleSteamData(steamData SteamGoodsInfo) {
	//批量更新steam数据 不存在的话就插入

}

// 结束steam任务
func endSteamTask(resp *http.Response, account model.SteamUser, status int, taskType int) {
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
