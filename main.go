package main

import (
	"buff-go/global"
	"buff-go/internal/service"
	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(global.ServerSetting.RunMode)
	//清楚所有账号缓存 和本地代理缓存
	service.ClearAllAccountCache()
	//gredis.DelAll() //清除所有缓存 慎用
	service.Buff()
	service.Steam()
	for {
		select {}
	}
}
