package source

import (
	"buff-go/internal/model"
	"fmt"
	"github.com/gocolly/colly"
	"strconv"
)

// free-proxy get ip from https://hidemy.name/en/proxy-list/
func Hidemy() []*model.Ip {
	var ips []*model.Ip

	fmt.Println("开始抓取 https://hidemy.name/en/proxy-list/")
	c := colly.NewCollector(
		//colly.Async(false), //设置为异步请求
		colly.AllowedDomains("hidemy.name"),
	)
	c.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/106.0.0.0 Safari/537.36"

	c.OnHTML("body > div.wrap > div.services_proxylist.services > div > div.table_block > table > tbody", func(e *colly.HTMLElement) {

		e.ForEach("tr", func(i int, element *colly.HTMLElement) {
			//fmt.Println("分析tr", i, element.Text)
			ip := element.ChildText("td:nth-child(1)")
			port, _ := strconv.Atoi(element.ChildText("td:nth-child(2)"))
			ok := element.ChildText("td:nth-child(5)")
			IsHttps := 0
			if ok == "https" {
				IsHttps = 1
			} else {
				IsHttps = 0
			}
			fmt.Println("ip:", ip, "port:", port, "is_https:", IsHttps)
			ips = append(ips, &model.Ip{
				Ip:      ip,
				Port:    port,
				IsHttps: strconv.Itoa(IsHttps),
				Type:    1,
				Source:  "hidemy",
			})
		})

	})
	//c.OnResponse(func(r *colly.Response) {
	//	fmt.Println("response received", string(r.Body))
	//})
	c.OnError(func(r *colly.Response, err error) {
		fmt.Println("抓取 hidemy 错误:", r.StatusCode, "重试")

	})
	PrimitiveUrl := "https://hidemy.name/en/proxy-list/"

	err := c.Visit(PrimitiveUrl)
	if err != nil {
		fmt.Println("抓取 hidemy 错误:", err)
		return nil
	}

	c.Wait()

	return ips
}
