package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/util"
	"encoding/json"
	"fmt"
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

func GetGoods(start int, end int) {

	//循环50次
	for i := start; i <= end; i++ {
		//等待50毫秒
		time.Sleep(20 * time.Millisecond)
		game := "csgo"
		pageNum := i
		pageSize := 80
		url := "https://buff.163.com/api/market/goods?game=" + game + "&page_num=" + util.IntToString(pageNum) + "&page_size=" + util.IntToString(pageSize)
		isSuccess := getRequest(url)
		if !isSuccess {
			break
		}
	}
}
func getRequest(url string) bool {
	data := util.StartRequestProxy(url)
	var response Response
	errJson := json.Unmarshal([]byte(data), &response)
	if errJson != nil {
		fmt.Println("重试:", url)
		getRequest(url)
	}
	if response.Code == "OK" {
		go InsertGoods(response.Data.Items)
	} else if response.Code == "Login Required" {
		return false
	}
	return true
}

//func GetGoods()  {
//	client := &http.Client{}
//	game := "csgo"
//	pageNum := 1
//	pageSize := 100
//	url := "https://buff.163.com/api/market/goods?game="+game+"&page_num="+util.IntToString(pageNum)+"&page_size="+util.IntToString(pageSize)
//	req, err := http.NewRequest("GET", "https://buff.163.com/api/market/goods?game=csgo&page_num=1", nil)
//	if err != nil {
//		log.Fatal(err)
//	}
//	//设置cookie 并写出cookie
//	cookie := http.Cookie{Name: "session", Value: "1-XMnMS_ZKw8eQBTopkCSEuQ3sq8bVAeIy9OmW4iZuoWmL2034674671"}
//	req.AddCookie(&cookie)
//	resp, err := client.Do(req)
//	if err != nil {
//		log.Fatal(err)
//	}
//	bodyText, err := ioutil.ReadAll(resp.Body)
//	if err != nil {
//		log.Fatal(err)
//	}
//	var response Response
//	errJson := json.Unmarshal(bodyText, &response)
//	if err != nil {
//		fmt.Println("json.Unmarshal failed:", errJson)
//	}
//	//fmt.Println(response)
//	if response.Code == "OK"{
//		//判断总页数
//		if response.Data.TotalPage > 1 {
//			for i := 1; i <= response.Data.TotalPage; i++ {
//				req, err := http.NewRequest("GET", "https://buff.163.com/api/market/goods?game=csgo&page_num="+strconv.Itoa(i)+"&use_suggestion=1&trigger=undefined_trigger", nil)
//				if err != nil {
//					log.Fatal(err)
//				}
//				//设置cookie 并写出cookie
//				cookie := http.Cookie{Name: "session", Value: "1-XMnMS_ZKw8eQBTopkCSEuQ3sq8bVAeIy9OmW4iZuoWmL2034674671"}
//				req.AddCookie(&cookie)
//				resp, err := client.Do(req)
//				if err != nil {
//					log.Fatal(err)
//				}
//				bodyText, err := ioutil.ReadAll(resp.Body)
//				if err != nil {
//					log.Fatal(err)
//				}
//				fmt.Println("第", i, "页","code:",response.Code,"msg:",response.Msg,"error:",response.Error,"extra:",response.Extra)
//				var response Response
//				errJson := json.Unmarshal(bodyText, &response)
//				if err != nil {
//					fmt.Println("json.Unmarshal failed:", errJson)
//				}
//				if response.Code == "OK"{
//					go	InsertGoods(response.Data.Items)
//				}
//				//等待100毫秒
//				time.Sleep(1500 * time.Millisecond)
//			}
//		}else{
//			fmt.Println(response.Data.Items)
//		}
//	}else{
//		fmt.Println("获取失败 err:", response.Error)
//	}
//}

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
