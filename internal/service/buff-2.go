package service

import (
	"buff-go/internal/model"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"time"
)

type Response struct {
	Code string `json:"code"`
	Data Data   `json:"data"`
	Msg  string `json:"msg"`
}

type Data struct {
	FopStr             string                 `json:"fop_str"`
	GoodsInfos         map[string]GoodsInfo   `json:"goods_infos"`
	HasMarketStores    map[string]bool        `json:"has_market_stores"`
	Items              []Item                 `json:"items"`
	PageNum            int                    `json:"page_num"`
	PageSize           int                    `json:"page_size"`
	PreviewScreenshots map[string]interface{} `json:"preview_screenshots"`
	ShowGameCmsIcon    bool                   `json:"show_game_cms_icon"`
	ShowPayMethodIcon  bool                   `json:"show_pay_method_icon"`
	SortBy             string                 `json:"sort_by"`
	TotalCount         int                    `json:"total_count"`
	TotalPage          int                    `json:"total_page"`
	UserInfo           map[string]UserInfo    `json:"user_info"`
}
type UserInfo struct {
	Avatar       string      `json:"avatar"`
	AvatarSafe   string      `json:"avatar_safe"`
	IsAutoAccept bool        `json:"is_auto_accept"`
	IsPremiumVip bool        `json:"is_premium_vip"`
	Nickname     string      `json:"nickname"`
	SellerLevel  int         `json:"seller_level"`
	ShopId       string      `json:"shop_id"`
	UserId       string      `json:"user_id"`
	VTypes       interface{} `json:"v_types"`
}

type GoodsInfo struct {
	AppID           int         `json:"appid"`
	Can3DInspect    bool        `json:"can_3d_inspect"`
	CanInspect      bool        `json:"can_inspect"`
	Description     interface{} `json:"description"`
	Game            string      `json:"game"`
	GoodsID         int         `json:"goods_id"`
	IconURL         string      `json:"icon_url"`
	ItemID          interface{} `json:"item_id"`
	MarketHashName  string      `json:"market_hash_name"`
	MarketMinPrice  string      `json:"market_min_price"`
	Name            string      `json:"name"`
	OriginalIconURL string      `json:"original_icon_url"`
	ShortName       string      `json:"short_name"`
	SteamPrice      string      `json:"steam_price"`
	SteamPriceCNY   string      `json:"steam_price_cny"`
}

type Item struct {
	AllowBargain          bool        `json:"allow_bargain"`
	AppID                 int         `json:"appid"`
	AssetInfo             AssetInfo   `json:"asset_info"`
	BackgroundImageURL    string      `json:"background_image_url"`
	Bookmarked            bool        `json:"bookmarked"`
	CanBargain            bool        `json:"can_bargain"`
	CanUseInspectTrnURL   bool        `json:"can_use_inspect_trn_url"`
	CannotBargainReason   string      `json:"cannot_bargain_reason"`
	CreatedAt             int         `json:"created_at"`
	Fee                   string      `json:"fee"`
	Game                  string      `json:"game"`
	GoodsID               int         `json:"goods_id"`
	Id                    string      `json:"id"`
	ImgSrc                string      `json:"img_src"`
	LowestBargainPrice    string      `json:"lowest_bargain_price"`
	Mode                  int         `json:"mode"`
	Price                 string      `json:"price"`
	RecentAverageDuration interface{} `json:"recent_average_duration"`
	RecentDeliverRate     interface{} `json:"recent_deliver_rate"`
	State                 int         `json:"state"`
	SupportedPayMethods   []int       `json:"supported_pay_methods"`
	TradableCooldown      interface{} `json:"tradable_cooldown"`
	UserId                string      `json:"user_id"`
}
type AssetInfo struct {
	ActionLink           string      `json:"action_link"`
	Appid                int         `json:"appid"`
	Assetid              string      `json:"assetid"`
	Classid              string      `json:"classid"`
	Contextid            int         `json:"contextid"`
	GoodsId              int         `json:"goods_id"`
	HasTradableCooldown  bool        `json:"has_tradable_cooldown"`
	Instanceid           string      `json:"instanceid"`
	Paintwear            string      `json:"paintwear"`
	TradableCooldownText string      `json:"tradable_cooldown_text"`
	TradableUnfrozenTime interface{} `json:"tradable_unfrozen_time"`
}

func GetBuffGoodInfo() {
	//获取所有商品
	goodsList, _ := myDao.GetAll()
	for _, goods := range goodsList {
		//获取buff商品信息
		getBuffGoodInfo(goods)
		time.Sleep(5 * time.Second)
	}
}
func getBuffGoodInfo(goods *model.Goods) {
	url := fmt.Sprintf("https://buff.163.com/api/market/goods/sell_order?game=csgo&goods_id=%v&page_num=1&sort_by=default&mode=&allow_tradable_cooldown=1&use_suggestion=0&_=%v", goods.GoodsId, time.Now().UnixNano()/1e6)
	fmt.Println(url)
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Println("buff err1:", err)
		return
	}
	req.AddCookie(&http.Cookie{Name: "Device-Id", Value: "nSt86DRnNpIcAVkzG5QC"})
	req.AddCookie(&http.Cookie{Name: "client_id", Value: "u591PbZEqJi47BnljKgbaA"})
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("buff err2:", err)
		return
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("buff err3:", err)
		return
	}
	buffData := Response{}
	err = json.Unmarshal(body, &buffData)
	if err != nil {
		fmt.Println("buff err4:", err)
		return
	}
	if buffData.Code == "OK" {
		//获取buff商品信息成功
		//更新buff商品信息
		fmt.Println("ok", buffData.Data.GoodsInfos[strconv.Itoa(goods.GoodsId)].Name)
	} else {
		fmt.Println("buff err5:", err)
	}
}
