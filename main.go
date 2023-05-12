package main

import (
	"buff-go/internal/service"
	"sync"
)

var Wg sync.WaitGroup

func main() {

	service.AutoGetSteamCookie()   //自动获取cookie
	service.ClearAllAccountCache() //清楚所有账号缓存 和本地代理缓存
	Wg.Add(1)
	//go func() {
	//	for {
	//
	//		num := runtime.NumGoroutine()
	//		fmt.Println("当前协程数量：", num)
	//		time.Sleep(time.Minute * 5)
	//	}
	//
	//}()
	//time.Sleep(time.Second * 10)
	Wg.Wait()
}
