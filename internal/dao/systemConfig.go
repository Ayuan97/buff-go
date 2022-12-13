package dao

import (
	"buff-go/internal/model"
)

// GetSteamConfig 获取steam配置
func (d *Dao) GetOneSystemConfig(id int64) model.Config {
	s := model.Config{
		Model: &model.Model{ID: id},
	}
	return s.GetConfigOne(d.engine)
}

//根据id 更新steamCookie
func (d *Dao) UpdateSteamCookie(id int64, res int) error {
	SystemConfig := model.Config{
		Model:       &model.Model{ID: id},
		SteamCookie: res,
	}
	return SystemConfig.UpdateSteamCookie(d.engine)
}

//根据id 更新buffCookie
func (d *Dao) UpdateBuffCookie(id int64, res int) error {
	SystemConfig := model.Config{
		Model:      &model.Model{ID: id},
		BuffCookie: res,
	}
	return SystemConfig.UpdateBuffCookie(d.engine)
}

//根据id  更新 StartSteamSellPrice
func (d *Dao) UpdateStartSteamSellPrice(id int64, res int) error {

	SystemConfig := model.Config{
		Model:          &model.Model{ID: id},
		StartSteamSell: res,
	}
	return SystemConfig.UpdateStartSteamSell(d.engine)
}
