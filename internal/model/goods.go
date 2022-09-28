package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Goods struct {
	*Model
	Appid              int     `json:"appid"`
	BuyMaxPrice        float64 `json:"buy_max_price"`
	BuyNum             int     `json:"buy_num"`
	Game               string  `json:"game"`
	Name               string  `json:"name"`
	MarketHashName     string  `json:"market_hash_name"`
	ShortName          string  `json:"short_name"`
	GoodsId            int     `gorm:"unique_index" json:"goods_id"`
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

func (g *Goods) Create(db *gorm.DB, goods []*Goods) bool {
	db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "goods_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"buy_max_price", "buy_num", "steam_price", "steam_price_cny", "quick_price"}),
	}).Create(&goods)

	return true
}

func (g *Goods) Get(db *gorm.DB) ([]*Goods, error) {
	var goods []*Goods
	err := db.Order("id desc").Find(&goods).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	return goods, nil
}
