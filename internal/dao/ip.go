package dao

import "buff-go/internal/model"

func (d *Dao) CountIps() int64 {
	var ip model.Ip
	return ip.CountIps(d.engine)
}

// 添加代理到数据库
func (d *Dao) AddIp(ip *model.Ip) error {
	return ip.AddIp(d.engine)
}

// 删除代理
func (d *Dao) DeleteIp(ip *model.Ip) error {
	return ip.DeleteIp(d.engine)
}

// 获取代理数量
func (d *Dao) GetIpCount() ([]*model.Ip, error) {
	var ip model.Ip
	return ip.GetIpCount(d.engine)
}
