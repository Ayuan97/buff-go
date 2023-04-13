package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"time"
)

var I = make(chan *model.Info, 16000)

var proxyBuyChan = make(chan string, 100)
var proxySellChan = make(chan string, 100)

// buff 求购
func GetBuffSell() {
	//每5秒扫描一次 查询是否有可用代理 和 可用账号 如果有则启动一个协程
	go func() {
		for {
			//查询buff抓取是否开启
			config := myDao.GetOneSystemConfig(1)
			if config.StartBuff == 0 {
				fmt.Println("buff抓取未开启")
				time.Sleep(10 * time.Second)
				continue
			}
			//查询本地代理和账号是否可用 可用则启动一个本地协程
			//查询本地代理是否可用
			buffLocalKey := rediskey.GetBuffLocalKey()
			buffLocalResult := gredis.Get(buffLocalKey)
			//查询账号是否可用
			buffUser, err := myDao.GetOneBuffUser()
			if err != nil {
				time.Sleep(10 * time.Second)
				continue
			}
			buffAccountKey := rediskey.GetBuffAccountKey(int(buffUser.ID))
			buffAccountResult := gredis.Get(buffAccountKey)
			if buffLocalResult == "" && buffAccountResult == "" {
				//开启本地代理
				go GetBuffSellData(0, "", buffUser)
				time.Sleep(2 * time.Second)
			} else {
				fmt.Println("buff本地代理正在抓取中...")
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

// buff 求购数据
func GetBuffSellData(isProxy int, proxy string, account model.BuffUser) {
	//设置本地代理正在抓取中
	buffLocalKey := rediskey.GetBuffLocalKey()
	gredis.Set(buffLocalKey, "1", 0)
	//设置账号正在抓取中
	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
	gredis.Set(buffAccountKey, "1", 0)
	//修改状态
	myDao.UpdateBuffUserStatus(int(account.ID), 1)
	for {
		fmt.Println("buff start:", time.Now().Format("2006-01-02 15:04:05"))
		//循环N次 获取buff数据
		config := myDao.GetOneSystemConfig(1) //系统配置
		for i := 1; i <= config.BuffPageNum; i++ {
			geturl := fmt.Sprintf("https://buff.163.com/api/market/goods/buying?game=csgo&page_num=%v&min_price=%v&max_price=%v&sort_by=price.desc&page_size=80&use_suggestion=0&_=%v", i, config.MinPrice, config.MaxPrice, time.Now().UnixNano()/1e6)
			client := &http.Client{}
			//isProxy 设置代理
			if isProxy == 1 {
				proxyURL, _ := url.Parse(proxy)
				client.Transport = &http.Transport{
					Proxy: http.ProxyURL(proxyURL),
				}
			}
			req, err := http.NewRequest("GET", geturl, nil)
			if err != nil {
				fmt.Println("buff err: 发起请求失败", err)
				//结束协程
				fmt.Println("结束协程")
				//设置本地代理抓取结束
				gredis.Del(buffLocalKey)
				//设置账号抓取结束
				gredis.Del(buffAccountKey)
				myDao.UpdateBuffUserStatus(int(account.ID), 0)
				runtime.Goexit()
				return
			}
			req.AddCookie(&http.Cookie{Name: "Device-Id", Value: account.DeviceId})
			req.AddCookie(&http.Cookie{Name: "Locale-Supported", Value: "zh-Hans"})
			req.AddCookie(&http.Cookie{Name: "csrf_token", Value: account.CsrfToken})
			req.AddCookie(&http.Cookie{Name: "game", Value: "csgo"})
			req.AddCookie(&http.Cookie{Name: "remember_me", Value: account.RememberMe})
			req.AddCookie(&http.Cookie{Name: "session", Value: account.Sessionid})
			resp, err := client.Do(req)
			if err != nil {
				fmt.Println("buff err2:", err)
				endTask2(account, 0, 1)
				return
			}
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("buff err3:", err)
				//结束协程
				endTask(resp, account, 0, 1)
				return
			}
			var buffData BuffData
			err = json.Unmarshal(body, &buffData)
			if err != nil {
				fmt.Println("buff err4:", err)
				endTask(resp, account, 0, 1)
				return
			}
			if buffData.Code == "Action Forbidden" {
				fmt.Println("buff err6:", buffData.Code)
				endTask(resp, account, 3, 1)
			}
			if buffData.Code == "Login Required" {
				fmt.Println("buff err7:", buffData)
				endTask(resp, account, 2, 1)
			}
			if buffData.Code == "OK" {
				go handleBuffData(buffData)
			} else {
				fmt.Println("buff err6:", buffData)
				//结束协程
				fmt.Println("结束协程")
				endTask(resp, account, 0, 1)
			}
			config = myDao.GetOneSystemConfig(1) //系统配置
			if config.StartBuff == 0 {
				endTask(resp, account, 0, 1)
				return
			}
			//每次请求间隔
			delay := time.Duration(config.BuffDelay)
			time.Sleep(time.Second * delay)
		}
	}

}

// 处理buff求购数据
func handleBuffData(buffData BuffData) {
	//批量更新buff数据 不存在的话就插入
	//查询缓存是否存在该商品
	var InfoList []*model.Info
	for _, v := range buffData.Result.Items {
		fmt.Println("buff 商品:", v.Name, "| 购买价格:", v.BuyMaxPrice, "| 数量:", v.BuyNum, "| 出售价格:", v.SellMinPrice, "| 数量:", v.SellNum)
		key := rediskey.GetCacheKey(v.MarketHashName)
		//查询缓存
		value, _ := gredis.HGetAll(key)
		var Info model.Info
		if len(value) == 0 {
			//缓存不存在
			//查询数据库
			_, err := myDao.GetOneInfoByMarketHashName(v.MarketHashName)
			if err != nil {
				//数据库不存在
				Info.Appid = v.Appid
				Info.GoodsId = v.Id
				Info.MarketHashName = v.MarketHashName
				Info.BuffBuyPrice = util.StringToFloat64(v.BuyMaxPrice)   //buff 购买价格
				Info.BuffBuyNum = v.BuyNum                                //buff 购买数量
				Info.BuffSellPrice = util.StringToFloat64(v.SellMinPrice) //buff 出售价格
				Info.BuffSellNum = v.SellNum                              //buff 出售数量
				Info.Game = v.Game
				Info.Name = v.Name
				Info.SteamMarketUrl = v.SteamMarketUrl //steam市场链接
				Info.IconUrl = v.GoodsInfo.OriginalIconUrl
				//插入数据库
				myDao.CreateInfo(&Info)
				//初始化缓存
				c := CacheData{
					Key:            key,
					id:             v.Id,
					name:           v.Name,
					marketHashName: v.MarketHashName,
					BuffBuyPrice:   util.StringToFloat64(v.BuyMaxPrice),
					BuffBuyNum:     v.BuyNum,
					BuffSellPrice:  util.StringToFloat64(v.SellMinPrice),
					BuffSellNum:    v.SellNum,
					SteamBuyPrice:  0,
					SteamBuyNum:    0,
					SteamSellPrice: 0,
					SteamSellNum:   0,
				}

				InitGoodCache(c)
			}
		} else {
			//缓存存在
			//批量更新数据
			Info.Name = v.Name
			Info.MarketHashName = v.MarketHashName
			Info.BuffBuyPrice = util.StringToFloat64(v.BuyMaxPrice) //buff 购买价格
			Info.BuffBuyNum = v.BuyNum                              //buff 购买数量
			//Info.BuffSellPrice = util.StringToFloat64(v.SellMinPrice) //buff 出售价格
			//Info.BuffSellNum = v.SellNum                              //buff 出售数量
			Info.GoodsId = v.Id
			InfoList = append(InfoList, &Info)

		}
		//插入缓存
		gredis.Hset(key, "buff_buy_price", v.BuyMaxPrice)
		gredis.Hset(key, "buff_buy_num", v.BuyNum)
		//gredis.Hset(key, "buff_sell_price", v.SellMinPrice)
		//gredis.Hset(key, "buff_sell_num", v.SellNum)
		gredis.Hset(key, "buff_goods_id", v.Id)
		if len(value) > 0 {
			//更新前数据
			CheckPriceChange(key, 1, value)
		}
	}
	if len(InfoList) > 0 {
		//更新数据库
		myDao.BatchBuffUpdateInfo(InfoList)
	}

}

// buff 出售channel 添加数据
func GetBuffBuy() {
	//判断chan中的数据是否为空
	go func() {
		for {
			if len(I) <= 500 {
				//读取所有商品 写入channel
				runtime.GOMAXPROCS(runtime.NumCPU())
				result, _ := myDao.GetAllInfo()
				for _, v := range result {
					I <- v
				}
			}
			time.Sleep(time.Second * 2)
		}
	}()
	//获取代理放入channel
	go getBuyProxy()
	//从channel中取出代理 开启协程 读取信息
	go getBuyData()
}

// 出售 获取代理放入 channel
func getBuyProxy() {
	num := 0
	for {
		IpData, err := httpproxy()
		if err != nil {
			log.Println(err)
		} else {

			for _, v := range IpData.Data {
				p := fmt.Sprintf("%v:%v", v.IP, v.Port)
				if num == 10 {
					proxySellChan <- p
				} else {
					proxyBuyChan <- p
				}
				num++
				if num == 10 {
					num = 0
				}
			}
		}
		time.Sleep(time.Second * 30)
	}
}

// 出售 从channel中取出代理 开启协程 读取信息
func getBuyData() {
	for {
		select {
		case proxy := <-proxyBuyChan:
			//chan中的数据为空时
			if proxy == "" {
				time.Sleep(time.Second * 1)
				continue
			}
			//开启协程
			go func() {
				key := rediskey.GetIpKey(proxy)
				//设置缓存 30秒
				gredis.Set(key, proxy, time.Duration(30)*time.Second)
				//从chan中取出商品
				for {
					info := <-I
					if info == nil {
						continue
					}
					getBuffGoodInfo(info, proxy)
				}
			}()
		}
	}
}

// 出售 开始抓取数据
func getBuffGoodInfo(info *model.Info, proxy string) {
	//设置缓存 30秒
	key := rediskey.GetIpKey(proxy)
	value := gredis.Get(key)
	//fmt.Println("proxy:", proxy, "value:", value)
	if value == "" {
		//结束协程
		fmt.Println("代理失效 - 结束协程")
		I <- info
		runtime.Goexit()
	}
	p, _ := url.Parse("http://" + proxy)
	getUrl := fmt.Sprintf("https://buff.163.com/api/market/goods/sell_order?game=csgo&goods_id=%v&page_num=1&sort_by=default&mode=&allow_tradable_cooldown=1&use_suggestion=0&_=%v", info.GoodsId, time.Now().UnixNano()/1e6)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(p),
		},
	}
	req, err := http.NewRequest("GET", getUrl, nil)
	if err != nil {
		//fmt.Println("buff err1:", err)
		return
	}
	req.AddCookie(&http.Cookie{Name: "Device-Id", Value: "nSt86DRnNpIcAVkzG5QC"})
	req.AddCookie(&http.Cookie{Name: "client_id", Value: "u591PbZEqJi47BnljKgbaA"})
	resp, err := client.Do(req)
	if err != nil {
		//fmt.Println("buff err2:", err)
		return
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		//fmt.Println("buff err3:", err)
		return
	}
	buffData := Response{}
	err = json.Unmarshal(body, &buffData)
	if err != nil {
		//fmt.Println("buff err4:", err)
		return
	}
	if buffData.Code == "OK" {
		if len(buffData.Data.Items) == 0 {
			fmt.Println("buff - 没有售卖信息")
		} else {
			go BuffBuyInfo(buffData, info)
		}
	} else {
		//fmt.Println("buff - body",string(body)	)
	}
}

// 获取代理
func httpproxy() (Ip, error) {
	client := &http.Client{}
	rqt, err := http.NewRequest("GET", "https://aapi.51daili.com/getapi2?linePoolIndex=1&packid=2&unkey=&tid=&qty=2&time=1&port=1&format=json&ss=1&css=&pro=&city=&dt=1&ct=0&service=1&usertype=17", nil)
	if err != nil {
		println("http:", "err")
		return Ip{}, err
	}
	response, _ := client.Do(rqt)
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		fmt.Println("http:", err)
		return Ip{}, err
	}

	fmt.Println("http:", string(body))
	var proxy Ip

	err = json.Unmarshal(body, &proxy)
	if err != nil {
		fmt.Println("json err:", err)
		return Ip{}, err
	}

	return proxy, nil

}

// 结束任务
func endTask(resp *http.Response, account model.BuffUser, status int, taskType int) {
	buffLocalKey := rediskey.GetBuffLocalKey()
	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
	resp.Body.Close()
	//设置本地代理抓取结束
	gredis.Del(buffLocalKey)
	//设置账号抓取结束
	gredis.Del(buffAccountKey)
	//设置账号状态
	myDao.UpdateBuffUserStatus(int(account.ID), status)
	if taskType == 1 {
		runtime.Goexit()
	}
}

// 结束任务
func endTask2(account model.BuffUser, status int, taskType int) {
	buffLocalKey := rediskey.GetBuffLocalKey()
	buffAccountKey := rediskey.GetBuffAccountKey(int(account.ID))
	//设置本地代理抓取结束
	gredis.Del(buffLocalKey)
	//设置账号抓取结束
	gredis.Del(buffAccountKey)
	//设置账号状态
	myDao.UpdateBuffUserStatus(int(account.ID), status)
	if taskType == 1 {
		runtime.Goexit()
	}
}
