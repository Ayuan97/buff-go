package main

import (
	"fmt"
	"github.com/chromedp/chromedp"
	"golang.org/x/net/context"
	"log"
	"time"
)

func main() {
	c, err := GetHttpHtmlContent("https://steamcommunity.com/market/listings/730/AWP%20%7C%20PAW%20%28Minimal%20Wear%29?filter=%E7%8C%AB%E7%8C%AB", "#searchResultsRows", "body")
	if err != nil {
		log.Println(err)
	}

	fmt.Println(c)
}

// 获取网站上爬取的数据
func GetHttpHtmlContent(url string, selector string, sel interface{}) (string, error) {
	options := []chromedp.ExecAllocatorOption{
		chromedp.CombinedOutput(log.Writer()),
		chromedp.Flag("headless", false), // debug使用
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("blink-settings", "imagesEnabled=false"),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.106 Safari/537.36"),

		//chromedp.UserAgent(`Mozilla/5.0 (Windows NT 6.3; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/73.0.3683.103 Safari/537.36`),
	}
	//初始化参数，先传一个空的数据
	options = append(chromedp.DefaultExecAllocatorOptions[:], options...)

	c, _ := chromedp.NewExecAllocator(context.Background(), options...)

	// create context
	chromeCtx, cancel := chromedp.NewContext(c, chromedp.WithLogf(log.Printf))
	// 执行一个空task, 用提前创建Chrome实例
	chromedp.Run(chromeCtx, make([]chromedp.Action, 0, 1)...)

	//创建一个上下文，超时时间为40s
	timeoutCtx, cancel := context.WithTimeout(chromeCtx, 10*time.Second)
	defer cancel()

	var htmlContent string
	err := chromedp.Run(timeoutCtx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(selector),
		chromedp.OuterHTML(sel, &htmlContent, chromedp.ByJSPath),
	)
	if err != nil {
		fmt.Printf("Run err : %v\n\n", err)
		return "", err
	}
	//log.Println(htmlContent)

	return htmlContent, nil
}
