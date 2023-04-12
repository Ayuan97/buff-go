package model

import "gorm.io/gorm"

type Order struct {
	*Model
	Name             string  `json:"name"`
	MarketHashName   string  `json:"market_hash_name"`
	BuffOrderId      string  `json:"buff_order_id"`
	BuffGoodsId      int     `json:"buff_goods_id"`
	BuffCrtTime      int     `json:"buff_crt_time"`
	BuffPrice        float64 `json:"buff_price"`
	BuffPayMethods   string  `json:"buff_pay_methods"`
	BuffUserId       string  `json:"buff_user_id"`
	BuffUserNickname string  `json:"buff_user_nickname"`
}

// 根据goodsid删除所有订单
func (o *Order) DeleteOrderByGoodsId(db *gorm.DB) error {
	return db.Where("buff_goods_id = ?", o.BuffGoodsId).Delete(&o).Error
}

// 批量插入订单
func (o *Order) Create(db *gorm.DB, orderList []*Order) bool {
	db.Create(&orderList)
	return true
}
