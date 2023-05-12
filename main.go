package main

import (
	"buff-go/internal/service"
	"fmt"
	"sync"
)

var Wg sync.WaitGroup

func main() {
	data, err := service.LoginSteam()
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(data)

	//service.AutoGetSteamCookie() //自动获取cookie

	//service.ClearAllAccountCache() //清楚所有账号缓存 和本地代理缓存

	//gredis.DelAll() //清除所有缓存 慎用

	//service.GetBuffSell() //buff  出售
	//service.GetSteamBuy() // steam 求购

	//service.GetSteamSell() // steam 出售
	//service.GetBuffBuy()   //buff  求购

	//service.StartGetProxy()	//代理抓取

	//service.GetSteamItemId() //steam 商品id抓取
	//Wg.Add(1)
	//go func() {
	//	for {
	//
	//		num := runtime.NumGoroutine()
	//		fmt.Println("当前协程数量：", num)
	//		time.Sleep(time.Second * 5)
	//	}
	//
	//}()
	//time.Sleep(time.Second * 10)
	//Wg.Wait()
}
