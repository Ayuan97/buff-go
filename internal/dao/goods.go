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
