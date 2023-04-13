package dao

import (
	"buff-go/internal/model"
)

// 查询商品名称是否存在
func (d *Dao) GetOneInfoByMarketHashName(name string) (*model.Info, error) {
	var info model.Info
	return info.GetInfoByMarketHashName(d.engine, name)
}

// 插入商品信息
func (d *Dao) CreateInfo(info *model.Info) bool {
	return info.Create(d.engine, info)
}

// 批量更新商品信息
func (d *Dao) BatchBuffUpdateInfo(infoList []*model.Info) bool {
	var info model.Info
	return info.BatchBuffUpdate(d.engine, infoList)
}

// 获取所有商品信息
func (d *Dao) GetAllInfo() ([]*model.Info, error) {
	var info model.Info
	return info.GetAll(d.engine)
}

// 获取所有商品信息 item_name_id 为空的
func (d *Dao) GetAllInfoBySteamItemId() ([]*model.Info, error) {
	var info model.Info
	return info.GetAllBySteamItemId(d.engine)
}

// 根据goodsid更新商品信息
func (d *Dao) UpdateInfoByGoodsId(info *model.Info) error {

	return info.UpdateInfoByGoodsId(d.engine, info)
}

// 根据id更新 steam_item_name_id
func (d *Dao) UpdateInfoBySteamItemId(info *model.Info) error {

	return info.UpdateInfoBySteamItemId(d.engine, info)
}

// 根据id更新数据
func (d *Dao) UpdateInfo(info *model.Info) error {
	return info.UpdateInfo(d.engine, info)
}
