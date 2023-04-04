package main

import (
	"buff-go/internal/service"
)

func main() {
	test()
	//gredis.DelAll() //清除所有缓存 慎用
	//gin.SetMode(global.ServerSetting.RunMode)

	//
	//清楚所有账号缓存 和本地代理缓存
	//service.ClearAllAccountCache()
	//service.Buff()
	//service.GetSteam()
	//代理抓取
	//service.StartGetProxy()
	//for {
	//
	//	//等待10秒
	//	time.Sleep(10 * time.Second)
	//
	//}
}
func test() {
	service.GetBuffGoodInfo()
}
