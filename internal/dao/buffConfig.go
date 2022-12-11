package dao

import "buff-go/internal/model"

// GetSteamConfig 获取steam配置
func (d *Dao) GetBuffConfig() []*model.BuffConfig {
	var buffConfig model.BuffConfig
	return buffConfig.GetBuffConfigAll(d.engine)
}

// GetSteamConfig 获取steam配置
func (d *Dao) GetOneBuffConfig(id int64) model.BuffConfig {
	buffConfig := model.BuffConfig{
		Model: &model.Model{ID: id},
	}
	return buffConfig.GetBuffConfigOne(d.engine)
}
