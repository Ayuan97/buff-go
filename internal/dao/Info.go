package dao

import "buff-go/internal/model"

//查询商品名称是否存在
func (d *Dao) GetOneInfoByMarketHashName(name string) (*model.Info, error) {
	var info model.Info
	return info.GetInfoByMarketHashName(d.engine, name)
}

//插入商品信息
func (d *Dao) CreateInfo(info *model.Info) bool {
	return info.Create(d.engine, info)
}

//批量更新商品信息
func (d *Dao) BatchBuffUpdateInfo(infoList []*model.Info) bool {
	var info model.Info
	return info.BatchBuffUpdate(d.engine, infoList)
}
