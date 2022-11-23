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
	c := cron.New(cron.WithSeconds())
	//service.GetGooDsListV2()
	//service.GetSellingPrice()
	//service.GetTest()

	//添加2秒钟定时任务 处理
	c.AddFunc("1 * * * * *", func() {
		key := rediskey.GetBuffKey()
		value := gredis.Get(key)
		if value == "1" {
			fmt.Println("buff列表获取 - 任务执行中")
		} else {
			fmt.Println("buff列表获取 - 开始执行任务:", time.Now())
			service.GetGooDsListV2()
			fmt.Println("buff列表获取 - 任务结束:", time.Now())
		}
	})

	//添加2秒钟定时任务 处理
	c.AddFunc("1 * * * * *", func() {
		key := rediskey.GetSteamSePriceKey()
		value := gredis.Get(key)
		if value == "1" {
			fmt.Println("steamc出售价格获取 - 任务执行中")
		} else {
			fmt.Println("steamc出售价格获取 - 开始执行任务:", time.Now())
			service.GetSellingPrice()
			fmt.Println("steamc出售价格获取 - 任务结束:", time.Now())
		}
	})

	//c.AddFunc("1 * * * * *", func() {
	//	itemIdKey := rediskey.GetSteamItemId()
	//	itemValue := gredis.Get(itemIdKey)
	//	if itemValue == "1" {
	//		fmt.Println("itemid 获取 -任务执行中")
	//	} else {
	//		fmt.Println("itemid 获取 -开始执行任务:", time.Now())
	//		service.GetInfo()
	//		fmt.Println("itemid 获取 -任务结束:", time.Now())
	//	}
	//})

	c.Start()
	t1 := time.NewTimer(time.Second * 10)
	for {
		select {
		case <-t1.C:
			t1.Reset(time.Second * 10)

		}
	}
}
