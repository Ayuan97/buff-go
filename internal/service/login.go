package service

import (
	"fmt"
	"github.com/tebeka/selenium"
	"github.com/tebeka/selenium/chrome"
	"time"
)

type Cookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

const (
	chromeDriverPathLocal  = "exec/chromedriver_local"
	chromeDriverPathMaster = "exec/chromedriver"
	port                   = 8080
)

func LoginSteam(name string, password string) ([]*Cookie, error) {
	// Start a WebDriver server instance
	opts := []selenium.ServiceOption{
		//selenium.Output(os.Stderr), // Output debug information to STDERR.
	}
	//selenium.SetDebug(true)
	service, err := selenium.NewChromeDriverService(chromeDriverPathMaster, port, opts...)
	if err != nil {
		return nil, err
	}
	defer service.Stop()

	// Connect to the WebDriver instance running locally.
	caps := selenium.Capabilities{"browserName": "chrome"}
	// 去掉”被自动控制“提示
	chromeCaps := chrome.Capabilities{
		ExcludeSwitches: []string{"enable-automation"},
		Args:            []string{"no-sandbox"},
	}
	//添加 --headless
	chromeCaps.Args = append(chromeCaps.Args, "--headless")
	chromeCaps.Args = append(chromeCaps.Args, "--disable-gpu")
	chromeCaps.Args = append(chromeCaps.Args, "--no-sandbox")

	caps.AddChrome(chromeCaps)
	wd, err := selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", port))
	if err != nil {
		return nil, err
	}
	defer wd.Quit()

	//打开steam登录页面
	if err := wd.Get("https://steamcommunity.com/login/home/?goto="); err != nil {
		return nil, err
	}
	//等待页面加载完成
	time.Sleep(5 * time.Second)
	//找到 class 为 newlogindialog_TextInput_2eKVn type 为 text 的元素
	elem, err := wd.FindElement(selenium.ByCSSSelector, ".newlogindialog_TextInput_2eKVn[type=text]")
	if err != nil {
		return nil, err
	}
	//输入账号
	if err := elem.SendKeys(name); err != nil {
		return nil, err
	}
	//找到 class 为 newlogindialog_TextInput_2eKVn type 为 password 的元素
	elem, err = wd.FindElement(selenium.ByCSSSelector, ".newlogindialog_TextInput_2eKVn[type=password]")
	if err != nil {
		return nil, err
	}
	//输入密码
	if err := elem.SendKeys(password); err != nil {
		return nil, err
	}
	//找到 class 为 newlogindialog_SubmitButton_2QgFE type 为 submit 的元素
	elem, err = wd.FindElement(selenium.ByCSSSelector, ".newlogindialog_SubmitButton_2QgFE[type=submit]")
	if err != nil {
		return nil, err
	}
	//点击登录
	if err := elem.Click(); err != nil {
		return nil, err
	}
	time.Sleep(10 * time.Second)
	var CookieList []*Cookie
	//获取cookie
	cookies, err := wd.GetCookies()
	if err != nil {
		return nil, err
	}
	for _, v := range cookies {
		CookieList = append(CookieList, &Cookie{Name: v.Name, Value: v.Value})
	}
	return CookieList, nil
}

func AutoGetSteamCookie() {
	userList, err := myDao.GetSteamUserList()
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, v := range userList {
		if (v.Status == 2 || v.Status == 3) && v.Type == 1 {
			cookie, err := LoginSteam(v.Account, v.Password)
			if err != nil {
				fmt.Println(err)
				continue
			}
			for _, c := range cookie {
				if c.Name == "steamLoginSecure" {
					v.SteamLoginSecure = c.Value
				}
				if c.Name == "sessionid" {
					v.SessionId = c.Value
				}
				if c.Name == "browserid" {
					v.BrowserId = c.Value
				}
				if c.Name == "steamCountry" {
					v.SteamCountry = c.Value
				}
			}
			v.Status = 0
			if err := myDao.UpdateSteamUserInfo(int(v.ID), v); err != nil {
				fmt.Println(err)
				continue
			}
			myDao.UpdateSteamUserStatus(int(v.ID), 0)
			fmt.Println("登录成功 name:", v.Account)
		}
	}
}
