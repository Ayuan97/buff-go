package model

import (
	"gorm.io/gorm"
)

type SteamConfig struct {
	*Model
	Sessionid        string `json:"sessionid"`
	SteamLoginSecure string `json:"steam_login_secure"`
	SteamLanguage    string `json:"steam_language"`
	Browserid        string `json:"browserid"`
	SteamCountry     string `json:"steam_country"`
}

//获取所有配置
func (s *SteamConfig) GetAll(db *gorm.DB) []*SteamConfig {
	var steamConfig []*SteamConfig
	//查询所有
	db.Find(&steamConfig)
	return steamConfig
}

//根据id  获取单个配置
func (s *SteamConfig) GetOne(db *gorm.DB) SteamConfig {
	var steamConfig SteamConfig
	//查询所有
	db.Where("id = ?", s.Model.ID).Find(&steamConfig)
	return steamConfig
}
