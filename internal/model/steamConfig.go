package model

import (
	"gorm.io/gorm"
)

type SteamConfig struct {
	*Model
	Desc  string `json:"desc"`
	Key   string `json:"key"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

//获取所有配置
func (s *SteamConfig) GetAll(db *gorm.DB) []*SteamConfig {
	var steamConfig []*SteamConfig
	//查询所有
	db.Find(&steamConfig)
	return steamConfig
}
