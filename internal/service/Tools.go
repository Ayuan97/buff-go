package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"context"
	"fmt"
	"github.com/chromedp/chromedp"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"time"
)

// 获取系统配置
func GetSystemConfig() model.Config {
	return myDao.GetOneSystemConfig(1)
}

//检查价格是否有变动
func CheckPriceChange(key string, checkType int, Info model.Info) {
	//获取 key 的所有 hget
	data, _ := gredis.HGetAll(key)
	if data != nil {
		for k, v := range data {

			isChange := false
			if checkType == 1 {
				//buff 价格变动
				if k == "buff_buy_max_price" {
					if Info.BuffBuyPrice != util.StringToFloat64(v) {
						isChange = true
						fmt.Println("buff 购买价格变动:", Info.MarketHashName)
					}
				}
				if k == "buff_sell_min_price" {
					if Info.BuffSellPrice != util.StringToFloat64(v) {
						isChange = true
						fmt.Println("buff 出售价格变动", Info.MarketHashName)
					}
				}
			} else {
				//steam 价格变动
				if k == "steam_sell_price" {
					if Info.SteamSellPrice != util.StringToFloat64(v) {
						isChange = true
						fmt.Println("steam 购买价格变动", Info.MarketHashName)
					}
				}
				if k == "steam_buy_max_price" {
					if Info.SteamBuyPrice != util.StringToFloat64(v) {
						isChange = true
						fmt.Println("steam 出售价格变动", Info.MarketHashName)
					}
				}
			}
			if isChange {
				//发送telegram通知

				//检测是否需要购买操作

			}
		}
	}
}

// 更新比例
func UpdateGoodsProportion(goodsId int, cat_type int) {

	goodsInfo, err := myDao.GetGoodsByGoodsId(int64(goodsId))
	if err != nil {
		fmt.Println("UpdateGoodsProportion err :", err)
		return
	}
	if goodsInfo.GoodsId > 0 {
		goodsInfo.Proportion = goodsInfo.BuyMaxPrice / goodsInfo.SteamSellPrice
		err := myDao.UpdateGoodsRatio(goodsInfo, goodsInfo.Proportion)
		if err != nil {
			fmt.Println("更新比例失败", err)
			return
		}
		config := myDao.GetOneSystemConfig(1)
		if cat_type == 2 && goodsInfo.Proportion >= config.BotProportion && goodsInfo.SteamSellPrice >= config.BotPrice {
			//商品名称是否包含 印花
			if !util.Contains(goodsInfo.MarketHashName, "Sticker") {
				//sendTelegram(goodsInfo)
			}
		}
	}

}

// 登录steam获取cookie
func LoginSteam() {
	options := []chromedp.ExecAllocatorOption{
		chromedp.Flag("headless", false), // debug使用
		chromedp.Flag("blink-settings", "imagesEnabled=true"),
		chromedp.UserAgent(`Mozilla/5.0 (Windows NT 6.3; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/73.0.3683.103 Safari/537.36`),
	}
	options = append(chromedp.DefaultExecAllocatorOptions[:], options...)
	c, _ := chromedp.NewExecAllocator(context.Background(), options...)

	// create context
	ctx, cancel := chromedp.NewContext(c, chromedp.WithLogf(log.Printf))
	defer cancel()

	// navigate to a page, wait for an element, click
	var example string
	err := chromedp.Run(ctx,
		//打开网页
		chromedp.Navigate("https://steamcommunity.com/login/home/?goto="),
		////等待3秒
		//chromedp.Sleep(5*time.Second),
		//等待元素出现
		chromedp.WaitVisible(`#responsive_page_template_content > div.page_content > div:nth-child(1) > div > div > div > div.newlogindialog_FormContainer_3jLIH > div`),
		//输入账号
		chromedp.SendKeys(`#responsive_page_template_content > div.page_content > div:nth-child(1) > div > div > div > div.newlogindialog_FormContainer_3jLIH > div > form > div:nth-child(1) > input`, "zhao19970223"),
		//输入密码
		chromedp.SendKeys(`#responsive_page_template_content > div.page_content > div:nth-child(1) > div > div > div > div.newlogindialog_FormContainer_3jLIH > div > form > div:nth-child(2) > input`, "ZHAO19970223."),
		//点击登录
		chromedp.Click(`#responsive_page_template_content > div.page_content > div:nth-child(1) > div > div > div > div.newlogindialog_FormContainer_3jLIH > div > form > div.newlogindialog_SignInButtonContainer_14fsn > button`, chromedp.NodeVisible),
		//等待3秒
		chromedp.Sleep(10*time.Second),
		//打开网页
		chromedp.Navigate("https://steamcommunity.com/market/"),
		//等待3秒
		chromedp.Sleep(10*time.Second),

		//获取浏览器cookie
		chromedp.Evaluate(`document.cookie`, &example),
	)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Go's time.After example:\n%s", example)
}

// 清楚所有账号缓存
func ClearAllAccountCache() {

	buffLocalKey := rediskey.GetBuffLocalKey()
	gredis.Del(buffLocalKey)
	steamLocalKey := rediskey.GetProxySteamKey("127.0.0.1")
	gredis.Del(steamLocalKey)

	buffUserList, err := myDao.GetBuffUserList()
	if err != nil {
		fmt.Println("获取buff用户列表失败", err)
		return
	}
	for _, buffUser := range buffUserList {
		buffAccountKey := rediskey.GetBuffAccountKey(int(buffUser.ID))
		gredis.Del(buffAccountKey)
		myDao.UpdateBuffUserStatus(int(buffUser.ID), 0)
		fmt.Sprintf("清除buff用户缓存成功,用户id:%d", buffUser.ID)
	}

	steamUserList, err := myDao.GetSteamUserList()
	for _, steamUser := range steamUserList {
		steamAccountKey := rediskey.GetSteamAccountKey(int(steamUser.ID))
		fmt.Println("清除:", steamAccountKey)
		gredis.Del(steamAccountKey)
		myDao.UpdateSteamUserStatus(int(steamUser.ID), 0)
		fmt.Println("清除steam用户缓存成功,用户id:", steamUser.ID)

	}

	ipsList, err := myDao.GetAllPrivateIp()
	if err != nil {
		fmt.Println("获取私有ip列表失败", err)
		return
	}
	for _, ip := range ipsList {
		address := ip.Ip + ":" + strconv.Itoa(ip.Port)
		key := rediskey.GetProxySteamKey(address)
		fmt.Println("清除:", key)
		gredis.Del(key)

	}

}

// 检测文件大小 超过1M则清空
func CheckLogFileSize() {
	fileInfo, err := os.Stat("log.txt")
	if err != nil {
		fmt.Println("获取文件信息失败", err)
		return
	}
	if fileInfo.Size() > 1024*1024*1 {
		//输出 "" 到文件
		err := ioutil.WriteFile("log.txt", []byte(""), 0777)
		if err != nil {
			fmt.Println("清空文件失败", err)
			return
		}
	}
}
