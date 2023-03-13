package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/source"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type Proxy struct {
	Success          int             `json:"success"`
	SellOrderTable   string          `json:"sell_order_table"`
	SellOrderSummary string          `json:"sell_order_summary"`
	BuyOrderTable    string          `json:"buy_order_table"`
	BuyOrderSummary  string          `json:"buy_order_summary"`
	HighestBuyOrder  string          `json:"highest_buy_order"`
	LowestSellOrder  string          `json:"lowest_sell_order"`
	BuyOrderGraph    [][]interface{} `json:"buy_order_graph"`
	SellOrderGraph   [][]interface{} `json:"sell_order_graph"`
	GraphMaxY        int             `json:"graph_max_y"`
	GraphMinX        float64         `json:"graph_min_x"`
	GraphMaxX        float64         `json:"graph_max_x"`
	PricePrefix      string          `json:"price_prefix"`
	PriceSuffix      string          `json:"price_suffix"`
}

var GoodChan = make(chan string, 16000)

// 开始
func StartGetProxy() {

	runtime.GOMAXPROCS(runtime.NumCPU())
	ipChan := make(chan *model.Ip, 2000)

	//检查库中的ip
	go func() {
		for {
			steamUser, err := myDao.GetOneSteamUser(2)
			if err != nil {
				continue
			}

			//查询数据库中的ip
			ips, err := myDao.GetAllIp()
			if err != nil {
				continue
			}
			for _, ip := range ips {
				//通过redis 检测ip是否已经在使用中
				key := rediskey.GetProxySteamKey(ip.Ip)
				if gredis.Get(key) != "" {
					//fmt.Println("ip已经在使用中", ip.Ip)
					continue
				}
				go func() {
					fmt.Println("开启协程 进行抓取", ip.Ip)
					getSteam(ip, steamUser)
				}()
				time.Sleep(5 * time.Second)
			}
		}
	}()

	//检查 chan 中的ip是否可用  可用加入数据库 不可用删除
	go func() {
		for {
			steamUser, err := myDao.GetOneSteamUser(2)
			if err != nil {
				continue
			}
			CheckProxy(<-ipChan, steamUser)
		}
	}()

	// 检查 chan 中的数据  -- 要抓取的url
	GoodsChan()

	//开启抓取  写入channel
	for {
		fmt.Println("开启抓取  写入channel")
		go run(ipChan)
		time.Sleep(2 * time.Minute)
	}

}

// 检测代理 可用则添加到数据库
func CheckProxy(ip *model.Ip, steamUser model.SteamUser) {

	if CheckIP(ip, steamUser) {
		ProxyAdd(ip)
	}
}

// 添加代理到数据库
func ProxyAdd(ip *model.Ip) {
	myDao.AddIp(ip)
}

// 抓取代理
func run(ipChan chan<- *model.Ip) {
	var wg sync.WaitGroup
	funs := []func() []*model.Ip{
		source.Hidemy,
		source.FreeProxy,
	}
	for _, f := range funs {
		wg.Add(1)
		go func(f func() []*model.Ip) {
			temp := f()
			for _, v := range temp {
				ipChan <- v
			}
			wg.Done()
		}(f)
	}
	wg.Wait()
	log.Println("所有代理抓取完成")
}

// 检测代理是否可用
func CheckIP(ip *model.Ip, steamUser model.SteamUser) bool {
	var testIP string
	port := strconv.Itoa(ip.Port)
	getUrl := <-GoodChan
	if ip.IsHttps == "1" {
		testIP = "https://" + ip.Ip + ":" + port
	} else {
		testIP = "http://" + ip.Ip + ":" + port
	}
	proxy, _ := url.Parse(testIP)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	netTransport := &http.Transport{
		Proxy:               http.ProxyURL(proxy),
		TLSClientConfig:     tlsConfig,
		MaxIdleConnsPerHost: 50,
	}
	httpClient := &http.Client{
		Timeout:   time.Second * 10,
		Transport: netTransport,
	}

	request, _ := http.NewRequest("GET", getUrl, nil)
	//设置一个header
	request.Header.Add("accept", "text/plain")
	request.AddCookie(&http.Cookie{Name: "steamCountry", Value: steamUser.SteamCountry})
	request.AddCookie(&http.Cookie{Name: "timezoneOffset", Value: "28800,0"})
	request.AddCookie(&http.Cookie{Name: "browserid", Value: steamUser.BrowserId})
	request.AddCookie(&http.Cookie{Name: "Steam_Language", Value: "schinese"})
	request.AddCookie(&http.Cookie{Name: "steamLoginSecure", Value: steamUser.SteamLoginSecure})
	request.AddCookie(&http.Cookie{Name: "sessionid", Value: steamUser.SessionId})
	resp, err := httpClient.Do(request)

	if err != nil {
		fmt.Printf("[CheckIP] testIP = %s, 代理不可用 \n", testIP)
		GoodChan <- getUrl
		return false
	}

	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		var SteamData SteamGoodsInfo
		//判读内容是否正确
		body := resp.Body
		bodyByte, _ := io.ReadAll(body)
		err := json.Unmarshal(bodyByte, &SteamData)
		if err != nil {
			fmt.Printf("[CheckIP]-2 testIP = %s, 代理不可用  抓取结果: %s \n ", testIP, SteamData)
			GoodChan <- getUrl
			return false
		}
		if SteamData.Success {
			fmt.Printf("[CheckIP]-2 testIP = %s, 代理可用 !! \n", testIP)
			go func() {
				result := handleSteamData(SteamData)
				if result {
					//修改账号状态
					myDao.UpdateSteamUserStatus(int(steamUser.ID), 2)
				}
			}()
			return true
		} else {
			fmt.Println("proxy steam err:", SteamData)
		}
	} else {
		fmt.Printf("[CheckIP]-2 testIP = %s, 代理不可用  resp.StatusCode: %d \n ", testIP, resp.StatusCode)
		GoodChan <- getUrl
		return false
	}
	return false
}

// 检查通道内的数量
func GoodsChan() {
	//判断chan中的数据是否为空
	go func() {
		for {
			if len(GoodChan) <= 20 {
				//读取所有商品 写入channel
				runtime.GOMAXPROCS(runtime.NumCPU())
				config := myDao.GetOneSystemConfig(1)
				var start = 0
				for i := 1; i <= config.SteamPageNum; i++ {
					geturl := fmt.Sprintf("https://steamcommunity.com/market/search/render/?query=&start=%v&count=100&search_descriptions=0&sort_column=price&sort_dir=desc&appid=730&norender=1&currency=23", start)
					start = start + 100
					GoodChan <- geturl
				}
			}
			time.Sleep(time.Second * 5)
		}
	}()
}

// 抓取steam信息
func getSteam(ip *model.Ip, steamUser model.SteamUser) {
	key := rediskey.GetProxySteamKey(ip.Ip)
	gredis.Set(key, 1, time.Minute*30)
	for {
		if CheckIP(ip, steamUser) {
			//如果代理可用 则继续循环

		} else {
			fmt.Println("代理不可用", ip)
			//如果代理不可用 则删除
			myDao.DeleteIp(ip)
			gredis.Del(key)
			fmt.Println("代理不可用,删除", ip)
			//停止循环 结束协程
			runtime.Goexit()
		}
	}

}
