package main

import (
	"buff-go/internal/service"
	"time"
)

func main() {
	//gredis.DelAll() //清除所有缓存 慎用

	//清楚所有账号缓存 和本地代理缓存
	service.ClearAllAccountCache()

	//buff
	service.GetBuffSell()
	//service.GetBuffBuy()
	//steam
	service.GetSteamBuy()

	//代理抓取
	//service.StartGetProxy()
	for {
		//等待10秒
		time.Sleep(10 * time.Second)

	}
}
