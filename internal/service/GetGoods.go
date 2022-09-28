package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"encoding/json"
	"fmt"
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

func GetGooDsListV2() {
	key := rediskey.GetBuffKey()
	err := gredis.Set(key, "1", time.Minute*5)
	if err != nil {
		return
	}
	PrimitiveUrl := "https://buff.163.com/api/market/goods?"
	var urlParam UrlParam
	urlParam.PageNum = 10000
	urlParam.Game = "csgo"
	urlParam.MaxPrice = 5000
	urlParam.MinPrice = 2
	urlParam.PageSize = 80

	for i := 1; i <= 187; i++ {
		num := randNum()
		num = 1000 + num
		time.Sleep(time.Millisecond * time.Duration(num))
		Url := PrimitiveUrl + "game=" + urlParam.Game +
			"&page_num=" + fmt.Sprintf("%d", i) +
			"&max_price=" + fmt.Sprintf("%d", urlParam.MaxPrice) +
			"&min_price=" + fmt.Sprintf("%d", urlParam.MinPrice) +
			"&page_size=" + fmt.Sprintf("%d", urlParam.PageSize) +
			"&sort_by=price.desc"
		go GetGoodsV2(Url, i)
	}
	gredis.Del(key)
}

//获取商品
func GetGoodsV2(getUrl string, page int) {
	proxies := ""
	//设置代理 创建http连接
	cli := util.NewHttpClient(proxies)
	//获取请求结果
	data, err, code := util.HttpGET(cli, getUrl)
	if code == 429 {
		//重新获取内容
		fmt.Println("抓取失败:", page, "err:", err, "code:", code)
	} else {
		result, err := BindData(data, err, code)
		if err != nil {
			fmt.Println("抓取失败:", page, "err:", err, "code:", code)
		}
		if len(result.Data.Items) > 0 {
			InsertGoods(result.Data.Items, page)
		}
	}
}

func BindData(data []byte, httpError error, httpCode int) (Response, error) {
	var GoodList Response
	str := string(data)
	result, _ := url.QueryUnescape(str)
	err := json.Unmarshal([]byte(result), &GoodList)
	if err != nil {
		fmt.Println("json err :", string(data), " http error:", httpError, " http code : ", httpCode)
		return GoodList, err
	}
	if GoodList.Code == "Login Required" {
		fmt.Println("登录超时:", GoodList.Code)
	}
	return GoodList, err
}

//批量插入商品信息
func InsertGoods(data []T, page int) {
	//批量插入数据库
	for _, v := range data {
		var goodsInfo model.Goods
		goodsInfo.Appid = v.Appid
		goodsInfo.BuyMaxPrice = util.StringToFloat64(v.BuyMaxPrice)
		goodsInfo.BuyNum = v.BuyNum
		goodsInfo.Game = v.Game
		goodsInfo.Name = v.Name
		goodsInfo.MarketHashName = v.MarketHashName
		goodsInfo.ShortName = v.ShortName
		goodsInfo.GoodsId = v.Id
		goodsInfo.IconUrl = v.GoodsInfo.IconUrl
		goodsInfo.SteamPrice = util.StringToFloat64(v.GoodsInfo.SteamPrice)
		goodsInfo.SteamPriceCny = util.StringToFloat64(v.GoodsInfo.SteamPriceCny)
		goodsInfo.QuickPrice = util.StringToFloat64(v.QuickPrice)
		goodsInfo.SellMinPrice = util.StringToFloat64(v.SellMinPrice)
		goodsInfo.SellNum = v.SellNum
		goodsInfo.SellReferencePrice = util.StringToFloat64(v.SellReferencePrice)
		goodsInfo.SteamMarketUrl = v.SteamMarketUrl
		myDao.CreateGoods(&goodsInfo)
	}

	fmt.Println("第", page, "页抓取成功")
}
