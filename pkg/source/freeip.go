package source

import (
	"buff-go/internal/model"
	"fmt"
	"github.com/gocolly/colly"
	"strconv"
)

// free-proxy get ip from https://free-proxy-list.net/
func FreeProxy() []*model.Ip {
	var ips []*model.Ip

	fmt.Println("开始抓取 https://free-proxy-list.net/")
	c := colly.NewCollector(
		//colly.Async(false), //设置为异步请求
		colly.AllowedDomains("free-proxy-list.net"),
	)
	//c.Limit(&colly.LimitRule{
	//	DomainGlob:  "*sslproxies.org*",
	//	Parallelism: 1,
	//Delay:       20 * time.Second,
	//RandomDelay: 5 * time.Second,
	//})
	c.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/106.0.0.0 Safari/537.36"
	c.OnRequest(func(r *colly.Request) {
		fmt.Println("Visiting", r.URL)
	})
	c.OnHTML("#list > div > div.table-responsive > div", func(e *colly.HTMLElement) {

		fmt.Println("分析html")

		e.ForEach("tr", func(i int, element *colly.HTMLElement) {
			//fmt.Println("分析tr", i, element.Text)
			ip := element.ChildText("td:nth-child(1)")
			port, _ := strconv.Atoi(element.ChildText("td:nth-child(2)"))
			ok := element.ChildText("td:nth-child(7)")
			fmt.Println("ip:", ip, "port:", port, "ok:", ok)
			IsHttps := 0
			if ok == "yes" {
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
				Source:  "free-proxy-list.net",
			})
		})

	})
	//c.OnResponse(func(r *colly.Response) {
	//	fmt.Println("response received", string(r.Body))
	//})
	c.OnError(func(r *colly.Response, err error) {
		fmt.Println("抓取错误:", r.StatusCode, "重试")

	})
	PrimitiveUrl := "https://free-proxy-list.net/"

	err := c.Visit(PrimitiveUrl)
	if err != nil {
		fmt.Println("抓取错误:", err)
		return nil
	}

	c.Wait()

	return ips
}
