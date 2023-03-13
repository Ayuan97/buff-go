package main

import (
	"buff-go/global"
	"buff-go/internal/service"
	"github.com/gin-gonic/gin"
	"time"
)

func main() {
	//test()

	gin.SetMode(global.ServerSetting.RunMode)

	//
	//清楚所有账号缓存 和本地代理缓存
	service.ClearAllAccountCache()
	//gredis.DelAll() //清除所有缓存 慎用
	service.Buff()
	service.GetSteam()
	//代理抓取
	//service.StartGetProxy()
	for {
		select {}

		//等待10秒
		time.Sleep(10 * time.Second)

	}
}
func test() {
	service.Test()
}
