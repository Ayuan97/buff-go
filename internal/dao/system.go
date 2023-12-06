package dao

import "buff-go/internal/model"

// GetSteamConfig 获取steam配置
func (d *Dao) GetOneSystem(id int64) model.System {
	System := model.System{
		Model: &model.Model{ID: id},
	}
	return System.GetSystemOne(d.engine)
}
