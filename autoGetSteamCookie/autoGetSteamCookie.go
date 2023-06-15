package main

import (
	"buff-go/internal/service"
	"sync"
)

var Wg sync.WaitGroup

func main() {

	service.AutoGetSteamCookie() //自动获取cookie
	Wg.Add(1)
	Wg.Wait()
}
