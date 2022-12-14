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
