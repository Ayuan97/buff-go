package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/source"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
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

func StartGetProxy() {

	runtime.GOMAXPROCS(runtime.NumCPU())
	ipChan := make(chan *model.Ip, 2000)
	// 检查库中的ip
	go func() {
		CheckProxyDB()
	}()

	// 检查 chan 中的ip
	for i := 0; i < 50; i++ {
		go func() {
			for {
				CheckProxy(<-ipChan)
			}
		}()
	}

	//开启抓取  写入channel
	for {

		n := myDao.CountIps()
		log.Printf("Chan: %v, IP: %v\n", len(ipChan), n)
		if len(ipChan) < 100 {
			go run(ipChan)
		}
		time.Sleep(10 * time.Minute)
	}

}

func CheckProxyDB() {

}

func CheckProxy(ip *model.Ip) {
	if CheckIP(ip) {
		fmt.Println("ip可用 添加到库中:", ip)
		ProxyAdd(ip)
	}
}

func ProxyAdd(ip *model.Ip) {
	//myDao.AddIp(ip)
}

func run(ipChan chan<- *model.Ip) {
	var wg sync.WaitGroup
	funs := []func() []*model.Ip{

		source.FreeProxy,
	}
	fmt.Println("funs:", funs)
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

// CheckIP is to check the ip work or not
func CheckIP(ip *model.Ip) bool {
	var pollURL string
	var testIP string
	port := strconv.Itoa(ip.Port)
	if ip.IsHttps == "1" {
		testIP = "https://" + ip.Ip + ":" + port
		pollURL = "https://steamcommunity.com/market/itemordershistogram?language=english&currency=23&item_nameid=176288647"
	} else {
		testIP = "http://" + ip.Ip + ":" + port
		pollURL = "https://steamcommunity.com/market/itemordershistogram?language=english&currency=23&item_nameid=176288647"
	}
	proxy, _ := url.Parse(testIP)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	netTransport := &http.Transport{
		Proxy:               http.ProxyURL(proxy),
		TLSClientConfig:     tlsConfig,
		MaxIdleConnsPerHost: 50,
	}
	httpClient := &http.Client{
		Timeout:   time.Second * 20,
		Transport: netTransport,
	}

	request, _ := http.NewRequest("GET", pollURL, nil)
	//设置一个header
	request.Header.Add("accept", "text/plain")

	resp, err := httpClient.Do(request)

	if err != nil {
		fmt.Printf("[CheckIP] testIP = %s, Error = %v\n", testIP, err)
		return false
	}

	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		var t Proxy
		body, _ := ioutil.ReadAll(resp.Body)
		//判读内容是否正确
		err := json.Unmarshal(body, &t)
		if err != nil {
			fmt.Printf("[CheckIP] testIP = %s, Error = %v\n", testIP, err)
			return false
		}
		if t.Success == 1 {
			return true
		}
		return true
	}
	return false
}
