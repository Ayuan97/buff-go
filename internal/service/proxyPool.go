package service

import (
	"buff-go/internal/model"
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

func Test() {
	source.Hidemy()
}

func StartGetProxy() {

	runtime.GOMAXPROCS(runtime.NumCPU())
	ipChan := make(chan *model.Ip, 2000)
	// 检查库中的ip
	go func() {
		for {
			fmt.Println("检查库中的ip")
			CheckProxyDB()
			time.Sleep(time.Minute * 5)
		}
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
		fmt.Println("开启抓取  写入channel")
		go run(ipChan)
		time.Sleep(3 * time.Minute)
	}

}

func CheckProxyDB() {
	ips, err := myDao.GetAllIp()
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, v := range ips {
		wg.Add(1)
		go func(v *model.Ip) {
			if !CheckIP(v) {
				myDao.DeleteIp(v)
			}
			wg.Done()
		}(v)
	}
	wg.Wait()

}

func CheckProxy(ip *model.Ip) {
	if CheckIP(ip) {
		ProxyAdd(ip)
	}
}

func ProxyAdd(ip *model.Ip) {
	myDao.AddIp(ip)
}

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

// CheckIP is to check the ip work or not
func CheckIP(ip *model.Ip) bool {
	var pollURL string
	var testIP string
	port := strconv.Itoa(ip.Port)
	info := <-GoodChan
	if ip.IsHttps == "1" {
		testIP = "https://" + ip.Ip + ":" + port
		pollURL = "https://steamcommunity.com/market/itemordershistogram?language=english&currency=23&item_nameid=" + info.SteamItemNameId
	} else {
		testIP = "http://" + ip.Ip + ":" + port
		pollURL = "https://steamcommunity.com/market/itemordershistogram?language=english&currency=23&item_nameid=" + info.SteamItemNameId
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
		//fmt.Printf("[CheckIP] testIP = %s, 代理不可用 \n", testIP)
		GoodChan <- info
		return false
	}

	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		var t Proxy
		//判读内容是否正确
		body := resp.Body
		bodyByte, _ := io.ReadAll(body)
		err := json.Unmarshal(bodyByte, &t)
		if err != nil {
			//fmt.Printf("[CheckIP] testIP = %s, 代理不可用 \n", testIP)
			GoodChan <- info
			return false
		}
		if t.Success == 1 {
			fmt.Printf("[CheckIP] testIP = %s, good good! 代理可用   name:%s \n", testIP, info.Name)
			//string转float64
			f, _ := strconv.ParseFloat(t.HighestBuyOrder, 64)
			d, _ := strconv.ParseFloat(t.LowestSellOrder, 64)
			//更新求购价格
			myDao.UpdateGoodsBuyPrice(info, f/100, d/100)
			return true
		}
		//fmt.Printf("[CheckIP] testIP = %s, 代理不可用 \n", testIP)
		GoodChan <- info
		return false
	} else {
		//fmt.Printf("[CheckIP] testIP = %s, 代理不可用 \n", testIP)
		GoodChan <- info
		return false
	}
}
