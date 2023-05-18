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
	req.AddCookie(&http.Cookie{Name: "steamCountry", Value: "HK%7C8ad7d7ea3737e06297549f92142430ad"})
	req.AddCookie(&http.Cookie{Name: "timezoneOffset", Value: "28800,0"})
	req.AddCookie(&http.Cookie{Name: "browserid", Value: "2640834275499774739"})
	req.AddCookie(&http.Cookie{Name: "Steam_Language", Value: "schinese"})
	req.AddCookie(&http.Cookie{Name: "steamLoginSecure", Value: "76561199029489705%7C%7CeyAidHlwIjogIkpXVCIsICJhbGciOiAiRWREU0EiIH0.eyAiaXNzIjogInI6MTY2M18yMjg1Q0VEQ19FQThEMSIsICJzdWIiOiAiNzY1NjExOTkwMjk0ODk3MDUiLCAiYXVkIjogWyAid2ViIiBdLCAiZXhwIjogMTY4NDQ4ODA0NywgIm5iZiI6IDE2NzU3NjA0NzUsICJpYXQiOiAxNjg0NDAwNDc1LCAianRpIjogIjBEMjBfMjI4REE3MjRfQjNGQjAiLCAib2F0IjogMTY4Mzg4NDU4NiwgInJ0X2V4cCI6IDE3MDE3NDU2ODYsICJwZXIiOiAwLCAiaXBfc3ViamVjdCI6ICIxMDMuMjIwLjc5LjExMCIsICJpcF9jb25maXJtZXIiOiAiMTAzLjIyMC43OS4xMTAiIH0.YzoOBVA9xYvJ9xlC5ubZWDaXXXPkOIWVLOov5XUdervEE7fEefHbAoqi7shtwbLzXopALxG4Zk2yosRm4l7HAA"})
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
