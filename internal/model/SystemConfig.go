package model

import (
	"gorm.io/gorm"
)

type Config struct {
	*Model
	GameName           string  `json:"game_name" gorm:"column:game_name"`
	BuffBuyStatus      int     `json:"buff_buy_status"`
	BuffSellStatus     int     `json:"buff_sell_status"`
	SteamBuyStatus     int     `json:"steam_buy_status"`
	SteamSellStatus    int     `json:"steam_sell_status"`
	BuffBuyDelay       int     `json:"buff_buy_delay"`
	BuffSellDelay      int     `json:"buff_sell_delay"`
	SteamBuyDelay      int     `json:"steam_buy_delay"`
	SteamSellDelay     int     `json:"steam_sell_delay"`
	BotFilter          string  `json:"bot_filter"`
	BuffPageNum        int     `json:"buff_page_num"`
	SteamPageNum       int     `json:"steam_page_num"`
	BotBuffProportion  float64 `json:"bot_buff_proportion"`
	BotSteamProportion float64 `json:"bot_steam_proportion"`
	BotPrice           float64 `json:"bot_price"`
	MinPrice           float64 `json:"min_price"`
	MaxPrice           float64 `json:"max_price"`
}

// 表名
func (Config) TableName() string {
	return "system_config"
}

// 根据id  获取单个配置
func (s *Config) GetConfigOne(db *gorm.DB) Config {
	var SystemConfig Config
	//使用First方法，如果找不到记录会返回错误
	err := db.Where("id = ?", s.ID).First(&SystemConfig).Error
	if err != nil {
		// 如果记录不存在，返回默认配置
		if err == gorm.ErrRecordNotFound {
			return s.getDefaultConfig()
		}
		// 其他错误也返回默认配置
		return s.getDefaultConfig()
	}
	return SystemConfig
}

// getDefaultConfig 获取默认配置
func (s *Config) getDefaultConfig() Config {
	// 根据ID确定游戏类型
	gameName := "csgo" // 默认CSGO
	if s.ID == 2 {
		gameName = "dota2"
	}

	defaultConfig := Config{
		Model:              &Model{ID: s.ID},
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

	return defaultConfig
}
