package main

import (
	"buff-go/global"
	"buff-go/internal/service"
	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(global.ServerSetting.RunMode)
	//buff
	service.Buff()
	for {
		select {}
	}
}
