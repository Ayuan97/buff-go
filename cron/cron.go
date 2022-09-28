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
	var err error
	c := cron.New(cron.WithSeconds())
	//添加2秒钟定时任务 处理
	_, err = c.AddFunc("1 * * * * *", func() {
		key := rediskey.GetBuffKey()
		value := gredis.Get(key)
		if value == "1" {
			fmt.Println("任务执行中")
		} else {
			fmt.Println("开始执行任务:", time.Now())
			service.GetGooDsListV2()
			fmt.Println("任务结束:", time.Now())
		}

	})
	if err != nil {
		fmt.Println("AddFun:", err)
		return
	}

	c.Start()
	t1 := time.NewTimer(time.Second * 10)
	for {
		select {
		case <-t1.C:
			t1.Reset(time.Second * 10)

		}
	}
}
