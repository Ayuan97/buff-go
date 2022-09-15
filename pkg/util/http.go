package util

import (
	"buff-go/global"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"
)

func NewHttpClient(proxyAddr string) *http.Client {
	NetTransport := &http.Transport{}
	if proxyAddr == "" {
		NetTransport = &http.Transport{
			MaxIdleConnsPerHost:   10,                              //每个host最大空闲连接
			ResponseHeaderTimeout: time.Second * time.Duration(10), //数据收发5秒超时
		}
	} else {
		proxy, err := url.Parse(proxyAddr)
		if err != nil {
			return nil
		}
		NetTransport = &http.Transport{
			Proxy:                 http.ProxyURL(proxy),
			MaxIdleConnsPerHost:   10,                              //每个host最大空闲连接
			ResponseHeaderTimeout: time.Second * time.Duration(10), //数据收发5秒超时
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
			Value: "1-RZC4P8_b1S1y8lNR7X2L2cPMungE5Fz8WTUcs5w_NQK52034674671",
		},
	})
	return &http.Client{
		Timeout:   time.Second * 5,
		Transport: NetTransport,
		Jar:       jar,
	}
}

func HttpGET(client *http.Client, url string) (body []byte, err error, StatusCode int) {
	rsp, err := client.Get(url)
	if err != nil {
		global.Logger.Warning("err:", err)
		return nil, err, 0
	}
	if rsp.StatusCode != http.StatusOK || err != nil {
		a, _ := ioutil.ReadAll(rsp.Body)
		global.Logger.Info("body:", string(a), "err:", err, "code:", rsp.StatusCode)
		return nil, err, rsp.StatusCode
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			fmt.Println("Close error:", err)
		}
	}(rsp.Body)
	rspBody, err := ioutil.ReadAll(rsp.Body)
	global.Logger.Info("rspBody:", string(rspBody), "err:", err, "StatusCode:", rsp.StatusCode)
	return rspBody, err, rsp.StatusCode
}
