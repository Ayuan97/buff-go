package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"encoding/json"
	"fmt"
	"github.com/gocolly/colly"
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

// 开启任务
func GetGooDsListV2() {
	key := rediskey.GetBuffKey()
	err := gredis.Set(key, "1", time.Minute*10)
	if err != nil {
		return
	}
	BuffConfig := myDao.GetOneBuffConfig(1)

	PrimitiveUrl := "https://buff.163.com/api/market/goods?"
	var urlParam UrlParam
	urlParam.PageNum = 10000
	urlParam.Game = "csgo"
	urlParam.MaxPrice = BuffConfig.MaxPrice
	urlParam.MinPrice = BuffConfig.MinPrice
	urlParam.PageSize = 80
	c := colly.NewCollector(
		//colly.Debugger(&debug.LogDebugger{}),
		colly.Async(true), //设置为异步请求
	)

	c.Limit(&colly.LimitRule{
		DomainGlob:  "*buff.163.com*",
		Delay:       time.Duration(BuffConfig.Delay) * time.Second,
		Parallelism: BuffConfig.Parallelism,
		RandomDelay: time.Duration(BuffConfig.RandomDelay) * time.Second,
	})
	c.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/105.0.0.0 Safari/537.36"

	cookie := []*http.Cookie{
		{
			Name:  "session",
			Value: BuffConfig.Sessionid,
		},
	} //设置cookie

	c.SetCookies("https://buff.163.com", cookie)
	Stop := false
	c.OnRequest(func(r *colly.Request) {
		if Stop == true {
			r.Abort()
		}
	})
	c.OnResponse(func(r *colly.Response) {
		data, _ := BindData(r.Body)
		InsertGoods(data.Data.Items, data.Data.PageNum)
		if data.Code == "Login Required" || data.Code == "Action Forbidden" {
			//结束任务
			Stop = true
		}
	})
	//发送错误
	c.OnError(func(r *colly.Response, err error) {
		fmt.Println("抓取错误:", r.StatusCode, "重试")
		r.Request.ProxyURL = ""
		r.Request.Retry()
	})
	fmt.Println("")
	for i := 1; i <= BuffConfig.PageNum; i++ {
		Url := PrimitiveUrl + "game=" + urlParam.Game +
			"&page_num=" + fmt.Sprintf("%d", i) +
			//"&max_price=" + fmt.Sprintf("%d", urlParam.MaxPrice) +
			"&min_price=" + fmt.Sprintf("%d", urlParam.MinPrice) +
			"&page_size=" + fmt.Sprintf("%d", urlParam.PageSize) +
			"&sort_by=price.desc"
		c.Visit(Url)

	}
	c.Visit(PrimitiveUrl)

	c.Wait()
	fmt.Println("抓取结束")
	gredis.Del(key)
}

// 绑定数据
func BindData(data []byte) (Response, error) {
	var GoodList Response
	str := string(data)
	result, _ := url.QueryUnescape(str)
	err := json.Unmarshal([]byte(result), &GoodList)
	if err != nil {
		fmt.Println("json err :", string(data))
		return GoodList, err
	}
	if GoodList.Code == "Login Required" {
		fmt.Println("登录超时:", GoodList.Code)
		myDao.UpdateBuffCookie(1, 0)
	}
	if GoodList.Code == "Action Forbidden" {
		fmt.Println("账号被封禁:", GoodList.Code)
		myDao.UpdateBuffCookie(1, 0)
	}
	return GoodList, err
}

// 批量插入商品信息
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
		//更新比例
		go UpdateGoodsProportion(goodsInfo.GoodsId, 1)
	}

	fmt.Println("第", page, "页抓取成功", "time:", time.Now().Format("2006-01-02 15:04:05"))
}
