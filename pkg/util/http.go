package util

import (
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
			Value: "1-8ZFOvWWGLnOKp1n9Oku2BN_yu7Q_GdmQX4-mbNPAQoJC2034674671",
		},
	})
	return &http.Client{
		Timeout:   time.Second * 5,
		Transport: NetTransport,
		Jar:       jar,
	}
}
func HttpClient(proxyAddr string) *http.Client {
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
		Host:   "steamcommunity.com",
	}, []*http.Cookie{
		{
			Name:  "sessionid",
			Value: "cc08c404785bc602e1ae2ef5",
		},
	})
	return &http.Client{
		Timeout:   time.Second * 5,
		Transport: NetTransport,
		Jar:       jar,
	}
}
func HttpGET(client *http.Client, url string) (body []byte, err error, StatusCode int) {
	client.Do(&http.Request{
		Method: "GET",
		Header: http.Header{
			"User-Agent": []string{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/105.0.0.0 Safari/537.36"},
		},
	})
	rsp, err := client.Get(url)
	if err != nil {
		return nil, err, 0
	}
	//if rsp.StatusCode != http.StatusOK || err != nil {
	//	return nil, err, rsp.StatusCode
	//}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			fmt.Println("Close error:", err)
		}
	}(rsp.Body)
	rspBody, err := ioutil.ReadAll(rsp.Body)
	return rspBody, err, rsp.StatusCode
}
