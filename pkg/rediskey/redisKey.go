package rediskey

import "fmt"

const ProxyMap = "proxy:list:map:%d" //代理池
const GetbuffKey = "get:buff:key"
const GetSteamItemIdKey = "get:steamItemId:key"
const GetSteamSellPriceKey = "get:steamSellPrice:key"
const ConfigKey = "get:config:key"
const CronBuffKey = "cron:buff:key"
const CronSteamSellPriceKey = "cron:steam:sell:price:key"
const GoodsNameKey = "goods:name:key:%d"

const BuffLocalKey = "buff:local:key"

// 查询本地代理是否在使用中
func GetBuffLocalKey() string {
	return BuffLocalKey
}

// 查询buff账号是否在使用中
func GetBuffAccountKey(id int) string {
	return fmt.Sprintf("buff:account:key:%d", id)
}

func GetConfigKey() string {
	return ConfigKey
}

// 系统是否开启了steam出售价格获取
func GetCronSteamSellPriceKey() string {
	return CronSteamSellPriceKey
}

// 系统是否开启了buff商品列表获取
func GetCronBuffKey() string {
	return CronBuffKey
}

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
func GetGoodsNameKey(id int) string {
	return fmt.Sprintf(GoodsNameKey, id)
}
