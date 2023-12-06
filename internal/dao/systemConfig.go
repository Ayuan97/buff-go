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
	key := rediskey.GetConfigKey(int(id))
	value := gredis.Get(key)
	if value == "" {
		s := model.Config{
			Model: &model.Model{ID: id},
		}
		config := s.GetConfigOne(d.engine)
		//结构体转换为字符串
		str, _ := json.Marshal(config)
		gredis.Set(key, str, time.Second*60)
		return config
	} else {
		var config model.Config
		//字符串解析到结构体
		json.Unmarshal([]byte(value), &config)
		return config
	}

}
