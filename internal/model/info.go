package model

import (
	"buff-go/global"
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
	Game            string  `json:"game"`
	Name            string  `json:"name"`
	MarketHashName  string  `json:"market_hash_name"`
	GoodsId         int     `gorm:"unique_index" json:"goods_id"`
	IconUrl         string  `json:"icon_url"`
	SteamItemNameId string  `json:"steam_item_name_id"`
	IsPush          int     `json:"is_push"`
	IsBuy           int     `json:"is_buy"`
	BuffProportion  float64 `json:"buff_proportion"`
	SteamProportion float64 `json:"steam_proportion"`
	BuffBuyUpdate   int     `json:"buff_buy_update"`
	BuffSellUpdate  int     `json:"buff_sell_update"`
	SteamBuyUpdate  int     `json:"steam_buy_update"`
	SteamSellUpdate int     `json:"steam_sell_update"`
}

//根据goods_id 查询单个商品信息
func (g *Info) GetInfoByGoodsId(db *gorm.DB, goodsId int) (*Info, error) {
	var info Info
	err := db.Where("goods_id = ?", goodsId).First(&info).Error
	if err != nil {
		//global.Logger.Errorf("GetInfoByGoodsId err: %v", err)
		return nil, err
	}
	return &info, nil
}

// 查询 单个MarketHashName
func (g *Info) GetInfoByMarketHashName(db *gorm.DB, marketHashName string) (*Info, error) {
	var info Info
	err := db.Where("market_hash_name = ?", marketHashName).First(&info).Error
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// 插入商品信息
func (g *Info) Create(db *gorm.DB, info *Info) bool {
	err := db.Create(&info).Error
	if err != nil {
		global.Logger.Errorf("Create err: %v", err)
		return false
	}
	return true
}

// 批量更新商品信息 - buff
func (g *Info) BatchBuffUpdate(db *gorm.DB, info []*Info) bool {
	db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "market_hash_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"buff_buy_price", "buff_buy_num", "goods_id", "buff_buy_update", "icon_url"}),
	}).Create(&info)
	return true
}
func (g *Info) BatchSteamUpdate(db *gorm.DB, info []*Info) bool {
	db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "market_hash_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"steam_sell_price", "steam_sell_num", "steam_sell_update"}),
	}).Create(&info)
	return true
}

// 获取所有商品信息
func (g *Info) GetAll(db *gorm.DB) ([]*Info, error) {
	var info []*Info
	err := db.Where("goods_id != 0").Select("goods_id,buff_buy_price").Find(&info).Error
	if err != nil {
		global.Logger.Errorf("GetAll err: %v", err)
		return nil, err
	}
	return info, nil
}

// 根据goodsid更新商品信息
func (g *Info) UpdateInfoByGoodsId(db *gorm.DB, info *Info) error {
	err := db.Model(&Info{}).Where("goods_id = ?", info.GoodsId).Updates(info).Error
	if err != nil {
		global.Logger.Errorf("UpdateInfoByGoodsId err: %v", err)
		return err
	}
	return nil
}

// 根据id更新 steam_item_name_id
func (g *Info) UpdateInfoBySteamItemId(db *gorm.DB, info *Info) error {
	err := db.Model(&Info{}).Where("id = ?", info.ID).Updates(info).Error
	if err != nil {
		global.Logger.Errorf("UpdateInfoBySteamItemId err: %v", err)
		return err
	}
	return nil
}

// 获取所有商品信息 item_name_id 为空的
func (g *Info) GetAllBySteamItemId(db *gorm.DB) ([]*Info, error) {
	var info []*Info
	err := db.Where("steam_item_name_id != ''").Find(&info).Error
	if err != nil {
		global.Logger.Errorf("GetAllBySteamItemId err: %v", err)
		return nil, err
	}
	return info, nil
}

// 根据id更新商品信息
func (g *Info) UpdateInfo(db *gorm.DB, info *Info) error {
	err := db.Model(&Info{}).Where("id = ?", info.ID).Updates(info).Error
	if err != nil {
		global.Logger.Errorf("UpdateInfoById err: %v", err)
		return err
	}
	return nil
}

// 更新比例
func (g *Info) UpdateBuffProportion(db *gorm.DB, info *Info) error {
	err := db.Model(&Info{}).Where("market_hash_name = ?", info.MarketHashName).Updates(info).Error
	if err != nil {
		global.Logger.Errorf("UpdateBuffProportion err: %v", err)
		return err
	}
	return nil
}

// 更新比例-steam
func (g *Info) UpdateSteamProportion(db *gorm.DB, info *Info) error {
	err := db.Model(&Info{}).Where("market_hash_name = ?", info.MarketHashName).Updates(info).Error
	if err != nil {
		global.Logger.Errorf("UpdateBuffProportion err: %v", err)
		return err
	}
	return nil
}

//根据 market_hash_name 更新商品信息
func (g *Info) UpdateInfoByMarketHashName(db *gorm.DB, info *Info) error {
	err := db.Model(&Info{}).Where("market_hash_name = ?", info.MarketHashName).Updates(info).Error
	if err != nil {
		global.Logger.Errorf("UpdateInfoByMarketHashName err: %v", err)
		return err
	}
	return nil
}
