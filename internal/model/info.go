package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Info struct {
	*Model
	Appid           int     `json:"appid"`
	BuffBuyPrice    float64 `json:"buff_buy_price"`
	BuffBuyNum      int     `json:"buff_buy_num"`
	BuffSellPrice   float64 `json:"buff_sell_price"`
	BuffSellNum     int     `json:"buff_sell_num"`
	SteamBuyPrice   float64 `json:"steam_buy_price"`
	SteamBuyNum     int     `json:"steam_buy_num"`
	SteamSellPrice  float64 `json:"steam_sell_price"`
	SteamSellNum    int     `json:"steam_sell_num"`
	SteamMarketUrl  string  `json:"steam_market_url"`
	Game            string  `json:"game"`
	Name            string  `json:"name"`
	MarketHashName  string  `json:"market_hash_name"`
	GoodsId         int     `gorm:"unique_index" json:"goods_id"`
	IconUrl         string  `json:"icon_url"`
	SteamItemNameId string  `json:"steam_item_name_id"`
	IsPush          int     `json:"is_push"`
	Proportion      float64 `json:"proportion"`
	SteamUpdate     int64   `json:"steam_update"`
	BuffUpdate      int64   `json:"buff_update"`
}

//查询 单个MarketHashName
func (g *Info) GetInfoByMarketHashName(db *gorm.DB, marketHashName string) (*Info, error) {
	var info Info
	err := db.Where("market_hash_name = ?", marketHashName).First(&info).Error
	if err != nil {
		return nil, err
	}
	return &info, nil
}

//插入商品信息
func (g *Info) Create(db *gorm.DB, info *Info) bool {
	db.Create(&info)
	return true
}

//批量更新商品信息
func (g *Info) BatchBuffUpdate(db *gorm.DB, info []*Info) bool {
	db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "market_hash_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"buff_buy_price", "buff_buy_num", "buff_sell_price", "buff_sell_num"}),
	}).Create(&info)
	return true
}
