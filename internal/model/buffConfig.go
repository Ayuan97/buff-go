package model

import (
	"gorm.io/gorm"
)

type BuffConfig struct {
	*Model
	Sessionid   string `json:"sessionid"`
	MinPrice    int    `json:"min_price"`
	MaxPrice    int    `json:"max_price"`
	Parallelism int    `json:"parallelism"`
	RandomDelay int    `json:"random_delay"`
	Delay       int    `json:"delay"`
	PageNum     int    `json:"page_num"`
}

// 获取所有配置
func (s *BuffConfig) GetBuffConfigAll(db *gorm.DB) []*BuffConfig {
	var buffConfig []*BuffConfig
	//查询所有
	db.Find(&buffConfig)
	return buffConfig
}

// 根据id  获取单个配置
func (s *BuffConfig) GetBuffConfigOne(db *gorm.DB) BuffConfig {
	var buffConfig BuffConfig
	//查询所有
	db.Where("id = ?", s.Model.ID).Find(&buffConfig)
	return buffConfig
}
