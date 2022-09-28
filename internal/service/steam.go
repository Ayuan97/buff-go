package service

import (
	"buff-go/pkg/util"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
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
	goods, _ := myDao.BatchGetGoods()
	for _, v := range goods {
		num := randNum()
		num = 10 + num
		time.Sleep(time.Millisecond * time.Duration(num))
		//name := url.QueryEscape(v.MarketHashName)
		//url := "https://steamcommunity.com/market/priceoverview/?appid=730&currency=23&market_hash_name="+name
		url := "https://steamcommunity.com/market/itemordershistogram?country=CN&language=schinese&currency=23&item_nameid=176057786&two_factor=0"
		//url := "https://steamcommunity.com/market/listings/" + fmt.Sprintf("%d", v.Appid) + "/" + v.MarketHashName
		go GetSteamInfo(url, v.MarketHashName)
	}

}

//获取商品
func GetSteamInfo(getUrl string, name string) {
	proxies := ""
	//设置代理 创建http连接
	cli := util.HttpClient(proxies)
	//获取请求结果
	data, err, code := util.HttpGET(cli, getUrl)
	if err != nil {
		fmt.Println("err:", err)
	}
	if code == 429 {
		fmt.Println("请求频繁")
	} else {
		var Info T2
		str := string(data)
		result, _ := url.QueryUnescape(str)
		err := json.Unmarshal([]byte(result), &Info)
		if err != nil {
			fmt.Println("json err :", string(data))
		}
		fmt.Println("商品:", name, "结果:", Info.Success)
	}
}

func randNum() int {
	return rand.Intn(50)
}
