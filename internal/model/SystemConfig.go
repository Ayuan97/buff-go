package model

import (
	"gorm.io/gorm"
)

type Config struct {
	*Model
	StartBuff      int     `json:"start_buff"`
	StartSteamSell int     `json:"start_steam_sell"`
	SteamDelay     int     `json:"steam_delay"`
	BuffDelay      int     `json:"buff_delay"`
	BotFilter      string  `json:"bot_filter"`
	BuffPageNum    int     `json:"buff_page_num"`
	SteamPageNum   int     `json:"steam_page_num"`
	BotProportion  float64 `json:"bot_proportion"`
	BotPrice       float64 `json:"bot_price"`
	MinPrice       float64 `json:"min_price"`
	MaxPrice       float64 `json:"max_price"`
}

// 根据id  获取单个配置
func (s *Config) GetConfigOne(db *gorm.DB) Config {
	var SystemConfig Config
	//查询所有
	db.Where("id = ?", s.ID).Find(&SystemConfig)
	return SystemConfig
}

// 根据id  更新 StartSteamSellPrice
func (s *Config) UpdateStartSteamSell(db *gorm.DB) error {
	return db.Model(&Config{}).Where("id = ?", s.Model.ID).Update("start_steam_sell", s.StartSteamSell).Error
}
