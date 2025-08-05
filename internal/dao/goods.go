package dao

import (
	"buff-go/internal/model"
)

// 获取所有商品
func (d *Dao) GetAll() ([]*model.Goods, error) {
	var goods model.Goods
	return goods.GetAll(d.engine)
}

func (d *Dao) BatchCreateGoods(goodsList []*model.Goods) bool {
	var goods model.Goods
	return goods.Create(d.engine, goodsList)
}

// 创建单个商品 存在即更新
func (d *Dao) CreateGoods(info *model.Goods) bool {
	var goods model.Goods
	goods.GoodsId = info.GoodsId
	return goods.CreateOn(d.engine, info)
}

// 获取所有商品 itemid != ”

// 获取单个商品信息
func (d *Dao) GetGoodsByGoodsId(goodsId int64) (*model.Goods, error) {
	var goods model.Goods
	return goods.GetOne(d.engine, goodsId)
}

// 根据商品id更新商品比例
func (d *Dao) UpdateGoodsRatioByGoodsId(goodsId int, Ratio float64) error {
	var goods model.Goods
	return goods.UpdateRatioByGoodsId(d.engine, goodsId, Ratio)
}
