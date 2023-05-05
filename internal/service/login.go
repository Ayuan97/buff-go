package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/util"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const (
	rsakeyURL     = "https://steamcommunity.com/login/getrsakey"
	loginURL      = "https://steamcommunity.com/login/dologin/"
	cacheDuration = 5 * time.Minute
)

type steamLogin struct {
	Username      string
	Password      string
	TwoFactorCode string
}

type rsaKey struct {
	Modulus       string `json:"publickey_mod"`
	Exponent      string `json:"publickey_exp"`
	Timestamp     string `json:"timestamp"`
	ModulusBigInt *big.Int
	ExponentInt   *big.Int
}

type loginResponse struct {
	Success             bool `json:"success"`
	Requires2FA         bool `json:"requires_twofactor"`
	LoginComplete       bool `json:"login_complete"`
	transfer_parameters struct {
		SteamId       string `json:"steamid"`
		TokenSecure   string `json:"token_secure"`
		Autth         string `json:"auth"`
		RememberLogin bool   `json:"remember_login"`
	}
}

func AutoGetSteamCookie() {
	go func() {
		//每隔5分钟执行一次
		ticker := time.NewTicker(cacheDuration)
		for {
			//查询所有steam账号
			allUser, _ := myDao.GetSteamUserList()
			for _, v := range allUser {
				if v.Status == 0 && v.Type == 1 {
					info, err := login(v)
					if err != nil {
						fmt.Println("登录失败", err)
					}
					if info.Success == true {
						// 更新账号信息
						v.SteamLoginSecure = info.transfer_parameters.SteamId + "||" + info.transfer_parameters.TokenSecure
						v.Status = 1
						myDao.UpdateSteamUserInfo(int(v.ID), *v)
					} else {
						fmt.Println("登录失败", info)
					}
				}
			}
			<-ticker.C
		}
	}()

}

func login(steamInfo *model.SteamUser) (loginResponse, error) {
	steamUser := steamLogin{
		Username: steamInfo.Account,
		Password: steamInfo.Password,
	}

	// 定义请求参数
	values := url.Values{}
	values.Set("username", steamUser.Username)
	resp, _ := http.PostForm("https://steamcommunity.com/login/getrsakey", values)
	body, _ := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	var rsaKey rsaKey
	// 将公钥模数和公钥指数进行转换
	modulus, _ := new(big.Int).SetString(rsaKey.Modulus, 16)
	E := util.StringToInt(rsaKey.Exponent)
	pub := &rsa.PublicKey{
		N: modulus,
		E: E,
	}
	// 加密
	encryptedPassword, _ := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(steamUser.Password))
	//base64编码
	encodedPassword := base64.StdEncoding.EncodeToString(encryptedPassword)

	// 准备请求参数
	data := url.Values{}
	data.Set("username", steamUser.Username)
	data.Set("password", encodedPassword)
	data.Set("captchagid", "-1")
	data.Set("captcha_text", "")
	data.Set("emailsteamid", "")
	data.Set("emailauth", "")
	data.Set("rsatimestamp", rsaKey.Timestamp)
	data.Set("remember_login", "false")

	// 创建一个 cookie jar
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}

	// 创建一个 HTTP 客户端
	client := &http.Client{
		Jar: jar,
	}
	jar.SetCookies(&url.URL{
		Scheme: "https",
		Host:   "steamcommunity.com",
	}, []*http.Cookie{
		{
			Name:  "sessionid",
			Value: steamInfo.SessionId,
		},
		{
			Name:  "steamLoginSecure",
			Value: steamInfo.SteamLoginSecure,
		},
		{
			Name:  "steamCountry",
			Value: steamInfo.SteamCountry,
		},
	})

	// 发送 HTTP POST 请求
	req, err := http.NewRequest("POST", "https://steamcommunity.com/login/dologin/", strings.NewReader(data.Encode()))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	// 打印响应内容和 cookie
	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	var loginResponse loginResponse
	err = json.Unmarshal(body, &loginResponse)
	if err != nil {
		return loginResponse, err
	}
	return loginResponse, nil

}
