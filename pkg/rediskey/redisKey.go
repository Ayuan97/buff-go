package rediskey

import "fmt"

const ProxyMap = "proxy:list:map:%d" //代理池
const GetbuffKey = "get:buff:key"
const GetSteamItemIdKey = "get:steamItemId:key"
const GetSteamSellPriceKey = "get:steamSellPrice:key"

// GetProxyMap 获取代理池
func GetProxyMap(poolType int) string {
	return fmt.Sprintf(ProxyMap, poolType)
}

func GetBuffKey() string {
	return GetbuffKey
}
func GetSteamItemId() string {
	return GetSteamItemIdKey
}
func GetSteamSePriceKey() string {
	return GetSteamSellPriceKey
}
