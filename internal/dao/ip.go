package dao

import "buff-go/internal/model"

// buff获取代理（向后兼容）
func (d *Dao) GetBuffIps() ([]*model.Ip, error) {
	var ip model.Ip
	return ip.GetBuffIps(d.engine)
}

// GetProxiesForPlatform 获取指定平台的代理
func (d *Dao) GetProxiesForPlatform(platform model.Platform) ([]*model.Ip, error) {
	var ip model.Ip
	return ip.GetProxiesForPlatform(d.engine, platform)
}

// GetProxiesByRegion 根据地区获取代理
func (d *Dao) GetProxiesByRegion(region model.ProxyRegion) ([]*model.Ip, error) {
	var ip model.Ip
	return ip.GetProxiesByRegion(d.engine, region)
}

// UpdateProxyPlatformStatus 更新代理的平台状态
func (d *Dao) UpdateProxyPlatformStatus(ip *model.Ip) error {
	return ip.UpdatePlatformStatus(d.engine)
}

// GetAllIp 获取所有代理
func (d *Dao) GetAllIp() ([]*model.Ip, error) {
	var ip model.Ip
	return ip.GetAllIp(d.engine)
}
