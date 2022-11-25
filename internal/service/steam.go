package service

import (
	"encoding/json"
	"fmt"
	"github.com/gocolly/colly/proxy"
	"net/http"
	"regexp"
	"strings"
	"time"

	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"github.com/gocolly/colly"
)

type T3 struct {
	Success        int         `json:"success"`
	SellOrderCount interface{} `json:"sell_order_count"`
	SellOrderPrice string      `json:"sell_order_price"`
	SellOrderTable []struct {
		Price        string `json:"price"`
		PriceWithFee string `json:"price_with_fee"`
		Quantity     string `json:"quantity"`
	} `json:"sell_order_table"`
	BuyOrderCount string `json:"buy_order_count"`
	BuyOrderPrice string `json:"buy_order_price"`
	BuyOrderTable []struct {
		Price    string `json:"price"`
		Quantity string `json:"quantity"`
	} `json:"buy_order_table"`
	HighestBuyOrder string          `json:"highest_buy_order"`
	LowestSellOrder string          `json:"lowest_sell_order"`
	BuyOrderGraph   [][]interface{} `json:"buy_order_graph"`
	SellOrderGraph  [][]interface{} `json:"sell_order_graph"`
	GraphMaxY       int             `json:"graph_max_y"`
	GraphMinX       float64         `json:"graph_min_x"`
	GraphMaxX       float64         `json:"graph_max_x"`
	PricePrefix     string          `json:"price_prefix"`
	PriceSuffix     string          `json:"price_suffix"`
}
type T2 struct {
	Success          int             `json:"success"`
	SellOrderTable   string          `json:"sell_order_table"`
	SellOrderSummary string          `json:"sell_order_summary"`
	BuyOrderTable    string          `json:"buy_order_table"`
	BuyOrderSummary  string          `json:"buy_order_summary"`
	HighestBuyOrder  string          `json:"highest_buy_order"`
	LowestSellOrder  string          `json:"lowest_sell_order"`
	BuyOrderGraph    [][]interface{} `json:"buy_order_graph"`
	SellOrderGraph   [][]interface{} `json:"sell_order_graph"`
	GraphMaxY        int             `json:"graph_max_y"`
	GraphMinX        float64         `json:"graph_min_x"`
	GraphMaxX        float64         `json:"graph_max_x"`
	PricePrefix      string          `json:"price_prefix"`
	PriceSuffix      string          `json:"price_suffix"`
}
type T4 struct {
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

func GetItemNameId() {
	key := rediskey.GetSteamItemId()
	err := gredis.Set(key, "1", time.Minute*5)
	if err != nil {
		return
	}
	c := colly.NewCollector()

	goods, _ := myDao.BatchGetGoods()
	for _, v := range goods {
		time.Sleep(time.Millisecond * 1000)

		b := c.Clone()
		b.Limit(&colly.LimitRule{
			RandomDelay: 2 * time.Second,
		})
		b.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/105.0.0.0 Safari/537.36"
		b.OnRequest(func(r *colly.Request) {
			r.Headers.Add("cookie", "sessionid=cc08c404785bc602e1ae2ef5")
		})
		b.OnResponse(func(response *colly.Response) {
			str := string(response.Body)
			re1 := regexp.MustCompile("Market_LoadOrderSpread\\(\\s*(\\d+)\\s*\\)")
			match := re1.FindString(str)
			re2 := regexp.MustCompile("[0-9]+")
			match2 := re2.FindAllString(match, -1)
			fmt.Println("name:", v.Name, "id:", match2[0])
			myDao.UpdateItemId(v.ID, match2[0])
		})
		//发送错误
		b.OnError(func(response *colly.Response, err error) {
			fmt.Println("steam 访问限制", response.StatusCode, "name:", v.Name, "err:", err)
			return
		})
		url := "https://steamcommunity.com/market/listings/" + fmt.Sprintf("%d", v.Appid) + "/" + v.MarketHashName
		b.Visit(url)
	}
	gredis.Del(key)

}

func GetSteamInfo() {
	c := colly.NewCollector(
		colly.MaxDepth(2),
		colly.Async(true),
	)
	c.Limit(&colly.LimitRule{
		Parallelism: 2,
		RandomDelay: 2 * time.Second,
	})
	c.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/105.0.0.0 Safari/537.36"
	c.OnResponse(func(response *colly.Response) {
		id := response.Ctx.Get("itemNameId")
		result, _ := BindT3(response.Body)
		fmt.Println("id:", id, "success:", result.Success, "sell_order_count:", result.SellOrderCount)
	})
	c.OnError(func(r *colly.Response, err error) {
		if r.StatusCode == 503 {
			fmt.Println(string(r.Body))
		}
		fmt.Println("steam:", r.StatusCode)
	})
	goods, _ := myDao.BatchGetGoodsItemId()
	for _, v := range goods {
		if v.SteamItemNameId == "" {
			fmt.Println("id = 空")
			continue
		}
		c.OnRequest(func(r *colly.Request) {
			r.Headers.Add("cookie", "sessionid=cc08c404785bc602e1ae2ef5")
			r.Ctx.Put("itemNameId", v.SteamItemNameId)
		})
		time.Sleep(time.Millisecond * 50)
		getUrl := "https://steamcommunity.com/market/itemordershistogram?country=PK&language=schinese&currency=23&item_nameid=" + v.SteamItemNameId + "&two_factor=0&norender=1"
		c.Visit(getUrl)
	}

	c.Wait()
}

func GetSellingPrice() {
	//https://steamcommunity.com/market/search/render/?query=&start=1&count=100&search_descriptions=0&sort_column=price&sort_dir=desc&appid=730&norender=1&currency=23
	key := rediskey.GetSteamSePriceKey()
	err := gredis.Set(key, "1", time.Minute*20)
	if err != nil {
		return
	}
	PrimitiveUrl := "https://steamcommunity.com/market/search/render/?query=&"

	c := colly.NewCollector(
		//colly.Debugger(&debug.LogDebugger{}),
		colly.Async(true), //设置为异步请求
	)
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*steamcommunity.com*",
		Parallelism: 2,
		RandomDelay: 12 * time.Second,
	})
	//设置代理
	if p, proxyerr := proxy.RoundRobinProxySwitcher(

		"http://185.199.231.45:8382",
		//"http://188.74.210.207:6286",
		//"http://188.74.183.10:8279",
		//"http://188.74.210.21:6100",
		"http://45.155.68.129:8133",
		"http://154.95.36.199:6893",
		//"http://45.94.47.66:8110",
		"http://144.168.217.88:8780",
	); proxyerr == nil {
		c.SetProxyFunc(p)
	}
	c.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/107.0.0.0 Safari/537.36"

	cookie := []*http.Cookie{
		{
			Name:  "sessionid",
			Value: "dc75823731edb06e3cdf0f50",
		},
		{
			Name:  "steamLoginSecure",
			Value: "76561198385127796%7C%7CeyAidHlwIjogIkpXVCIsICJhbGciOiAiRWREU0EiIH0.eyAiaXNzIjogInI6MEM2NF8yMUE4NkVBNF84ODU4NCIsICJzdWIiOiAiNzY1NjExOTgzODUxMjc3OTYiLCAiYXVkIjogWyAid2ViIiBdLCAiZXhwIjogMTY2OTQ0MTQzMSwgIm5iZiI6IDE2NjA3MTQ2MjAsICJpYXQiOiAxNjY5MzU0NjIwLCAianRpIjogIjBDNjlfMjFBODZFQ0ZfOUI0MzMiLCAib2F0IjogMTY2OTM1NDYyMCwgInJ0X2V4cCI6IDE2ODc1ODI1MDgsICJwZXIiOiAwLCAiaXBfc3ViamVjdCI6ICIxMDMuMjIwLjc5LjExMCIsICJpcF9jb25maXJtZXIiOiAiMTAzLjIyMC43OS4xMTAiIH0.r8IhWk4iTOKpZLVuMYMyNcKIVoih6EWPhb7sM_pcNIxVebGitNWXipPLK7CEx4PI4HzdNCLua7nffqww0JZhCw",
		},
		{
			Name:  "Steam_Language",
			Value: "tchinese",
		},
		{
			Name:  "browserid",
			Value: "2708381203747910562",
		},
		{
			Name:  "steamCountry",
			Value: "HK|8ad7d7ea3737e06297549f92142430ad",
		},
	} //设置cookie

	c.SetCookies("https://steamcommunity.com", cookie)

	c.OnRequest(func(r *colly.Request) {

	})

	c.OnResponse(func(r *colly.Response) {
		data, _ := BindT4(r.Body)
		isUpdateCookie := InsertSteamGoods(data)
		if isUpdateCookie {
			siteCokkie := c.Cookies("https://steamcommunity.com")
			fmt.Println("siteCokkie:", siteCokkie)
			errCookie := c.SetCookies("https://steamcommunity.com", siteCokkie)
			if errCookie != nil {
				fmt.Println("设置cookie失败")
			}
		}
	})
	//发送错误
	c.OnError(func(r *colly.Response, err error) {

		fmt.Println("抓取steam错误:", r.StatusCode, "当前代理", r.Request.ProxyURL, "err:", err, "string:", string(r.Body))
		//r.Request.ProxyURL = ""
		r.Request.Retry()
	})
	var start = 0
	for i := 1; i <= 60; i++ {
		Url := PrimitiveUrl + "start=" + fmt.Sprintf("%d", start) +
			"&count=" + fmt.Sprintf("%d", 100) +
			"&search_descriptions=" + fmt.Sprintf("%d", 0) +
			"&sort_column=price" +
			"&sort_dir=desc" +
			"&appid=730" +
			"&norender=1" +
			"&currency=23"
		fmt.Println("url:", Url)
		c.Visit(Url)
		start = start + 100
	}
	c.Visit(PrimitiveUrl)

	c.Wait()
	fmt.Println("抓取结束")
	gredis.Del(key)
}

func InsertSteamGoods(list T4) bool {
	isUpdateCookie := false
	for _, v := range list.Results {
		fmt.Println("商品名称:", v.HashName, "商品价格分:", v.SellPrice, "商品价格text:", v.SellPriceText)
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
			//更新比例
			go UpdateGoodsProportion(goods.GoodsId)
		} else {
			fmt.Println("不是人民币 需要重新获取cookie")
			isUpdateCookie = true
		}
	}
	return isUpdateCookie

}

// 绑定数据 T4
func BindT4(data []byte) (T4, error) {
	var Steam T4
	str := string(data)
	err := json.Unmarshal([]byte(str), &Steam)
	if err != nil {
		fmt.Println("json err :", err)
		return Steam, err
	}
	return Steam, err
}

// 绑定数据
func BindT3(data []byte) (T3, error) {
	var SteamInfo T3
	str := string(data)
	err := json.Unmarshal([]byte(str), &SteamInfo)
	if err != nil {
		fmt.Println("json err :", err)
		return SteamInfo, err
	}
	return SteamInfo, err
}
