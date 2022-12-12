package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"context"
	"fmt"
	"github.com/chromedp/chromedp"
	"log"
	"time"
)

//获取系统配置
func GetSystemConfig() model.Config {
	return myDao.GetOneSystemConfig(1)
}

// 更新比例
func UpdateGoodsProportion(goodsId int) {

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

		if goodsInfo.Proportion > 0.9 && goodsInfo.SteamSellPrice > 100 {
			//设置1分钟缓存 1*60
			key := rediskey.GetGoodsNameKey(goodsInfo.GoodsId)
			value := gredis.Get(key)
			if value != "1" {
				sendTelegram(goodsInfo)
				gredis.Set(key, "1", time.Second*1*60)
			} else {
				//fmt.Println("已经发送过了")
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
