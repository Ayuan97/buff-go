package service

import (
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"fmt"
	"strconv"
)

type CacheData struct {
	Key            string
	id             int
	name           string
	marketHashName string
	BuffBuyPrice   float64
	BuffBuyNum     int
	BuffSellPrice  float64
	BuffSellNum    int
	SteamBuyPrice  float64
	SteamBuyNum    int
	SteamSellPrice float64
	SteamSellNum   int
}

// 初始化该商品的缓存
func InitGoodCache(c CacheData) {
	//初始化商品缓存
	gredis.Hset(c.Key, "buff_buy_price", strconv.FormatFloat(c.BuffBuyPrice, 'f', 2, 64))
	gredis.Hset(c.Key, "buff_buy_num", strconv.Itoa(c.BuffBuyNum))
	gredis.Hset(c.Key, "buff_sell_price", strconv.FormatFloat(c.BuffSellPrice, 'f', 2, 64))
	gredis.Hset(c.Key, "buff_sell_num", strconv.Itoa(c.BuffSellNum))
	gredis.Hset(c.Key, "steam_buy_price", strconv.FormatFloat(c.SteamBuyPrice, 'f', 2, 64))
	gredis.Hset(c.Key, "steam_buy_num", strconv.Itoa(c.SteamBuyNum))
	gredis.Hset(c.Key, "steam_sell_price", strconv.FormatFloat(c.SteamSellPrice, 'f', 2, 64))
	gredis.Hset(c.Key, "steam_sell_num", strconv.Itoa(c.SteamSellNum))
	gredis.Hset(c.Key, "name", c.name)
	gredis.Hset(c.Key, "market_hash_name", c.marketHashName)
	gredis.Hset(c.Key, "buff_goods_id", c.id)

}

// 检查价格是否有变动
func CheckPriceChange(key string, checkType int, oldValue map[string]string) {
	//获取 key 的所有 hget
	data, _ := gredis.HGetAll(key)
	if data != nil && len(data) > 0 {
		//计算比例
		p := "0"
		num := 0.0
		if util.StringToFloat64(data["buff_buy_price"]) == 0 || (util.StringToFloat64(data["steam_sell_price"])) == 0 {
			p = "0"
		} else {
			num = util.StringToFloat64(data["buff_buy_price"]) / util.StringToFloat64(data["steam_sell_price"])
			s := strconv.FormatFloat(num, 'f', 2, 64)
			//只保留两位小数
			p = s
		}
		fmt.Println("比例", p, "buff_buy_price", data["buff_buy_price"], "steam_sell_price", data["steam_sell_price"], "goods_id", data["goods_id"])
		data["proportion"] = p
		if checkType == 1 {
			if data["buff_buy_max_price"] != oldValue["buff_buy_max_price"] || data["buff_sell_min_price"] != oldValue["buff_sell_min_price"] {
				send(data, checkType)
			}
		}
		if checkType == 2 {
			if data["steam_sell_price"] != oldValue["steam_sell_price"] {
				send(data, checkType)
			}
		}

	}
}

// 清楚所有账号缓存
func ClearAllAccountCache() {

	buffLocalKey := rediskey.GetBuffLocalKey()
	gredis.Del(buffLocalKey)
	steamLocalKey := rediskey.GetProxySteamKey("127.0.0.1")
	gredis.Del(steamLocalKey)

	buffUserList, err := myDao.GetBuffUserList()
	if err != nil {
		fmt.Println("获取buff用户列表失败", err)
		return
	}
	for _, buffUser := range buffUserList {
		buffAccountKey := rediskey.GetBuffAccountKey(int(buffUser.ID))
		gredis.Del(buffAccountKey)
		myDao.UpdateBuffUserStatus(int(buffUser.ID), 0)
		fmt.Sprintf("清除buff用户缓存成功,用户id:%d", buffUser.ID)
	}

	steamUserList, err := myDao.GetSteamUserList()
	for _, steamUser := range steamUserList {
		steamAccountKey := rediskey.GetSteamAccountKey(int(steamUser.ID))
		fmt.Println("清除:", steamAccountKey)
		gredis.Del(steamAccountKey)
		myDao.UpdateSteamUserStatus(int(steamUser.ID), 0)
		fmt.Println("清除steam用户缓存成功,用户id:", steamUser.ID)

	}

	ipsList, err := myDao.GetAllPrivateIp()
	if err != nil {
		fmt.Println("获取私有ip列表失败", err)
		return
	}
	for _, ip := range ipsList {
		address := ip.Ip + ":" + strconv.Itoa(ip.Port)
		key := rediskey.GetProxySteamKey(address)
		fmt.Println("清除:", key)
		gredis.Del(key)

	}

}
