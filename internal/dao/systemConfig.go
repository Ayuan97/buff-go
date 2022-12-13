package dao

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"encoding/json"
	"time"
)

// GetSteamConfig 获取steam配置
func (d *Dao) GetOneSystemConfig(id int64) model.Config {
	//设置缓存
	key := rediskey.GetConfigKey()
	value := gredis.Get(key)
	if value != "" {
		var config model.Config
		//字符串解析到结构体
		json.Unmarshal([]byte(value), &config)
		return config
	} else {
		s := model.Config{
			Model: &model.Model{ID: id},
		}
		gredis.Set(key, s, time.Second*120)
		return s.GetConfigOne(d.engine)
	}

}

// 根据id 更新steamCookie
func (d *Dao) UpdateSteamCookie(id int64, res int) error {
	SystemConfig := model.Config{
		Model:       &model.Model{ID: id},
		SteamCookie: res,
	}
	return SystemConfig.UpdateSteamCookie(d.engine)
}

// 根据id 更新buffCookie
func (d *Dao) UpdateBuffCookie(id int64, res int) error {
	SystemConfig := model.Config{
		Model:      &model.Model{ID: id},
		BuffCookie: res,
	}
	return SystemConfig.UpdateBuffCookie(d.engine)
}

// 根据id  更新 StartSteamSellPrice
func (d *Dao) UpdateStartSteamSellPrice(id int64, res int) error {

	SystemConfig := model.Config{
		Model:          &model.Model{ID: id},
		StartSteamSell: res,
	}
	return SystemConfig.UpdateStartSteamSell(d.engine)
}
