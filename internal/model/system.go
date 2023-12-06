package model

import "gorm.io/gorm"

type System struct {
	*Model
	SystemType int64 `json:"system_type"`
}

// 根据id  获取单个配置
func (s *System) GetSystemOne(db *gorm.DB) System {
	var System System
	//查询所有
	db.Where("id = ?", s.ID).Find(&System)
	return System
}
