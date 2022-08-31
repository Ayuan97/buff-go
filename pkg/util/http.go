package util

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"
)

//采集代理返回的参数
type proxyResult struct {
	Ip           string `json:"ip"`           //ip
	Port         int    `json:port`           //端口
	Agreement    string `json:agreement`      //请求协议
	Anonymous    string `json:anonymous`      //透明度
	Region       string `json:region`         //地区
	Speed        string `json:"speed"`        //响应速度
	Source       string `json:"source"`       //来源（采集资源站）
	Verification string `json:"verification"` //验证时间
}

//采集代理所需的参数
type ProxyParamet struct {
	IpIndex           int `json:"ipIndex"`           //ip下标
	PortIndex         int `json:"portIndex"`         //端口下标
	AgreementIndex    int `json:"agreementIndex"`    //请求协议下标
	AnonymousIndex    int `json:"anonymousIndex"`    //透明度下标
	RegionIndex       int `json:"regionIndex"`       //地区下标
	SpeedIndex        int `json:"speedIndex"`        //响应速度下标
	SourceIndex       int `json:"sourceIndex"`       //来源（采集资源站）下标
	VerificationIndex int `json:"verificationIndex"` //验证时间下标
}

var proxy string

func StartRequestProxy(address string) string {
	posturl := address
	proxy := "http://f104.kdltps.com:15818"
	cli := newHttpClient(proxy)
	cli.Do(&http.Request{
		Method: "GET",
		Header: http.Header{
			"User-Agent": []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3770.100 Safari/537.36"},
		},
	})
	fmt.Println("抓取url:", posturl)
	fmt.Println("proxy:", proxy)

	data, err := httpGET(cli, posturl)
	if err != nil || len(data) == 0 {
		fmt.Println("httpGET error:", err)
		//等待500毫秒后重新请求
		time.Sleep(time.Millisecond * 100)
		if proxy != "" {
			proxy = ""
		} else {
			//proxy =  GetProxyInfo()
			proxy = "https://f104.kdltps.com:20818"

		}
		StartRequestProxy(address)
	}
	return string(data)
}
func newHttpClient(proxyAddr string) *http.Client {
	NetTransport := &http.Transport{}
	if proxyAddr == "" {
		NetTransport = &http.Transport{
			MaxIdleConnsPerHost:   10,                             //每个host最大空闲连接
			ResponseHeaderTimeout: time.Second * time.Duration(5), //数据收发5秒超时
		}
	} else {
		proxy, err := url.Parse(proxyAddr)
		if err != nil {
			return nil
		}
		NetTransport = &http.Transport{
			Proxy:                 http.ProxyURL(proxy),
			MaxIdleConnsPerHost:   10,                             //每个host最大空闲连接
			ResponseHeaderTimeout: time.Second * time.Duration(5), //数据收发5秒超时
		}
	}

	//设置cookie
	jar, err := cookiejar.New(nil)
	if err != nil {
		fmt.Println("cookiejar error:", err)
	}

	jar.SetCookies(&url.URL{
		Scheme: "https",
		Host:   "buff.163.com",
	}, []*http.Cookie{
		{
			Name:  "session",
			Value: "1-PMm_yUmcynVRlZ76nl_uxnYZesz4Hyqzn_AMZLEmcLyA2034674671",
		},
	})
	return &http.Client{
		Timeout:   time.Second * 5,
		Transport: NetTransport,
		Jar:       jar,
	}
}

func httpGET(client *http.Client, url string) (body []byte, err error) {
	rsp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	if rsp.StatusCode != http.StatusOK || err != nil {
		err = fmt.Errorf("HTTP GET Code=%v, URI=%v, err=%v", rsp.StatusCode, url, err)
		return nil, err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			//fmt.Println("Close error:", err)
		}
	}(rsp.Body)

	return ioutil.ReadAll(rsp.Body)
}

type Proxy struct {
	Msg  string `json:"msg"`
	Code int    `json:"code"`
	Data struct {
		Count          int      `json:"count"`
		DedupCount     int      `json:"dedup_count"`
		OrderLeftCount int      `json:"order_left_count"`
		ProxyList      []string `json:"proxy_list"`
	} `json:"data"`
}

//获取代理信息
func GetProxyInfo() string {
	client := &http.Client{}
	var url string
	url = "https://dps.kdlapi.com/api/getdps/?orderid=955975957452070&num=1&signature=swzue8lxt5l3etjr45h17de7drek0axn&area=%E6%B1%9F%E8%8B%8F&carrier=2&pt=1&dedup=1&format=json&sep=1"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	bodyText, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	//fmt.Println(string(bodyText))
	//解析到结构体
	var proxy Proxy
	err = json.Unmarshal(bodyText, &proxy)
	if err != nil {
		log.Fatal(err)
	}
	//fmt.Println(proxy.Data.ProxyList[0])
	return "http://" + proxy.Data.ProxyList[0]
}
