package model

import (
	"fmt"
	"gorm.io/gorm"
)

type Goods struct {
	*Model
	Appid              int     `json:"appid"`
	BuyMaxPrice        float64 `json:"buy_max_price"`
	BuyNum             int     `json:"buy_num"`
	Game               string  `json:"game"`
	GoodsId            int     `json:"goods_id"`
	IconUrl            string  `json:"icon_url"`
	SteamPrice         float64 `json:"steam_price"`
	SteamPriceCny      float64 `json:"steam_price_cny"`
	QuickPrice         float64 `json:"quick_price"`
	SellMinPrice       float64 `json:"sell_min_price"`
	SellNum            int     `json:"sell_num"`
	SellReferencePrice float64 `json:"sell_reference_price"`
	SteamMarketUrl     string  `json:"steam_market_url"`
	TransactedNum      int     `json:"transacted_num"`
}

func (g *Goods) Create(db *gorm.DB, goods []*Goods) (bool, error) {
	err := db.Create(&goods).Error
	if err != nil {
		fmt.Println("create goods failed:", err)
	}
	return true, err
}
