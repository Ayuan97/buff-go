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
	gredis.Hset(c.Key, "is_push", "1")
	gredis.Hset(c.Key, "is_buy", "1")

}

// CheckPriceChange 检查价格是否有变动
// checkType 1 buff-buy 2 buff-sell 3 steam-buy 4 steam-sell
func CheckPriceChange(key string, checkType int, oldValue map[string]string) {
	config := myDao.GetOneSystemConfig(1)

	//获取 key 的所有 hget
	data, _ := gredis.HGetAll(key)
	if data != nil && len(data) > 0 {
		num := 0.0
		//计算比例
		if checkType == 1 {
			//buff-求购 更新
			//buff 求购 / steam 出售 = 比例
			buffBuyPrice := util.StringToFloat64(data["buff_buy_price"])        //buff 求购
			steamSellPrice := util.StringToFloat64(data["steam_sell_price"])    //steam 出售
			oldBuffBuyPrice := util.StringToFloat64(oldValue["buff_buy_price"]) //buff 求购 旧

			if buffBuyPrice != 0 && steamSellPrice != 0 {
				//价格是否变化
				if buffBuyPrice != oldBuffBuyPrice {
					num = buffBuyPrice / steamSellPrice
					p := strconv.FormatFloat(num, 'f', 2, 64)
					data["proportion"] = p
					data["change_type"] = strconv.Itoa(checkType)
					data["change_name"] = "buff"
				}
			}
		} else if checkType == 2 {
			//buff-出售 更新
			//buff 出售 / (steam 求购) * 0.85 = 比例
			buffSellPrice := util.StringToFloat64(data["buff_sell_price"])        //buff 出售
			steamBuyPrice := util.StringToFloat64(data["steam_buy_price"])        //steam 求购
			oldBuffSellPrice := util.StringToFloat64(oldValue["buff_sell_price"]) //buff 出售 旧
			//oldSteamBuyPrice := util.StringToFloat64(oldValue["steam_buy_price"])

			if buffSellPrice != 0 && steamBuyPrice != 0 {
				//价格是否变化
				if buffSellPrice != oldBuffSellPrice {
					num = buffSellPrice / (steamBuyPrice * 0.87)
					p := strconv.FormatFloat(num, 'f', 2, 64)
					data["proportion"] = p
					data["change_type"] = strconv.Itoa(checkType)
					data["change_name"] = "buff"
				}
			}
		} else if checkType == 3 {
			//steam-求购 更新
			//(steam 求购) * 0.85  / buff 出售= 比例
			buffSellPrice := util.StringToFloat64(data["buff_sell_price"])        //buff 出售
			steamBuyPrice := util.StringToFloat64(data["steam_buy_price"])        //steam 求购
			oldSteamBuyPrice := util.StringToFloat64(oldValue["steam_buy_price"]) //steam 求购 旧

			if steamBuyPrice != 0 && buffSellPrice != 0 {
				//价格是否变化
				if steamBuyPrice != oldSteamBuyPrice {
					num = buffSellPrice / (steamBuyPrice * 0.87)
					p := strconv.FormatFloat(num, 'f', 2, 64)
					data["proportion"] = p
					data["change_type"] = strconv.Itoa(checkType)
					data["change_name"] = "steam"
				}
			}
		} else if checkType == 4 {
			//steam-出售 更新
			//buff 求购 / steam 出售 = 比例
			buffBuyPrice := util.StringToFloat64(data["buff_buy_price"])            //buff 求购
			steamSellPrice := util.StringToFloat64(data["steam_sell_price"])        //steam 出售
			oldSteamSellPrice := util.StringToFloat64(oldValue["steam_sell_price"]) //steam 出售 旧

			if buffBuyPrice != 0 && steamSellPrice != 0 {
				//价格是否变化
				if steamSellPrice != oldSteamSellPrice {
					num = buffBuyPrice / steamSellPrice
					p := strconv.FormatFloat(num, 'f', 2, 64)
					data["proportion"] = p
					data["change_type"] = strconv.Itoa(checkType)
					data["change_name"] = "steam"

				}
			}
		}
		//更新比例
		if (checkType == 1 || checkType == 4) && num != 0.0 && data["market_hash_name"] != "" {
			p := fmt.Sprintf("%.2f", num)
			//更新比例
			myDao.UpdateInfoBuffProportion(data["market_hash_name"], util.StringToFloat64(p))
		} else {
			p := fmt.Sprintf("%.2f", num)
			myDao.UpdateInfoSteamProportion(data["market_hash_name"], util.StringToFloat64(p))
		}

		//is_push 是否存在
		if data["is_push"] == "" {
			data["is_push"] = "1"
		}
		if data["is_push"] == "1" {
			if (checkType == 1 || checkType == 4) && (num >= config.BotBuffProportion && num != 0.0) {
				send(data)
			} else if (checkType == 2 || checkType == 3) && (num <= config.BotSteamProportion && num != 0.0) {
				send(data)
			}
		}
	}

}

// 清楚所有账号缓存
func ClearAllAccountCache() {

	buffLocalKey := rediskey.GetBuffLocalKey()
	gredis.Del(buffLocalKey)
	steamLocalKey := rediskey.GetProxySteamKey("127.0.0.1:80")
	gredis.Del(steamLocalKey)

	buffUserList, err := myDao.GetBuffUserList()
	if err != nil {
		//fmt.Println("获取buff用户列表失败", err)
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

	//清楚config 缓存
	configKey := rediskey.GetConfigKey()
	gredis.Del(configKey)
	fmt.Println("清楚config缓存")

}
