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
	GoodsInfo             GoodsInfo   `json:"goods_info"`
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

// CheckBuffStatus 检测buff 状态是否开启
func CheckBuffStatus() bool {
	return true
	config := myDao.GetOneSystemConfig(1)
	if config.BuffCookie == 1 && config.StartBuff == 1 {
		return true
	} else {
		return false
	}
}

// Buff 获取buff数据
func Buff() {
	if CheckBuffStatus() {
		get()
	}
}
func get() {

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
			buffUser, _ := myDao.GetOneBuffUser()

			buffAccountKey := rediskey.GetBuffAccountKey(int(buffUser.ID))
			buffAccountResult := gredis.Get(buffAccountKey)
			if buffLocalResult == "" && buffAccountResult == "" {
				//开启本地代理
				go GetBuffData(0, "", buffUser)
			} else {
				fmt.Println("buff本地代理正在抓取中...")
			}

			////查询是否有可用代理
			//proxy := myDao.GetOneProxy(1)
			////查询是否有可用账号
			//account := myDao.GetOneAccount(1)
			//if proxy != nil && account != nil {
			//	//启动一个协程 使用代理
			//	go GetBuffData(1, proxy.Ip)
			//}
			time.Sleep(5 * time.Second)
		}
	}()
}

func GetBuffData(isProxy int, proxy string, account model.BuffUser) {
	//设置本地代理正在抓取中
	buffLocalKey := rediskey.GetBuffLocalKey()
	gredis.Set(buffLocalKey, "1", 60*60*24)
	//设置账号正在抓取中
	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
	gredis.Set(buffAccountKey, "1", 60*60*24)
	for {
		fmt.Println("buff start:", time.Now().Format("2006-01-02 15:04:05"))
		BuffConfig := myDao.GetOneBuffConfig(1)
		//循环N次 获取buff数据
		for i := 1; i <= BuffConfig.PageNum; i++ {
			geturl := fmt.Sprintf("https://buff.163.com/api/market/goods?game=csgo&page_num=%v&min_price=%v&max_price=%v&sort_by=price.asc&use_suggestion=0&_=%v", i, BuffConfig.MinPrice, BuffConfig.MaxPrice, time.Now().UnixNano()/1e6)
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
				//结束协程
				fmt.Println("结束协程")
				resp.Body.Close()
				//设置本地代理抓取结束
				gredis.Del(buffLocalKey)
				//设置账号抓取结束
				gredis.Del(buffAccountKey)
				runtime.Goexit()
				return
			}
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("buff err3:", err)
				//结束协程
				fmt.Println("结束协程")
				resp.Body.Close()
				//设置本地代理抓取结束
				gredis.Del(buffLocalKey)
				//设置账号抓取结束
				gredis.Del(buffAccountKey)
				runtime.Goexit()
				return
			}
			var buffData BuffData
			err = json.Unmarshal(body, &buffData)
			if err != nil {
				fmt.Println("buff err4:", err)
				//结束协程
				fmt.Println("结束协程")
				resp.Body.Close()
				//设置本地代理抓取结束
				gredis.Del(buffLocalKey)
				//设置账号抓取结束
				gredis.Del(buffAccountKey)
				runtime.Goexit()
				return
			}
			if buffData.Code == "Action Forbidden" {
				fmt.Println("buff err6:", buffData.Code)
				//结束协程
				fmt.Println("账号被封 结束协程")
				resp.Body.Close()
				//设置本地代理抓取结束
				gredis.Del(buffLocalKey)
				//设置账号抓取结束
				gredis.Del(buffAccountKey)
				//设置账号被封
				myDao.UpdateBuffUserStatus(int(account.ID), 3)

				runtime.Goexit()
			}
			if buffData.Code == "Login Required" {
				fmt.Println("buff err6:", buffData.Code)
				//结束协程
				fmt.Println("登录超时 结束协程")
				resp.Body.Close()
				//设置本地代理抓取结束
				gredis.Del(buffLocalKey)
				//设置账号抓取结束
				gredis.Del(buffAccountKey)
				//设置账号被封
				myDao.UpdateBuffUserStatus(int(account.ID), 2)
				runtime.Goexit()
			}
			if buffData.Code == "OK" {
				for _, v := range buffData.Result.Items {
					if v.QuickPrice != "" {
						_, err := myDao.GetGoodsByGoodsId(int64(v.Id))
						if err != nil {
							fmt.Println("buff err5:", err)
							return
						}
					}
				}
				fmt.Println("第", buffData.Result.PageNum, "页", "date:", time.Now().Format("2006-01-02 15:04:05"))
			} else {
				fmt.Println("buff err6:", buffData)
				//结束协程
				fmt.Println("结束协程")
				resp.Body.Close()
				runtime.Goexit()
			}
			//每次请求间隔25秒
			delay := time.Duration(BuffConfig.Delay) * time.Second
			time.Sleep(time.Second * delay)
		}
	}

}
