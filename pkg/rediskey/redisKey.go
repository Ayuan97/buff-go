package rediskey

import (
	"buff-go/pkg/util"
	"fmt"
)

const ProxyMap = "proxy:list:map:%d" //代理池
const GetbuffKey = "get:buff:key"
const GetSteamItemIdKey = "get:steamItemId:key"
const GetSteamSellPriceKey = "get:steamSellPrice:key"
const ConfigKey = "get:config:key"
const CronBuffKey = "cron:buff:key"
const CronSteamSellPriceKey = "cron:steam:sell:price:key"
const GoodsNameKey = "goods:name:key:%d"

const BuffLocalKey = "buff:local:key"
const SteamLocalKey = "steam:local:key"
const ProxySteamKey = "proxy:steam:key:%s"

// 代理key
func GetProxySteamKey(ip string) string {
	return fmt.Sprintf(ProxySteamKey, ip)
}

// 商品缓存
func GetCacheKey(name string) string {
	//将名称转换为md5字符串
	str := util.StringToMD5(name)
	return fmt.Sprintf("goods:cache:key:%s", str)
}

// 查询本地代理是否在使用中 - buff
func GetBuffLocalKey() string {
	return BuffLocalKey
}

// 查询本地代理是否在使用中 - steam
func GetSteamLocalKey() string {
	return SteamLocalKey
}

// 查询buff账号是否在使用中 - buff
func GetBuffAccountKey(id int) string {
	return fmt.Sprintf("buff:account:key:%d", id)
}

// 查询steam账号是否在使用中 - steam
func GetSteamAccountKey(id int) string {
	return fmt.Sprintf("steam:account:key:%d", id)
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
