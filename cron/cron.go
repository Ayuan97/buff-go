package main

import (
	"buff-go/internal/service"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"fmt"
	"github.com/robfig/cron/v3"

	"time"
)

func main() {
	//GetTest()

	//爬虫抓取
	GetProxy()
	//抓取buff和steamc的信息
	//GetList()

}

func GetList() {
	c := cron.New(cron.WithSeconds())

	//清空redis
	key1 := rediskey.GetBuffKey()
	gredis.Del(key1)
	key2 := rediskey.GetSteamSePriceKey()
	gredis.Del(key2)
	key3 := rediskey.GetSteamItemId()
	gredis.Del(key3)

	//buff-商品列表
	c.AddFunc("1 * * * * *", func() {
		key := rediskey.GetBuffKey()
		value := gredis.Get(key)

		if value == "1" {
			fmt.Println("buff列表获取 - 任务执行中")
		} else {
			fmt.Println("buff列表获取 - 开始执行任务:", time.Now())
			//
			config := service.GetSystemConfig()
			if config.BuffCookie == 1 && config.StartBuff == 1 {
				service.GetGooDsListV2()
				fmt.Println("buff列表获取 - 任务结束:", time.Now())
			} else {
				fmt.Println("buff列表获取 - 任务未开启 ", "Cookie 状态:", config.BuffCookie, "buff 价格获取状态 :", config.StartBuff)
			}
		}
	})

	//steamc出售列表
	c.AddFunc("1 * * * * *", func() {
		key := rediskey.GetSteamSePriceKey()
		value := gredis.Get(key)
		if value == "1" {
			fmt.Println("steamc出售价格获取 - 任务执行中")
		} else {
			fmt.Println("steamc出售价格获取 - 开始执行任务:", time.Now())
			//
			config := service.GetSystemConfig()
			if config.SteamCookie == 1 && config.StartSteamSell == 1 {
				service.GetSellingPrice()
				fmt.Println("steamc出售价格获取 - 任务结束:", time.Now())
			} else {
				fmt.Println("steamc出售价格获取 - 任务未开启 ", "Cookie 状态:", config.SteamCookie, "steam 价格获取状态 :", config.StartSteamSell)
			}
		}
	})

	//itemid获取
	//c.AddFunc("1 * * * * *", func() {
	//	key := rediskey.GetSteamItemId()
	//	value := gredis.Get(key)
	//	if value == "1" {
	//		fmt.Println("itemid获取 - 任务执行中")
	//	} else {
	//		fmt.Println("itemid获取 - 开始执行任务:", time.Now())
	//		service.GetItemNameId()
	//		fmt.Println("itemid获取 - 任务结束:", time.Now())
	//	}
	//})

	c.Start()
	t1 := time.NewTimer(time.Second * 1)
	for {
		select {
		case <-t1.C:
			t1.Reset(time.Second * 1)

		}
	}
}

func GetTest() {
}

func GetProxy() {
	service.StartGetProxy()
}
