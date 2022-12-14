package dao

import "buff-go/internal/model"

func (d *Dao) CountIps() int64 {
	var ip model.Ip
	return ip.CountIps(d.engine)
}
