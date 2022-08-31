package service

import (
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"fmt"
)

type Pool struct {
	Ip     string
	Port   int
	Https  int
	Status int
}

// 获取代理
func GetProxy() {
	key := rediskey.GetProxyMap(1)
	result := gredis.Srandmember(key)
	fmt.Println("result:", result)
}

// 代理失效 移出代理池 30s后再试
func FailProxy() {

}
