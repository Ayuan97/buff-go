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

// 获取所有商品
func (d *Dao) BatchGetGoods() ([]*model.Goods, error) {
	var goods model.Goods
	return goods.Get(d.engine)
}
