package dao

import (
	"buff-go/internal/model"
)

type JuhePhoneCaptchaRsp struct {
	ErrorCode int    `json:"error_code"`
	Reason    string `json:"reason"`
}

func (d *Dao) BatchCreateGoods(goodsList []*model.Goods) bool {
	var goods model.Goods
	return goods.Create(d.engine, goodsList)
}

//创建单个商品 存在即更新
func (d *Dao) CreateGoods(info *model.Goods) bool {
	var goods model.Goods
	goods.GoodsId = info.GoodsId
	return goods.CreateOn(d.engine, info)
}

// 获取所有商品
func (d *Dao) BatchGetGoods() ([]*model.Goods, error) {
	var goods model.Goods
	return goods.Get(d.engine)
}

// 更新单个商品
func (d *Dao) UpdateItemId(Id int64, itemId string) error {
	var goods model.Goods
	return goods.UpdateItemId(d.engine, Id, itemId)
}
