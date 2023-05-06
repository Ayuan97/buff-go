package main

import (
	"buff-go/internal/service"
	"fmt"
	"io/ioutil"
	"net/http"
	"runtime"
	"sync"
	"time"
)

var Wg sync.WaitGroup

func main() {
	//service.AutoGetSteamCookie() //自动获取cookie

	service.ClearAllAccountCache() //清楚所有账号缓存 和本地代理缓存

	//gredis.DelAll() //清除所有缓存 慎用

	service.GetBuffSell() //buff  出售
	service.GetSteamBuy() // steam 求购

	service.GetSteamSell() // steam 出售
	service.GetBuffBuy()   //buff  求购

	//service.StartGetProxy()	//代理抓取

	//service.GetSteamItemId() //steam 商品id抓取
	Wg.Add(1)
	go func() {
		for {
			//统计协程的熟练
			num := runtime.NumGoroutine()
			fmt.Println("当前协程数量：", num)
			time.Sleep(time.Second * 5)
		}

	}()
	time.Sleep(time.Second * 10)
	Wg.Wait()
}

func test() {
	//curl 请求
	geturl := fmt.Sprintf("https://steamcommunity.com/market/search/render/?query=&start=%v&count=10&search_descriptions=0&sort_column=price&sort_dir=desc&appid=730&norender=1&currency=23", 100)
	client := &http.Client{}

	req, err := http.NewRequest("GET", geturl, nil)
	if err != nil {
		fmt.Println("steam err: 发起请求失败", err)
		return
	}
	req.AddCookie(&http.Cookie{Name: "steamCountry", Value: "HK%7C8ad7d7ea3737e06297549f92142430ad"})
	req.AddCookie(&http.Cookie{Name: "timezoneOffset", Value: "28800,0"})
	req.AddCookie(&http.Cookie{Name: "browserid", Value: "3038284548861100670"})
	req.AddCookie(&http.Cookie{Name: "Steam_Language", Value: "schinese"})
	req.AddCookie(&http.Cookie{Name: "steamLoginSecure", Value: "76561199029489705||eyAidHlwIjogIkpXVCIsICJhbGciOiAiRWREU0EiIH0.eyAiaXNzIjogInI6MEQxRl8yMjdCNDBEMF83QkZEQSIsICJzdWIiOiAiNzY1NjExOTkwMjk0ODk3MDUiLCAiYXVkIjogWyAid2ViIiBdLCAiZXhwIjogMTY4MzM2MDE1MywgIm5iZiI6IDE2NzQ2MzI2MTksICJpYXQiOiAxNjgzMjcyNjE5LCAianRpIjogIjBEMjBfMjI3QjQwRDBfOEU3NTEiLCAib2F0IjogMTY4MzI3MjYxOCwgInJ0X2V4cCI6IDE3MDE1MDI5MDgsICJwZXIiOiAwLCAiaXBfc3ViamVjdCI6ICIxMDMuMjIwLjc5LjExMCIsICJpcF9jb25maXJtZXIiOiAiMTAzLjIyMC43NC4xOTUiIH0.PuxB6xVWQFkccs5LPZTP06L5Pi7ZD_EUU1q4jRpNXhPGfdYAKkOSpDS5mMM1cf8BAxg-UEu4Ng4b9OaeXgv4BQ"})
	req.AddCookie(&http.Cookie{Name: "sessionid", Value: "zug9x323xgpd9vvkq081t4g8"})
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("steam err2:", err)
		return
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("steam err3:", err)
		return
	}
	fmt.Println(string(body))
}
