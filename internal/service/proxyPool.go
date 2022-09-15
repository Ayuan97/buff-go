package service

import (
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

type Pool struct {
	Ip     string
	Port   int
	Https  int
	Status int
}

type Proxy struct {
	Msg  string `json:"msg"`
	Code int    `json:"code"`
	Data struct {
		Count          int      `json:"count"`
		DedupCount     int      `json:"dedup_count"`
		OrderLeftCount int      `json:"order_left_count"`
		ProxyList      []string `json:"proxy_list"`
	} `json:"data"`
}

// 获取代理
func GetProxy() string {

	//return  GetProxyInfo()
	key := rediskey.GetProxyMap(1)
	result := gredis.Srandmember(key)
	return result.Val()
}

// 代理失效 移出代理池 n s后再试
func FailProxy(ipAddr string) int {
	key := rediskey.GetProxyMap(1)
	result := gredis.Srem(key, ipAddr)
	if result > 0 {
		go AddProxy(ipAddr, 10)
		return 1
	}
	return 0
}

func AddProxy(ipAddr string, number int64) {
	fmt.Printf("等待 %d s 重新加入代理池\n", number)
	time.Sleep(time.Duration(number) * time.Second)
	key := rediskey.GetProxyMap(1)
	gredis.SAdd(key, ipAddr)

}

//获取代理信息
func GetProxyInfo() string {
	client := &http.Client{}
	var url string
	url = "https://dps.kdlapi.com/api/getdps/?orderid=955975957452070&num=1&signature=swzue8lxt5l3etjr45h17de7drek0axn&pt=1&sep=1"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	bodyText, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	return "http://" + string(bodyText)
	//fmt.Println(string(bodyText))
	//解析到结构体
	//var proxy Proxy
	//err = json.Unmarshal(bodyText, &proxy)
	//if err != nil {
	//	log.Fatal(err)
	//}
	//fmt.Println(proxy.Data.ProxyList[0])
	//return "https://" + proxy.Data.ProxyList[0]
}
