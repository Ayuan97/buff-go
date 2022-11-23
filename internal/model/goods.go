package model

import (
	"buff-go/global"
	"fmt"
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
	SteamItemNameId    string  `json:"steam_item_name_id"`
	SteamSellPrice     float64 `json:"steam_sell_price"`
	Proportion         float64
}

func (g *Goods) Create(db *gorm.DB, goods []*Goods) bool {
	db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "goods_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"buy_max_price", "buy_num", "steam_price", "steam_price_cny", "quick_price"}),
	}).Create(&goods)

	return true
}

func (g *Goods) CreateOn(db *gorm.DB, goods *Goods) bool {
	var goodsCount int64
	err2 := db.Model(g).Where("goods_id = ?", goods.GoodsId).Count(&goodsCount).Error
	if err2 != nil {
		global.Logger.Errorf("CreateOn error: %v", err2)
		return false
	}
	if goodsCount > 0 {
		//更新
		err := db.Model(g).Where("goods_id = ?", goods.GoodsId).Updates(Goods{
			BuyMaxPrice:        goods.BuyMaxPrice,
			BuyNum:             goods.BuyNum,
			SteamPrice:         goods.SteamPrice,
			SteamPriceCny:      goods.SteamPriceCny,
			QuickPrice:         goods.QuickPrice,
			SellMinPrice:       goods.SellMinPrice,
			SellNum:            goods.SellNum,
			SellReferencePrice: goods.SellReferencePrice,
			SteamMarketUrl:     goods.SteamMarketUrl,
			TransactedNum:      goods.TransactedNum,
		}).Error
		if err != nil {
			fmt.Println("更新错误", err)
		}
	} else {
		err := db.Model(g).Create(goods).Error
		if err != nil {
			fmt.Println("新增错误", err)
			return false
		}
		return true
	}

	return false
}

func (g *Goods) Get(db *gorm.DB) ([]*Goods, error) {
	var goods []*Goods
	err := db.Where("steam_item_name_id = '' ").Order("id desc").Find(&goods).Limit(200).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	return goods, nil
}
func (g *Goods) GetItemId(db *gorm.DB) ([]*Goods, error) {
	var goods []*Goods
	err := db.Where("steam_item_name_id != '' ").Order("id desc").Find(&goods).Limit(200).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	return goods, nil
}

func (g *Goods) UpdateItemId(db *gorm.DB, id int64, ItemId string) error {
	//更新
	err := db.Model(g).Where("id = ?", id).Updates(Goods{
		SteamItemNameId: ItemId,
	}).Error
	if err != nil {
		fmt.Println("UpdateItemId err :", err)
		return err
	}
	return nil
}

// GetBySteamItemNameId 通过商品名称获取商品信息ID
func (g *Goods) GetBySteamItemNameId(db *gorm.DB, name string) (*Goods, error) {
	var goods Goods
	err := db.Where("market_hash_name = ?", name).First(&goods).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	return &goods, nil
}

// 更新steam出售价格
func (g *Goods) UpdateSteamSellPrice(db *gorm.DB) error {
	//更新
	err := db.Model(g).Where("id = ?", g.ID).Updates(Goods{
		SteamSellPrice: g.SteamSellPrice,
	}).Error
	if err != nil {
		fmt.Println("UpdateSteamSellPrice err :", err)
	}
	return err
}

// 获取单个商品信息
func (g *Goods) GetOne(db *gorm.DB, goodsId int64) (*Goods, error) {
	var goods Goods
	err := db.Where("goods_id = ?", goodsId).First(&goods).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	return &goods, nil
}

// 更新商品比例
func (g *Goods) UpdateRatio(db *gorm.DB, Ratio float64) error {
	//更新
	err := db.Model(g).Where("id = ?", g.ID).Updates(Goods{
		Proportion: Ratio,
	}).Error
	if err != nil {
		fmt.Println("UpdateRatio err :", err)
	}
	return err
}
