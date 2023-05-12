package main

import (
	"buff-go/internal/service"
	"sync"
)

var Wg sync.WaitGroup

func main() {

	service.AutoGetSteamCookie() //自动获取cookie
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
