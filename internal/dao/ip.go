package dao

import "buff-go/internal/model"

func (d *Dao) CountIps() int64 {
	var ip model.Ip
	return ip.CountIps(d.engine)
}

// 获取一个私有代理
func (d *Dao) GetOneIp(country int) (*model.Ip, error) {
	var ip model.Ip
	return ip.GetOneIp(d.engine, country)
}

// 获取所有私有代理
func (d *Dao) GetAllPrivateIp() ([]*model.Ip, error) {
	var ip model.Ip
	return ip.GetAllPrivateIp(d.engine)
}

// 添加代理到数据库
func (d *Dao) AddIp(ip *model.Ip) error {
	return ip.AddIp(d.engine)
}

// 删除代理
func (d *Dao) DeleteIp(ip *model.Ip) error {
	return ip.DeleteIp(d.engine)
}

// 获取所有代理
func (d *Dao) GetAllIp() ([]*model.Ip, error) {
	var ip model.Ip
	return ip.GetAllIp(d.engine)
}

// 获取代理
func (d *Dao) GetIps() (*model.Ip, error) {
	var ip model.Ip
	return ip.GetIps(d.engine)
}

// 随机获取一个代理
func (d *Dao) GetRandomIps() (*model.Ip, error) {
	var ip model.Ip
	return ip.GetRandomIps(d.engine)
}

// 获取代理数量
func (d *Dao) GetIpCount() int64 {
	var ip model.Ip
	return ip.CountIps(d.engine)
}

// buff获取代理
func (d *Dao) GetBuffIps() ([]*model.Ip, error) {
	var ip model.Ip
	return ip.GetBuffIps(d.engine)
}
