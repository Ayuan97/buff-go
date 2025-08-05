package dao

import "buff-go/internal/model"

// buff获取代理
func (d *Dao) GetBuffIps() ([]*model.Ip, error) {
	var ip model.Ip
	return ip.GetBuffIps(d.engine)
}
