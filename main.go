package main

import (
	"buff-go/internal/service"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type data struct {
	Key       string `json:"key"`
	CheckType int    `json:"type"`
	Value     string `json:"value"`
}

func processQueue(queueName string, wg *sync.WaitGroup) {
	defer wg.Done()
	//根据不同的队列名称 启动不同的处理
	switch queueName {
	case rediskey.CheckPrice:
		//循环处理队列
		for {
			str, err := gredis.RPop(queueName)
			if str == "" || err != nil {
				time.Sleep(time.Second * 5)
				continue
			}
			var d data
			d = data{}
			err = json.Unmarshal([]byte(str), &d)
			if err != nil {
				fmt.Println("checkPrice -1", d.Key, d.CheckType, err)
				continue
			}
			v, _ := base64.StdEncoding.DecodeString(d.Value)
			var value map[string]string
			err = json.Unmarshal(v, &value)
			if err != nil {
				fmt.Println("checkPrice -2", d.Key, d.CheckType, err)
				continue
			}
			fmt.Println("name", value["name"], "type", d.CheckType)
			service.CheckPriceChange(d.Key, d.CheckType, value)
		}
	case rediskey.AutoBuySteam():
		//自动购买
		for {
			str, err := gredis.RPop(queueName)
			if str == "" || err != nil {
				time.Sleep(time.Second * 5)
				continue
			}
		}
	}

}

func main() {
	var wg sync.WaitGroup
	wg.Add(1)

	// 定义最大协程数量
	checkPrice := 3

	//价格更新队列
	for i := 0; i < checkPrice; i++ {
		wg.Add(1)
		go processQueue(rediskey.CheckPriceList(), &wg)
	}

	//autobuysteam := 1
	////自动购买队列
	//for i := 0; i < autobuysteam; i++ {
	//	wg.Add(1)
	//	go processQueue(rediskey.AutoBuySteam(), &wg)
	//}

	// 等待所有协程完成
	wg.Wait()

}
