package rediskey

import (
	"buff-go/pkg/util"
	"fmt"
)

const CheckPrice = "check:price:list"
const Proxy = "proxy:%s:%s"
const ConfigKey = "get:config:key"
const IpKey = "ip:key:%s"

func CheckPriceList() string {
	return CheckPrice
}

// 获取代理是否在使用中
// source 1 buff 2 steam
func GetProxyMapKey(ip string, source string) string {
	return fmt.Sprintf(Proxy, ip, source)
}

// 设置ip
func GetIpKey(ip string) string {
	return fmt.Sprintf(IpKey, ip)
}

// 商品缓存
func GetCacheKey(name string) string {
	//将名称转换为md5字符串
	str := util.StringToMD5(name)
	return fmt.Sprintf("goods:cache:key:%s", str)
}

// 查询steam账号是否在使用中 - steam
func GetSteamAccountKey(id int) string {
	return fmt.Sprintf("steam:account:key:%d", id)
}

func GetConfigKey() string {
	return ConfigKey
}
