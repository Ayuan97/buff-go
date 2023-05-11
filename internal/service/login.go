package service

import (
	"fmt"
	"github.com/tebeka/selenium"
	"github.com/tebeka/selenium/chrome"
	"os"
	"time"
)

const (
	chromeDriverPath = "exec/chromedriver"
	port             = 8080
)

func Login() {
	// Start a WebDriver server instance
	opts := []selenium.ServiceOption{
		selenium.Output(os.Stderr), // Output debug information to STDERR.
	}
	selenium.SetDebug(true)
	service, err := selenium.NewChromeDriverService(chromeDriverPath, port, opts...)
	if err != nil {
		panic(err) // panic is used only as an example and is not otherwise recommended.
	}
	defer service.Stop()

	// Connect to the WebDriver instance running locally.
	caps := selenium.Capabilities{"browserName": "chrome"}
	// 去掉”被自动控制“提示
	chromeCaps := chrome.Capabilities{
		ExcludeSwitches: []string{"enable-automation"},
		Args:            []string{"no-sandbox"},
	}
	caps.AddChrome(chromeCaps)
	wd, err := selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", port))
	if err != nil {
		panic(err)
	}
	defer wd.Quit()

	//打开steam登录页面
	if err := wd.Get("https://steamcommunity.com/login/home/?goto="); err != nil {
		panic(err)
	}
	//等待页面加载完成
	time.Sleep(5 * time.Second)
	//找到 class 为 newlogindialog_TextInput_2eKVn type 为 text 的元素
	elem, err := wd.FindElement(selenium.ByCSSSelector, ".newlogindialog_TextInput_2eKVn[type=text]")
	if err != nil {
		panic(err)
	}
	//输入账号
	if err := elem.SendKeys("zhaochengyuan0002"); err != nil {
		panic(err)
	}
	//找到 class 为 newlogindialog_TextInput_2eKVn type 为 password 的元素
	elem, err = wd.FindElement(selenium.ByCSSSelector, ".newlogindialog_TextInput_2eKVn[type=password]")
	if err != nil {
		panic(err)
	}
	//输入密码
	if err := elem.SendKeys("Zhao19970223."); err != nil {
		panic(err)
	}
	//找到 class 为 newlogindialog_SubmitButton_2QgFE type 为 submit 的元素
	elem, err = wd.FindElement(selenium.ByCSSSelector, ".newlogindialog_SubmitButton_2QgFE[type=submit]")
	if err != nil {
		panic(err)
	}
	//点击登录
	if err := elem.Click(); err != nil {
		panic(err)
	}
	time.Sleep(10 * time.Second)
	//获取cookie
	cookies, err := wd.GetCookies()
	if err != nil {
		panic(err)
	}
	fmt.Println(cookies)

	//打开 新的url
	if err := wd.Get("https://steamcommunity.com/market/search/render/?query=&start=1000&count=1&search_descriptions=0&sort_column=price&sort_dir=desc&appid=730&norender=1&currency=23"); err != nil {
		panic(err)
	}

	time.Sleep(5 * time.Minute)

}
