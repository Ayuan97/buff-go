package service

//type Proxy struct {
//	Success          int             `json:"success"`
//	SellOrderTable   string          `json:"sell_order_table"`
//	SellOrderSummary string          `json:"sell_order_summary"`
//	BuyOrderTable    string          `json:"buy_order_table"`
//	BuyOrderSummary  string          `json:"buy_order_summary"`
//	HighestBuyOrder  string          `json:"highest_buy_order"`
//	LowestSellOrder  string          `json:"lowest_sell_order"`
//	BuyOrderGraph    [][]interface{} `json:"buy_order_graph"`
//	SellOrderGraph   [][]interface{} `json:"sell_order_graph"`
//	GraphMaxY        int             `json:"graph_max_y"`
//	GraphMinX        float64         `json:"graph_min_x"`
//	GraphMaxX        float64         `json:"graph_max_x"`
//	PricePrefix      string          `json:"price_prefix"`
//	PriceSuffix      string          `json:"price_suffix"`
//}
//
//var ProxyString string
//var GoodChan = make(chan *model.Goods, 16000)
//
//// 代理池
//
//func StartGetProxy() {
//
//	runtime.GOMAXPROCS(runtime.NumCPU())
//	ipChan := make(chan *model.Ip, 2000)
//	// 检查库中的ip
//	go func() {
//		for {
//			fmt.Println("检查库中的ip")
//			CheckProxyDB()
//			time.Sleep(time.Minute * 5)
//		}
//	}()
//
//	// 检查 chan 中的ip
//	for i := 0; i < 50; i++ {
//		go func() {
//			for {
//				CheckProxy(<-ipChan)
//			}
//		}()
//	}
//
//	//开启抓取  写入channel
//	for {
//		fmt.Println("开启抓取  写入channel")
//		go run(ipChan)
//		time.Sleep(3 * time.Minute)
//	}
//
//}
//
//func CheckProxyDB() {
//	ips, err := myDao.GetAllIp()
//	if err != nil {
//		return
//	}
//	var wg sync.WaitGroup
//	for _, v := range ips {
//		wg.Add(1)
//		go func(v *model.Ip) {
//			if !CheckIP(v) {
//				myDao.DeleteIp(v)
//			}
//			wg.Done()
//		}(v)
//	}
//	wg.Wait()
//
//}
//
//func CheckProxy(ip *model.Ip) {
//	if CheckIP(ip) {
//		ProxyAdd(ip)
//	}
//}
//
//func ProxyAdd(ip *model.Ip) {
//	myDao.AddIp(ip)
//}
//
//func run(ipChan chan<- *model.Ip) {
//	var wg sync.WaitGroup
//	funs := []func() []*model.Ip{
//		source.Hidemy,
//		source.FreeProxy,
//	}
//	for _, f := range funs {
//		wg.Add(1)
//		go func(f func() []*model.Ip) {
//			temp := f()
//			for _, v := range temp {
//				ipChan <- v
//			}
//			wg.Done()
//		}(f)
//	}
//	wg.Wait()
//	log.Println("所有代理抓取完成")
//}
//
//func CheckIP(ip *model.Ip) bool {
//	var pollURL string
//	var testIP string
//	port := strconv.Itoa(ip.Port)
//	info := <-GoodChan
//	if ip.IsHttps == "1" {
//		testIP = "https://" + ip.Ip + ":" + port
//		pollURL = "https://steamcommunity.com/market/itemordershistogram?language=english&currency=23&item_nameid=" + info.SteamItemNameId
//	} else {
//		testIP = "http://" + ip.Ip + ":" + port
//		pollURL = "https://steamcommunity.com/market/itemordershistogram?language=english&currency=23&item_nameid=" + info.SteamItemNameId
//	}
//	proxy, _ := url.Parse(testIP)
//
//	tlsConfig := &tls.Config{InsecureSkipVerify: true}
//
//	netTransport := &http.Transport{
//		Proxy:               http.ProxyURL(proxy),
//		TLSClientConfig:     tlsConfig,
//		MaxIdleConnsPerHost: 50,
//	}
//	httpClient := &http.Client{
//		Timeout:   time.Second * 20,
//		Transport: netTransport,
//	}
//
//	request, _ := http.NewRequest("GET", pollURL, nil)
//	//设置一个header
//	request.Header.Add("accept", "text/plain")
//
//	resp, err := httpClient.Do(request)
//
//	if err != nil {
//		//fmt.Printf("[CheckIP] testIP = %s, 代理不可用 \n", testIP)
//		GoodChan <- info
//		return false
//	}
//
//	defer resp.Body.Close()
//	if resp.StatusCode == 200 {
//		var t Proxy
//		//判读内容是否正确
//		body := resp.Body
//		bodyByte, _ := io.ReadAll(body)
//		err := json.Unmarshal(bodyByte, &t)
//		if err != nil {
//			//fmt.Printf("[CheckIP] testIP = %s, 代理不可用 \n", testIP)
//			GoodChan <- info
//			return false
//		}
//		if t.Success == 1 {
//			fmt.Printf("[CheckIP] testIP = %s, good good! 代理可用   name:%s \n", testIP, info.Name)
//			//string转float64
//			f, _ := strconv.ParseFloat(t.HighestBuyOrder, 64)
//			d, _ := strconv.ParseFloat(t.LowestSellOrder, 64)
//			//更新求购价格
//			myDao.UpdateGoodsBuyPrice(info, f/100, d/100)
//			return true
//		}
//		//fmt.Printf("[CheckIP] testIP = %s, 代理不可用 \n", testIP)
//		GoodChan <- info
//		return false
//	} else {
//		//fmt.Printf("[CheckIP] testIP = %s, 代理不可用 \n", testIP)
//		GoodChan <- info
//		return false
//	}
//}
//
//// 通过代理获取steam求购价格
//
//func GoodsChan() {
//	//判断chan中的数据是否为空
//	go func() {
//		for {
//			if len(GoodChan) <= 50 {
//				//读取所有商品 写入channel
//				runtime.GOMAXPROCS(runtime.NumCPU())
//				result, _ := myDao.GetAll()
//				for _, v := range result {
//					GoodChan <- v
//				}
//			}
//			time.Sleep(time.Second * 5)
//		}
//	}()
//}
//
//// 获取steam求购价格
//func GetSteamBuyPrice() {
//
//	//开启协程 2
//	for i := 0; i < 1; i++ {
//		go func() {
//			for {
//				select {
//				case info := <-GoodChan:
//					//查询代理数量
//					proxyCount := myDao.CountIps()
//					if proxyCount > 0 {
//						//获取steam价格
//						time.Sleep(time.Second * 1)
//						go func() {
//							//fmt.Println("开始获取steam求购价格", info.Name)
//							value := GetSteamPrice(info)
//							if !value {
//								//重新进入队列
//								//fmt.Println("获取steam求购价格失败 重新进入队列", info.Name)
//								GoodChan <- info
//							} else {
//								fmt.Println("获取steam求购价格成功", info.Name)
//							}
//						}()
//					} else {
//						fmt.Println("代理数量不足 等待抓取代理 目前代理数量:", proxyCount)
//						time.Sleep(time.Second * 60)
//					}
//
//				}
//			}
//		}()
//	}
//
//}
//
//func GetSteamPrice(info *model.Goods) bool {
//	if ProxyString == "" {
//		value, _ := myDao.GetIps()
//		if value.IsHttps == "1" {
//			ProxyString = "https://" + value.Ip + ":" + strconv.Itoa(value.Port)
//		} else {
//			ProxyString = "http://" + value.Ip + ":" + strconv.Itoa(value.Port)
//		}
//	}
//	pollURL := "https://steamcommunity.com/market/itemordershistogram?language=english&currency=23&item_nameid=" + info.SteamItemNameId
//
//	proxy, _ := url.Parse(ProxyString)
//
//	tlsConfig := &tls.Config{InsecureSkipVerify: true}
//
//	netTransport := &http.Transport{
//		Proxy:               http.ProxyURL(proxy),
//		TLSClientConfig:     tlsConfig,
//		MaxIdleConnsPerHost: 50,
//	}
//	httpClient := &http.Client{
//		Timeout:   time.Second * 20,
//		Transport: netTransport,
//	}
//
//	request, _ := http.NewRequest("GET", pollURL, nil)
//	//设置一个header
//	request.Header.Add("accept", "text/plain")
//
//	resp, err := httpClient.Do(request)
//
//	if err != nil {
//		//fmt.Println("获取求购价格 发出请求失败", err)
//		ChangeProxy()
//		return false
//	}
//
//	defer resp.Body.Close()
//	if resp.StatusCode == 200 {
//		var t Proxy
//		//判读内容是否正确
//		body := resp.Body
//		bodyByte, _ := io.ReadAll(body)
//		err := json.Unmarshal(bodyByte, &t)
//		if err != nil {
//			//fmt.Println("获取求购价格 发出请求失败", err)
//			ChangeProxy()
//			return false
//		}
//		if t.Success == 1 {
//			//string转float64
//			f, _ := strconv.ParseFloat(t.HighestBuyOrder, 64)
//			d, _ := strconv.ParseFloat(t.LowestSellOrder, 64)
//			//更新求购价格
//			myDao.UpdateGoodsBuyPrice(info, f/100, d/100)
//			return true
//		}
//		ChangeProxy()
//		return false
//	} else {
//		ChangeProxy()
//		return false
//	}
//}
//
//// 切换代理
//func ChangeProxy() {
//	//代理失效 切换代理
//	value, _ := myDao.GetRandomIps()
//	if value.IsHttps == "1" {
//		ProxyString = "https://" + value.Ip + ":" + strconv.Itoa(value.Port)
//	} else {
//		ProxyString = "http://" + value.Ip + ":" + strconv.Itoa(value.Port)
//	}
//	//fmt.Println("代理失效 切换代理", ProxyString)
//}
