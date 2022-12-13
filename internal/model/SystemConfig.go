package model

import (
	"gorm.io/gorm"
)

type Config struct {
	*Model
	StartBuff          int     `json:"start_buff"`
	StartSteamSell     int     `json:"start_steam_sell"`
	SteamSellCatSecond int     `json:"steam_sell_cat_second"`
	BuffCookie         int     `json:"buff_cookie"`
	SteamCookie        int     `json:"steam_cookie"`
	BotProportion      float64 `json:"bot_proportion"`
	BotPrice           float64 `json:"bot_price"`
}

// 根据id  获取单个配置
func (s *Config) GetConfigOne(db *gorm.DB) Config {
	var SystemConfig Config
	//查询所有
	db.Where("id = ?", s.ID).Find(&SystemConfig)
	return SystemConfig
}

// 根据id 更新steamCookie
func (s *Config) UpdateSteamCookie(db *gorm.DB) error {
	return db.Model(&Config{}).Where("id = ?", s.Model.ID).Update("steam_cookie", s.SteamCookie).Error
}

// 根据id  更新 StartSteamSellPrice
func (s *Config) UpdateStartSteamSell(db *gorm.DB) error {
	return db.Model(&Config{}).Where("id = ?", s.Model.ID).Update("start_steam_sell", s.StartSteamSell).Error
}

// 根据id 更新buffCookie
func (s *Config) UpdateBuffCookie(db *gorm.DB) error {
	return db.Model(&Config{}).Where("id = ?", s.Model.ID).Update("buff_cookie", s.BuffCookie).Error
}
