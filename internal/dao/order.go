package dao

import "buff-go/internal/model"

// 根据goodsid删除所有订单
func (d *Dao) DeleteOrderByGoodsId(goodsId int) error {
	var order model.Order
	order.BuffGoodsId = goodsId
	return order.DeleteOrderByGoodsId(d.engine)
}

// 批量插入订单
func (d *Dao) BatchCreateOrder(orderList []*model.Order) bool {
	var order model.Order
	return order.Create(d.engine, orderList)
}
