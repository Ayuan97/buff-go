package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"fmt"
	"strconv"
	"time"
)

// 处理buff出售信息
func BuffBuyInfo(buffData Response, info *model.Info) {
	buyNum := len(buffData.Data.HasMarketStores)
	//获取buff商品信息成功
	//更新缓存
	goodInfo := buffData.Data.GoodsInfos[strconv.Itoa(info.GoodsId)]
	InfoKey := rediskey.GetCacheKey(goodInfo.MarketHashName)
	minPrice := 99999999.99
	var orderList []*model.Order
	for _, v := range buffData.Data.Items {
		//查找最低价格
		if util.StringToFloat64(v.Price) < minPrice {
			minPrice = util.StringToFloat64(v.Price)
		}
		//删除上一次的订单
		err := myDao.DeleteOrderByGoodsId(v.GoodsID)
		if err != nil {
			fmt.Println("buff - 删除订单失败")
			continue
		}
		var order model.Order
		order.Name = goodInfo.Name
		order.MarketHashName = goodInfo.MarketHashName
		order.BuffOrderId = v.Id
		order.BuffGoodsId = v.GoodsID
		order.BuffCrtTime = v.CreatedAt
		order.BuffPrice = util.StringToFloat64(v.Price)
		payMethod := ""
		for _, pay := range v.SupportedPayMethods {
			payMethod += strconv.Itoa(pay) + ","
		}
		order.BuffPayMethods = payMethod
		order.BuffUserId = v.UserId
		order.BuffUserNickname = buffData.Data.UserInfo[v.UserId].Nickname
		orderList = append(orderList, &order)
	}
	//批量插入订单
	err := myDao.BatchCreateOrder(orderList)
	if err != true {
		fmt.Println("buff - 批量插入订单失败")
	}
	//更新缓存
	gredis.Hset(InfoKey, "buff_sell_num", buyNum)
	gredis.Hset(InfoKey, "buff_sell_price", minPrice)
	//更新数据库
	var Info model.Info
	Info.GoodsId = info.GoodsId
	Info.Name = goodInfo.Name
	Info.MarketHashName = goodInfo.MarketHashName

	Info.BuffSellPrice = minPrice //buff 出售价格
	Info.BuffSellNum = buyNum     //buff 出售数量
	Info.GoodsId = info.GoodsId
	Info.BuffSellUpdate = int(time.Now().Unix())
	myDao.UpdateInfoByGoodsId(&Info)
	fmt.Println("buff - buy - name:", Info.Name, "buyNum:", buyNum, "minPrice:", minPrice)

}
