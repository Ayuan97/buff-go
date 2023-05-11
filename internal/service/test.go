package service

import (
	"fmt"
	"io/ioutil"
	"net/http"
)

func Test() {
	//s, _ := myDao.GetSteamUserInfo(5)
	//info, err := login(&s)
	//if err != nil {
	//	fmt.Println("登录失败", err)
	//	return
	//}
	//if info.Success == true {
	//	fmt.Println("登录成功", info)
	//	// 更新账号信息
	//	s.SteamLoginSecure = info.transfer_parameters.SteamId + "%7C%7C" + info.transfer_parameters.TokenSecure
	//	s.Status = 1
	//	myDao.UpdateSteamUserInfo(int(s.ID), s)
	//} else {
	//	fmt.Println("登录失败", info)
	//	return
	//}
	//curl 请求
	geturl := fmt.Sprintf("https://steamcommunity.com/market/search/render/?query=&start=%v&count=10&search_descriptions=0&sort_column=price&sort_dir=desc&appid=730&norender=1&currency=23", 100)
	client := &http.Client{}

	req, err := http.NewRequest("GET", geturl, nil)
	if err != nil {
		fmt.Println("steam err: 发起请求失败", err)
		return
	}
	req.AddCookie(&http.Cookie{Name: "steamCountry", Value: "HK%7C8ad7d7ea3737e06297549f92142430ad"})
	req.AddCookie(&http.Cookie{Name: "timezoneOffset", Value: "28800,0"})
	req.AddCookie(&http.Cookie{Name: "browserid", Value: "2824364291139976372"})
	req.AddCookie(&http.Cookie{Name: "Steam_Language", Value: "schinese"})
	req.AddCookie(&http.Cookie{Name: "steamLoginSecure", Value: "76561199502270846%7C%7CeyAidHlwIjogIkpXVCIsICJhbGciOiAiRWREU0EiIH0.eyAiaXNzIjogInI6MEQyOV8yMjg0NkJBNF80RTdEMiIsICJzdWIiOiAiNzY1NjExOTk1MDIyNzA4NDYiLCAiYXVkIjogWyAid2ViIiBdLCAiZXhwIjogMTY4Mzg4Mzg4OSwgIm5iZiI6IDE2NzUxNTYxMDksICJpYXQiOiAxNjgzNzk2MTA5LCAianRpIjogIjBEMjlfMjI4NDZCQTRfNEU5MkUiLCAib2F0IjogMTY4Mzc5NjEwOSwgInJ0X2V4cCI6IDE3MDE5ODM2MDMsICJwZXIiOiAwLCAiaXBfc3ViamVjdCI6ICIxMDMuMjIwLjc5LjExMCIsICJpcF9jb25maXJtZXIiOiAiMTAzLjIyMC43OS4xMTAiIH0.9UP1zE3lcs0fhd8QFUlaiYh4r3iRA-3_SHll6_gWK6LFtAKArrMBg5SPkRW8Jf-r6xE8XALSw_a526ZgqkbeBA"})
	req.AddCookie(&http.Cookie{Name: "sessionid", Value: "f6cd809eda4291c05073b4ea"})
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("steam err2:", err)
		return
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("steam err3:", err)
		return
	}
	fmt.Println(string(body))
}
