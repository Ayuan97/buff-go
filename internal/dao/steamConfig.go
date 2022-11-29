package dao

import "buff-go/internal/model"

// GetSteamConfig 获取steam配置
func (d *Dao) GetSteamConfig() []*model.SteamConfig {
	var steamConfig model.SteamConfig
	return steamConfig.GetAll(d.engine)
}

// GetSteamConfig 获取steam配置
func (d *Dao) GetOneSteamConfig(id int64) model.SteamConfig {
	steamConfig := model.SteamConfig{
		Model: &model.Model{ID: id},
	}
	return steamConfig.GetOne(d.engine)
}
