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
	fmt.Printf("开始获取系统配置 ID：%d\n", id)

	//设置缓存
	key := rediskey.GetConfigKey(int(id))
	value := gredis.Get(key)
	if value == "" {
		fmt.Printf("缓存中未找到配置，从数据库查询 ID：%d 缓存键：%s\n", id, key)

		s := model.Config{
			Model: &model.Model{ID: id},
		}
		config, err := s.GetConfigOne(d.engine)
		if err != nil {
			fmt.Printf("数据库查询配置失败 ID：%d 错误：%v\n", id, err)
			panic(fmt.Sprintf("致命错误：数据库查询配置失败 ID：%d 错误：%v", id, err))
		}

		if config == nil {
			fmt.Printf("数据库中未找到配置记录 ID：%d\n", id)
			panic(fmt.Sprintf("致命错误：数据库中未找到配置记录 ID：%d", id))
		}

		if config.ID == 0 {
			fmt.Printf("配置记录ID为0，数据异常 ID：%d\n", id)
			panic(fmt.Sprintf("致命错误：配置记录ID为0，数据异常 查询ID：%d", id))
		}

		fmt.Printf("数据库查询配置成功 ID：%d 游戏：%s 抓取状态：%d\n", config.ID, config.GameName, config.BuffBuyStatus)

		//结构体转换为字符串，增加缓存时间到30分钟
		str, _ := json.Marshal(*config)
		gredis.Set(key, str, time.Minute*30)
		return *config
	} else {
		fmt.Printf("从缓存获取配置 ID：%d 缓存键：%s\n", id, key)
		var config model.Config
		//字符串解析到结构体
		err := json.Unmarshal([]byte(value), &config)
		if err != nil {
			fmt.Printf("缓存数据解析失败 ID：%d 错误：%v\n", id, err)
			// 缓存数据损坏，清理缓存并重新从数据库获取
			gredis.Del(key)
			return d.GetOneSystemConfig(id)
		}

		fmt.Printf("缓存获取配置成功 ID：%d 游戏：%s 抓取状态：%d\n", config.ID, config.GameName, config.BuffBuyStatus)
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
			Status:             1,  // 默认启用状态
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

// GetAllConfigs 获取所有游戏配置
func (d *Dao) GetAllConfigs() map[string]model.Config {
	configs := make(map[string]model.Config)
	configs["csgo"] = d.GetOneSystemConfig(1)
	configs["dota2"] = d.GetOneSystemConfig(2)
	return configs
}

// GetEnabledConfigs 获取所有启用状态的游戏配置
func (d *Dao) GetEnabledConfigs() map[string]model.Config {
	allConfigs := d.GetAllConfigs()
	enabledConfigs := make(map[string]model.Config)

	for gameName, config := range allConfigs {
		if config.ID != 0 && config.Status == 1 {
			enabledConfigs[gameName] = config
			fmt.Printf("找到启用配置 游戏：%s ID：%d 状态：%d\n", gameName, config.ID, config.Status)
		} else {
			fmt.Printf("跳过未启用配置 游戏：%s ID：%d 状态：%d\n", gameName, config.ID, config.Status)
		}
	}

	return enabledConfigs
}

// GetEnabledConfigByGameName 根据游戏名称获取启用状态的配置
func (d *Dao) GetEnabledConfigByGameName(gameName string) (model.Config, error) {
	config := d.GetConfigByGameName(gameName)

	if config.ID == 0 {
		return model.Config{}, fmt.Errorf("配置不存在 游戏：%s", gameName)
	}

	if config.Status != 1 {
		return model.Config{}, fmt.Errorf("配置未启用 游戏：%s 状态：%d", gameName, config.Status)
	}

	fmt.Printf("获取启用配置成功 游戏：%s ID：%d 状态：%d\n", gameName, config.ID, config.Status)
	return config, nil
}
