package main

import (
	"buff-go/internal/service"
	"fmt"
	"runtime"
	"sync"
	"time"
)

var Wg sync.WaitGroup

func main() {

	service.AutoGetSteamCookie()   //自动获取cookie
	service.ClearAllAccountCache() //清楚所有账号缓存 和本地代理缓存
	service.GetBuffSell()          //buff  出售
	service.GetSteamBuy()          // steam 求购
	service.GetSteamSell()         // steam 出售
	service.GetBuffBuy()           //buff  求购

	Wg.Add(1)
	go func() {
		for {

			num := runtime.NumGoroutine()
			fmt.Println("当前协程数量：", num)
			time.Sleep(time.Second * 5)
		}

	}()
	time.Sleep(time.Second * 10)
	Wg.Wait()
}
