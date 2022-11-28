package dao

import "buff-go/internal/model"

// GetSteamConfig 获取steam配置
func (d *Dao) GetSteamConfig() []*model.SteamConfig {
	var steamConfig model.SteamConfig
	return steamConfig.GetAll(d.engine)
}
