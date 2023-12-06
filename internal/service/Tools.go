package service

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"buff-go/pkg/util"
	"fmt"
	"strconv"
	"time"
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
// checkType 1 buff-求购 2 buff-出售 3 steam-求购 4 steam-出售
func CheckPriceChange(key string, checkType int, oldValue map[string]string) {
	//获取当前要抓取的项目
	system := myDao.GetOneSystem(1)
	//获取系统配置
	var config model.Config
	if system.SystemType == 1 {
		config = myDao.GetOneSystemConfig(1) //csgo
	} else {
		config = myDao.GetOneSystemConfig(2) //dota2
	}
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
		//更新商品信息

		if (checkType == 1 || checkType == 4) && data["market_hash_name"] != "" {
			p := fmt.Sprintf("%.2f", num)
			//myDao.UpdateInfoBuffProportion(data["market_hash_name"], util.StringToFloat64(p))
			var info model.Info
			changeTime := time.Now().Unix()
			info.MarketHashName = data["market_hash_name"]
			info.BuffProportion = util.StringToFloat64(p)
			if checkType == 1 {
				info.BuffBuyPrice = util.StringToFloat64(data["buff_buy_price"])
				info.BuffBuyNum = util.StringToInt(data["buff_buy_num"])
				info.BuffBuyUpdate = int(changeTime)
			} else {
				info.SteamSellPrice = util.StringToFloat64(data["steam_sell_price"])
				info.SteamSellNum = util.StringToInt(data["steam_sell_num"])
				info.SteamSellUpdate = int(changeTime)
			}
			myDao.UpdateInfoByMarketHashName(&info)

		} else {
			p := fmt.Sprintf("%.2f", num)
			var info model.Info
			changeTime := time.Now().Unix()
			info.MarketHashName = data["market_hash_name"]
			info.SteamProportion = util.StringToFloat64(p)
			if checkType == 2 {
				info.BuffSellPrice = util.StringToFloat64(data["buff_sell_price"])
				info.BuffSellNum = util.StringToInt(data["buff_sell_num"])
				info.BuffSellUpdate = int(changeTime)
			} else {
				info.SteamBuyPrice = util.StringToFloat64(data["steam_buy_price"])
				info.SteamBuyNum = util.StringToInt(data["steam_buy_num"])
				info.SteamBuyUpdate = int(changeTime)
			}
			myDao.UpdateInfoByMarketHashName(&info)
		}

		//is_push 是否存在
		if data["is_push"] == "" {
			data["is_push"] = "1"
		}
		//更新时间相差大于一个小时不推送
		if data["is_push"] == "1" {
			if (checkType == 1 || checkType == 4) && (num >= config.BotBuffProportion && num != 0.0) {
				if util.StringToInt(data["buff_buy_update"])-util.StringToInt(data["steam_sell_update"]) < 60*60 {
					fmt.Println("buff:name:", data["market_hash_name"], "buff_buy_update:", data["buff_buy_update"], "steam_sell_update:", data["steam_sell_update"], "is_push:", data["is_push"])
					send(data)
				}
			} else if (checkType == 2 || checkType == 3) && (num <= config.BotSteamProportion && num != 0.0) {
				send(data)
			}
		}
	}

}

// 清楚所有账号缓存
func ClearBuffSell() {

	ips, _ := myDao.GetBuffIps()
	for _, ip := range ips {
		address := ip.Ip + ":" + strconv.Itoa(ip.Port)
		key := rediskey.GetProxyMapKey(address, "buff")
		gredis.Del(key)
	}

	buffUserList, err := myDao.GetBuffUserList()
	if err != nil {
		//fmt.Println("获取buff用户列表失败", err)
		return
	}
	for _, buffUser := range buffUserList {
		myDao.UpdateBuffUserStatus(int(buffUser.ID), 0)
		fmt.Sprintf("清除buff用户缓存成功,用户id:%d", buffUser.ID)
	}

	//清楚config 缓存
	configKey := rediskey.GetConfigKey(1)
	gredis.Del(configKey)
	configKey = rediskey.GetConfigKey(2)
	gredis.Del(configKey)
	fmt.Println("清楚config缓存")

}

func ClearSteamSell() {
	ips, _ := myDao.GetBuffIps()
	for _, ip := range ips {
		address := ip.Ip + ":" + strconv.Itoa(ip.Port)
		key := rediskey.GetProxyMapKey(address, "steam")
		gredis.Del(key)
	}

	steamUserList, _ := myDao.GetSteamUserList()
	for _, steamUser := range steamUserList {
		steamAccountKey := rediskey.GetSteamAccountKey(int(steamUser.ID))
		fmt.Println("清除:", steamAccountKey)
		gredis.Del(steamAccountKey)
		myDao.UpdateSteamUserStatus(int(steamUser.ID), 0)
		fmt.Println("清除steam用户缓存成功,用户id:", steamUser.ID)

	}

	//清楚config 缓存
	configKey := rediskey.GetConfigKey(1)
	gredis.Del(configKey)
	configKey = rediskey.GetConfigKey(2)
	gredis.Del(configKey)
	fmt.Println("清楚config缓存")
}

// 获取一个没有在用的代理
// 1->buff 2->steam
func GetOneProxy(source string) (*model.Ip, string, string, error) {
	ips, err := myDao.GetBuffIps()
	if err != nil {
		return nil, "", "", err
	}
	for _, ip := range ips {
		address := ip.Ip + ":" + strconv.Itoa(ip.Port)
		key := rediskey.GetProxyMapKey(address, source)
		value := gredis.Get(key)
		fmt.Println("key:", key, "value:", value)
		if value == "" {
			return ip, address, key, nil
		}
	}
	return nil, "", "", nil
}
