package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/util"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"
)

type Response struct {
	Code  string `json:"code"`
	Data  Data   `json:"data"`
	Msg   string `json:"msg"`
	Error string `json:"error"`
	Extra string `json:"extra"`
}

type Data struct {
	Items      []T `json:"items"`
	PageNum    int `json:"page_num"`
	PageSize   int `json:"page_size"`
	TotalCount int `json:"total_count"`
	TotalPage  int `json:"total_page"`
}
type GoodsInfo struct {
	IconUrl         string      `json:"icon_url"`
	ItemId          interface{} `json:"item_id"`
	OriginalIconUrl string      `json:"original_icon_url"`
	SteamPrice      string      `json:"steam_price"`
	SteamPriceCny   string      `json:"steam_price_cny"`
}
type T struct {
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

type UrlParam struct {
	Path     string
	Game     string
	PageNum  int
	PageSize int
	MinPrice int
	MaxPrice int
}

var proxy string

func GetGooDsList() {

	PrimitiveUrl := "https://buff.163.com/api/market/goods?"
	var urlParam UrlParam
	urlParam.PageNum = 10000
	urlParam.Game = "csgo"
	urlParam.MaxPrice = 4000
	urlParam.MinPrice = 1
	urlParam.PageSize = 80
	postUrl := PrimitiveUrl + "game=" + urlParam.Game +
		"&page_num=" + fmt.Sprintf("%d", urlParam.PageNum) +
		"&max_price=" + fmt.Sprintf("%d", urlParam.MaxPrice) +
		"&min_price=" + fmt.Sprintf("%d", urlParam.MinPrice) +
		"&page_size=" + fmt.Sprintf("%d", urlParam.PageSize)
	totalNum := GetTotalPageNum(postUrl)
	var pageNum float64
	pageNum = float64(int(float64(totalNum)))
	//计算循环次数
	forNum := int(math.Ceil(pageNum / 10))
	for i := 1; i <= forNum; i++ {
		time.Sleep(time.Millisecond * 500)
		go func(i int) {
			for l := 1; l <= 10; l++ {
				var CureePageNum int
				if i == 1 {
					CureePageNum = l
				} else {
					CureePageNum = (i-1)*10 + l
				}
				if CureePageNum > totalNum {
					break
				}
				var Url string
				Url = PrimitiveUrl + "game=" + urlParam.Game +
					"&page_num=" + fmt.Sprintf("%d", CureePageNum) +
					"&max_price=" + fmt.Sprintf("%d", urlParam.MaxPrice) +
					"&min_price=" + fmt.Sprintf("%d", urlParam.MinPrice) +
					"&page_size=" + fmt.Sprintf("%d", urlParam.PageSize)
				fmt.Println(Url)
				GetGoods(Url)
			}
		}(i)
	}
}

//获取商品
func GetGoods(getUrl string) {
	isProxy := gredis.Get("proxy-" + proxy)
	if isProxy == "0" {
		fmt.Println("proxy 不可用  切换代理")
		proxy = GetProxy()
	}
	gredis.Set(getUrl, proxy, time.Minute*1)
	cli := util.NewHttpClient(proxy)
	cli.Do(&http.Request{
		Method: "GET",
		Header: http.Header{
			"User-Agent": []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3770.100 Safari/537.36"},
		},
	})

	data, err, code := util.HttpGET(cli, getUrl)
	if code == 429 || err != nil {
		val := gredis.Get(getUrl)
		gredis.Set("proxy-"+val, 0, time.Minute*2)
		GetGoods(getUrl)
		time.Sleep(time.Millisecond * 1000)
		fmt.Println("proxy:", proxy)
	}

	result, err := BindData(data)
	if err != nil {
		time.Sleep(time.Millisecond * 500)
		GetGoods(getUrl)
	}
	if len(result.Data.Items) > 0 {
		InsertGoods(result.Data.Items)
	}
}

//获取页码
func GetTotalPageNum(getUrl string) int {
	url := ""
	cli := util.NewHttpClient(url)
	cli.Do(&http.Request{
		Method: "GET",
		Header: http.Header{
			"User-Agent": []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3770.100 Safari/537.36"},
		},
	})

	data, err, code := util.HttpGET(cli, getUrl)
	if code == 429 {
		time.Sleep(time.Millisecond * 1000)
		proxy = GetProxy()
		GetGoods(getUrl)
	}
	if err != nil || len(data) == 0 {
		fmt.Println("err:", err, "data:", data)
		//等待500毫秒后重新请求
		time.Sleep(time.Millisecond * 500)
		//获取代理
		proxy = GetProxy()
		GetGoods(getUrl)
	}
	result, _ := BindData(data)
	return result.Data.TotalPage
}

func BindData(data []byte) (Response, error) {
	var GoodList Response
	str := string(data)
	result, _ := url.QueryUnescape(str)
	err := json.Unmarshal([]byte(result), &GoodList)
	if err != nil {
		fmt.Println("json err :", result)
		return GoodList, err
	}
	return GoodList, err
}

//批量插入商品信息
func InsertGoods(data []T) {
	//批量插入数据库
	var goods []*model.Goods
	for _, v := range data {
		var goodsInfo model.Goods
		goodsInfo.Appid = v.Appid
		goodsInfo.BuyMaxPrice = util.StringToFloat64(v.BuyMaxPrice)
		goodsInfo.BuyNum = v.BuyNum
		goodsInfo.Game = v.Game
		goodsInfo.GoodsId = v.Id
		goodsInfo.IconUrl = v.GoodsInfo.IconUrl
		goodsInfo.SteamPrice = util.StringToFloat64(v.GoodsInfo.SteamPrice)
		goodsInfo.SteamPriceCny = util.StringToFloat64(v.GoodsInfo.SteamPriceCny)
		goodsInfo.QuickPrice = util.StringToFloat64(v.QuickPrice)
		goodsInfo.SellMinPrice = util.StringToFloat64(v.SellMinPrice)
		goodsInfo.SellNum = v.SellNum
		goodsInfo.SellReferencePrice = util.StringToFloat64(v.SellReferencePrice)
		goodsInfo.SteamMarketUrl = v.SteamMarketUrl
		goods = append(goods, &goodsInfo)
	}
	myDao.BatchCreateGoods(goods)
	fmt.Println("插入成功")
}
