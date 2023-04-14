package model

import (
	"gorm.io/gorm"
)

type Config struct {
	*Model
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

// 根据id  获取单个配置
func (s *Config) GetConfigOne(db *gorm.DB) Config {
	var SystemConfig Config
	//查询所有
	db.Where("id = ?", s.ID).Find(&SystemConfig)
	return SystemConfig
}
