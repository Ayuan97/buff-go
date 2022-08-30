package dao

import (
	"buff-go/internal/model"
)

type JuhePhoneCaptchaRsp struct {
	ErrorCode int    `json:"error_code"`
	Reason    string `json:"reason"`
}

// 创建用户
func (d *Dao) BatchCreateGoods(goodsList []*model.Goods) (bool, error) {
	var goods model.Goods
	return goods.Create(d.engine, goodsList)
}
