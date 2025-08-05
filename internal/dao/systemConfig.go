package dao

import (
	"buff-go/internal/model"
	"buff-go/pkg/gredis"
	"buff-go/pkg/rediskey"
	"encoding/json"
	"fmt"
	"time"
)

// GetOneSystemConfig 获取系统配置
func (d *Dao) GetOneSystemConfig(id int64) model.Config {
	//设置缓存
	key := rediskey.GetConfigKey(int(id))
	value := gredis.Get(key)
	if value == "" {
		s := model.Config{
			Model: &model.Model{ID: id},
		}
		config := s.GetConfigOne(d.engine)

		// 如果从数据库获取到了有效配置，尝试保存到数据库（确保数据库中有记录）
		if config.ID == 0 {
			// 如果数据库中没有记录，创建默认记录
			d.createDefaultConfigIfNotExists(id)
			// 重新获取配置
			config = s.GetConfigOne(d.engine)
		}

		//结构体转换为字符串，增加缓存时间到30分钟
		str, _ := json.Marshal(config)
		gredis.Set(key, str, time.Minute*30)
		return config
	} else {
		var config model.Config
		//字符串解析到结构体
		json.Unmarshal([]byte(value), &config)
		return config
	}
}

// createDefaultConfigIfNotExists 如果配置不存在则创建默认配置
func (d *Dao) createDefaultConfigIfNotExists(id int64) {
	var count int64
	d.engine.Model(&model.Config{}).Where("id = ?", id).Count(&count)
	if count == 0 {
		// 根据ID确定游戏类型
		gameName := "csgo" // 默认CSGO
		if id == 2 {
			gameName = "dota2"
		}

		// 创建默认配置
		defaultConfig := &model.Config{
			Model:              &model.Model{ID: id},
			GameName:           gameName,
			BuffBuyStatus:      1,  // 默认启用
			BuffSellStatus:     1,  // 默认启用
			SteamBuyStatus:     0,  // 默认禁用
			SteamSellStatus:    0,  // 默认禁用
			BuffBuyDelay:       5,  // 5秒延迟
			BuffSellDelay:      5,  // 5秒延迟
			SteamBuyDelay:      10, // 10秒延迟
			SteamSellDelay:     10, // 10秒延迟
			BotFilter:          "",
			BuffPageNum:        10,   // 默认10页
			SteamPageNum:       5,    // 默认5页
			BotBuffProportion:  0.95, // 95%
			BotSteamProportion: 0.95, // 95%
			BotPrice:           100.0,
			MinPrice:           0.01,   // 最小价格0.01
			MaxPrice:           1000.0, // 最大价格1000
		}

		// 根据游戏类型调整默认值
		if gameName == "dota2" {
			defaultConfig.BuffPageNum = 5 // DOTA2默认较少页面
			defaultConfig.SteamPageNum = 3
		}

		d.engine.Create(defaultConfig)
	}
}

// UpdateSystemConfig 更新系统配置
func (d *Dao) UpdateSystemConfig(id int64, config *model.Config) error {
	// 更新数据库
	err := d.engine.Model(&model.Config{}).Where("id = ?", id).Updates(config).Error
	if err != nil {
		return err
	}

	// 清除缓存
	key := rediskey.GetConfigKey(int(id))
	gredis.Del(key)

	// 发布配置变更通知
	d.publishConfigChange(id)

	return nil
}

// publishConfigChange 发布配置变更通知
func (d *Dao) publishConfigChange(configID int64) {
	// 使用Redis发布/订阅机制通知配置变更
	channel := fmt.Sprintf("config_change:%d", configID)
	message := map[string]interface{}{
		"config_id": configID,
		"timestamp": time.Now().Unix(),
		"action":    "update",
	}

	messageBytes, _ := json.Marshal(message)
	gredis.Publish(channel, string(messageBytes))

	// 同时发布到全局配置变更频道
	gredis.Publish("config_change:all", string(messageBytes))
}

// ClearConfigCache 清除配置缓存
func (d *Dao) ClearConfigCache(id int64) {
	key := rediskey.GetConfigKey(int(id))
	gredis.Del(key)
}

// GetConfigByGameName 根据游戏名称获取配置
func (d *Dao) GetConfigByGameName(gameName string) model.Config {
	// 根据游戏名称确定ID
	var id int64 = 1 // 默认CSGO
	if gameName == "dota2" {
		id = 2
	}
	return d.GetOneSystemConfig(id)
}

// GetActiveGameConfig 获取当前活跃的游戏配置
// 优先级：CSGO > DOTA2，返回第一个启用的配置
func (d *Dao) GetActiveGameConfig() (model.Config, string, error) {
	// 先尝试获取CSGO配置
	csgoConfig := d.GetOneSystemConfig(1)
	if csgoConfig.ID != 0 && csgoConfig.BuffBuyStatus == 1 {
		return csgoConfig, "csgo", nil
	}

	// 如果CSGO未启用，尝试DOTA2配置
	dota2Config := d.GetOneSystemConfig(2)
	if dota2Config.ID != 0 && dota2Config.BuffBuyStatus == 1 {
		return dota2Config, "dota2", nil
	}

	// 如果都未启用，返回CSGO配置作为默认
	if csgoConfig.ID != 0 {
		return csgoConfig, "csgo", nil
	}

	// 如果CSGO配置也不存在，返回DOTA2配置
	return dota2Config, "dota2", nil
}

// GetAllConfigs 获取所有游戏配置
func (d *Dao) GetAllConfigs() map[string]model.Config {
	configs := make(map[string]model.Config)
	configs["csgo"] = d.GetOneSystemConfig(1)
	configs["dota2"] = d.GetOneSystemConfig(2)
	return configs
}
