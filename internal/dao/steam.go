package dao

import (
	"buff-go/internal/model"
)

// 通过商品名称获取商品信息ID
func (d *Dao) GetGoodsBySteamItemNameId(name string) (*model.Goods, error) {
	var goods model.Goods
	goods.MarketHashName = name
	return goods.GetBySteamItemNameId(d.engine, name)
}

// 更新商品价格
func (d *Dao) UpdateGoodsPrice(goods *model.Goods, price int) error {
	//分转元保留两位小数
	goods.SteamSellPrice = float64(price) / 100
	return goods.UpdateSteamSellPrice(d.engine)
}
