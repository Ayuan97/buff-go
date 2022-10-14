package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/gocolly/colly"

	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
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
		result, _ := Bind(response.Body)
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

func GetTest() {
	url := "https://httpbin.org/delay/2"

	// Instantiate default collector
	c := colly.NewCollector(
		// Attach a debugger to the collector
		//colly.Debugger(&debug.LogDebugger{}),
		colly.Async(true),
	)

	// Limit the number of threads started by colly to two
	// when visiting links which domains' matches "*httpbin.*" glob
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*httpbin.*",
		Parallelism: 2,
		RandomDelay: 1 * time.Second,
	})

	c.OnResponse(func(response *colly.Response) {
		fmt.Println("url3:", response.Request.URL)
		//fmt.Println("url:", response.Request.URL.String())
		//fmt.Println("time:",time.Now())
	})
	// Start scraping in four threads on https://httpbin.org/delay/2
	for i := 0; i < 10; i++ {
		url2 := fmt.Sprintf("%s?n=%d", url, i)
		fmt.Println(url2)
		c.Visit(url2)
	}
	fmt.Println(url)
	// Start scraping on https://httpbin.org/delay/2
	c.Visit(url)
	// Wait until threads are finished
	c.Wait()
}

//绑定数据
func Bind(data []byte) (T3, error) {
	var SteamInfo T3
	str := string(data)
	err := json.Unmarshal([]byte(str), &SteamInfo)
	if err != nil {
		fmt.Println("json err :", err)
		return SteamInfo, err
	}
	return SteamInfo, err
}
