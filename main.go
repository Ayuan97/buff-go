package main

import (
	"fmt"
	"github.com/fatih/color"
	"github.com/gin-gonic/gin"
	"net/http"
	"paopao-ce/global"
	"paopao-ce/internal/routers"
	"paopao-ce/pkg/util"
)

var (
	version, buildDate, commitID string
)

func main() {
	gin.SetMode(global.ServerSetting.RunMode)

	router := routers.NewRouter()
	s := &http.Server{
		Addr:           global.ServerSetting.HttpIp + ":" + global.ServerSetting.HttpPort,
		Handler:        router,
		ReadTimeout:    global.ServerSetting.ReadTimeout,
		WriteTimeout:   global.ServerSetting.WriteTimeout,
		MaxHeaderBytes: 1 << 20,
	}

	util.PrintHelloBanner(fmt.Sprintf("paopao %s (build:%s %s)", version, commitID, buildDate))
	fmt.Fprintf(color.Output, "美女充电冲冲冲 service listen on %s\n",
		color.GreenString(fmt.Sprintf("http://%s:%s", global.ServerSetting.HttpIp, global.ServerSetting.HttpPort)),
	)
	s.ListenAndServe()
}
