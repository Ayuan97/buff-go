package main

import (
	"buff-go/internal/service"
	"time"
)

func main() {
	//gredis.DelAll() //清除所有缓存 慎用

	service.ClearAllAccountCache() //清楚所有账号缓存 和本地代理缓存

	service.GetBuffSell()  //buff  出售
	service.GetBuffBuy()   //buff  求购
	service.GetSteamBuy()  // steam 求购
	service.GetSteamSell() // steam 出售

	//service.StartGetProxy()	//代理抓取

	//service.GetSteamItemId() //steam 商品id抓取

	for {
		//等待10秒
		time.Sleep(10 * time.Second)
	}
}
