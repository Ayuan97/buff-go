package service

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type SteamBuy struct {
	Sessionid      string  `json:"sessionid"`
	Currency       string  `json:"currency"`
	AppId          uint64  `json:"appid"`
	MarketHashName string  `json:"market_hash_name"`
	PriceTotal     float64 `json:"price_total"`
	Quantity       uint64  `json:"quantity"`
	BillingState   string  `json:"billing_state"`
	SaveMyAddress  int     `json:"save_my_address"`
}

func BuySteam() {
	reqbody := &SteamBuy{
		Sessionid:      "43503aff060fc38f5d65bc97",
		Currency:       "23",
		AppId:          730,
		MarketHashName: "Sealed Graffiti | X-Axes (Cash Green)",
		PriceTotal:     7,
		Quantity:       1,
		BillingState:   "",
	}
	req, err := http.NewRequest(
		http.MethodPost,
		"https://steamcommunity.com/market/createbuyorder/",
		strings.NewReader(url.Values{
			"appid":            {strconv.FormatUint(reqbody.AppId, 10)},
			"currency":         {reqbody.Currency},
			"market_hash_name": {reqbody.MarketHashName},
			"price_total":      {strconv.FormatUint(uint64(reqbody.PriceTotal), 10)},
			"quantity":         {strconv.FormatUint(reqbody.Quantity, 10)},
			"sessionid":        {reqbody.Sessionid},
		}.Encode()),
	)
	if err != nil {
		fmt.Println("创建请求失败:", err)
		return
	}

	var referer string
	referer = strings.Replace(reqbody.MarketHashName, " ", "%20", -1)
	referer = strings.Replace(referer, "#", "%23", -1)

	req.Header.Add(
		"Referer",
		fmt.Sprintf("https://steamcommunity.com/market/listings/%d/%s", reqbody.AppId, referer),
	)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{}
	req.AddCookie(&http.Cookie{Name: "steamCountry", Value: "HK|8ad7d7ea3737e06297549f92142430ad"})
	req.AddCookie(&http.Cookie{Name: "timezoneOffset", Value: "28800,0"})
	req.AddCookie(&http.Cookie{Name: "browserid", Value: "2640834275499774739"})
	req.AddCookie(&http.Cookie{Name: "Steam_Language", Value: "schinese"})
	req.AddCookie(&http.Cookie{Name: "steamLoginSecure", Value: "76561199226843106||eyAidHlwIjogIkpXVCIsICJhbGciOiAiRWREU0EiIH0.eyAiaXNzIjogInI6MEQzNl8yMjk2RTIxMV9ENjg2NCIsICJzdWIiOiAiNzY1NjExOTkyMjY4NDMxMDYiLCAiYXVkIjogWyAid2ViIiBdLCAiZXhwIjogMTY4NTE3NTQ5MywgIm5iZiI6IDE2NzY0NDgwNzAsICJpYXQiOiAxNjg1MDg4MDcwLCAianRpIjogIjBEMzJfMjI5NkUyMERfRTc5MkYiLCAib2F0IjogMTY4NTA4ODA2OSwgInJ0X2V4cCI6IDE3MDM0MjE2MzksICJwZXIiOiAwLCAiaXBfc3ViamVjdCI6ICIxMDMuMjIwLjc5LjExMCIsICJpcF9jb25maXJtZXIiOiAiMTAzLjIyMC43OS4xMTAiIH0.e5SO73npAWiKuvmz56WYPwHr9JYww6TWdC4u9bnd0JvZzZkKY_Z7M96DN2bvQngWi0pg6KbzU4_1QnOAFiwCBQ"})
	req.AddCookie(&http.Cookie{Name: "sessionid", Value: "43503aff060fc38f5d65bc97"})
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		fmt.Println("请求失败:", err)
		return
	}
	fmt.Println("请求成功", resp.StatusCode)
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("读取失败:", err)
		return
	}
	fmt.Println("读取成功", string(body))

}
