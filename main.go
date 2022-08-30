package main

import (
	"buff-go/global"
	"buff-go/internal/routers"
	"buff-go/pkg/util"
	"fmt"
	"github.com/fatih/color"
	"github.com/gin-gonic/gin"
	"net/http"
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

	util.PrintHelloBanner(fmt.Sprintf("buff-go %s (build:%s %s)", version, commitID, buildDate))
	fmt.Fprintf(color.Output, "小趴菜冲啊 service listen on %s\n",
		color.GreenString(fmt.Sprintf("http://%s:%s", global.ServerSetting.HttpIp, global.ServerSetting.HttpPort)),
	)
	s.ListenAndServe()
}
