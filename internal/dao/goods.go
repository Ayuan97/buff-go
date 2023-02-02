package dao

import (
	"buff-go/internal/model"
)

type JuhePhoneCaptchaRsp struct {
	ErrorCode int    `json:"error_code"`
	Reason    string `json:"reason"`
}

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
func (d *Dao) BatchGetGoods() ([]*model.Goods, error) {
	var goods model.Goods
	return goods.Get(d.engine)
}
func (d *Dao) BatchGetGoodsItemId() ([]*model.Goods, error) {
	var goods model.Goods
	return goods.GetItemId(d.engine)
}

// 更新单个商品
func (d *Dao) UpdateItemId(Id int64, itemId string) error {
	var goods model.Goods
	return goods.UpdateItemId(d.engine, Id, itemId)
}

// 获取单个商品信息
func (d *Dao) GetGoodsByGoodsId(goodsId int64) (*model.Goods, error) {
	var goods model.Goods
	return goods.GetOne(d.engine, goodsId)
}

// 更新比例
func (d *Dao) UpdateGoodsRatio(goods *model.Goods, Ratio float64) error {
	return goods.UpdateRatio(d.engine, Ratio)
}

// 更新求购价格
func (d *Dao) UpdateGoodsBuyPrice(goods *model.Goods, HighestBuyOrder float64, LowestSellOrder float64) error {
	return goods.UpdateBuyPrice(d.engine, HighestBuyOrder, LowestSellOrder)
}

// 根据商品id更新商品比例
func (d *Dao) UpdateGoodsRatioByGoodsId(goodsId int, Ratio float64) error {
	var goods model.Goods
	return goods.UpdateRatioByGoodsId(d.engine, goodsId, Ratio)
}
