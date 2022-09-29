package service

import (
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"fmt"
	"github.com/gocolly/colly"
	"regexp"
	"time"
)

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

func GetInfo() {
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
