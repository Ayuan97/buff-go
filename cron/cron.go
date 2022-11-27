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
	//service.GetGooDsListV2()
	//service.GetSellingPrice()
	//service.GetTest()

	//GetTest()
	GetList()

}

func GetList() {
	c := cron.New(cron.WithSeconds())

	//buff-商品列表
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

	//steamc出售列表
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

	//itemid获取
	c.AddFunc("1 * * * * *", func() {
		key := rediskey.GetSteamItemId()
		value := gredis.Get(key)
		if value == "1" {
			fmt.Println("itemid获取 - 任务执行中")
		} else {
			fmt.Println("itemid获取 - 开始执行任务:", time.Now())
			service.GetItemNameId()
			fmt.Println("itemid获取 - 任务结束:", time.Now())
		}
	})

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
	//service.LoginSteam()
}
