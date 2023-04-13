package service

// buff 求购
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
type BuffGoods struct {
	Appid                 int         `json:"appid"`
	Bookmarked            bool        `json:"bookmarked"`
	BuyMaxPrice           string      `json:"buy_max_price"`
	BuyNum                int         `json:"buy_num"`
	CanBargain            bool        `json:"can_bargain"`
	CanSearchByTournament bool        `json:"can_search_by_tournament"`
	Description           interface{} `json:"description"`
	Game                  string      `json:"game"`
	GoodsInfo             GoodInfo    `json:"goods_info"`
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
type GoodInfo struct {
	IconUrl         string      `json:"icon_url"`
	ItemId          interface{} `json:"item_id"`
	OriginalIconUrl string      `json:"original_icon_url"`
	SteamPrice      string      `json:"steam_price"`
	SteamPriceCny   string      `json:"steam_price_cny"`
}

// buff 出售
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

// 代理
type Ip struct {
	Code    int    `json:"code"`
	Success string `json:"success"`
	Msg     string `json:"msg"`
	Data    []struct {
		IP   string `json:"IP"`
		Port int    `json:"Port"`
	} `json:"data"`
}
