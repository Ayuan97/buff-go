package service

type BuyData struct {
	Game                  string  `json:"game"`
	GoodsId               int     `json:"goods_id"`
	SellOrderId           string  `json:"sell_order_id"`           //订单id
	Price                 float64 `json:"price"`                   //价格
	PayMethod             int     `json:"pay_method"`              //支付方式
	AllowTradableCooldown int     `json:"allow_tradable_cooldown"` //是否允许交易冷却
	Token                 string  `json:"token"`                   //token
	CdkeyId               string  `json:"cdkey_id"`
}

// 购买
func Buy(BuyType string, BuyData BuyData) {
	if BuyType == "buff" {
		BuffBuy(BuyData)
	}
}
func BuffBuy(data BuyData) {

}
