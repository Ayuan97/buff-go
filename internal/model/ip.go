package model

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ProxyRegion 代理地区类型
type ProxyRegion int

const (
	ProxyRegionDomestic ProxyRegion = iota + 1 // 国内代理
	ProxyRegionHongKong                        // 香港代理
	ProxyRegionOverseas                        // 海外代理
)

// Platform 平台类型
type Platform string

const (
	PlatformBuff  Platform = "buff"
	PlatformSteam Platform = "steam"
	// 为未来扩展预留
	PlatformC5Game   Platform = "c5game"
	PlatformIGXE     Platform = "igxe"
	PlatformUUYouPin Platform = "uuyoupin"
)

// PlatformStatus 平台状态
type PlatformStatus struct {
	IsActive    bool      `json:"is_active"`    // 在该平台是否可用
	FailCount   int       `json:"fail_count"`   // 在该平台的失败次数
	LastFailed  time.Time `json:"last_failed"`  // 最后失败时间
	BannedUntil time.Time `json:"banned_until"` // 封禁到期时间
}

type Ip struct {
	*Model
	Ip                 string      `json:"data"`
	Type               int         `json:"type"`
	Name               string      `json:"name"`
	PassWord           string      `json:"pass_word"`
	ProxyType          int         `json:"proxy_type"`
	Country            int         `json:"country"`
	IsHttps            string      `json:"is_https"`
	Speed              int         `json:"speed"`
	Source             string      `json:"source"`
	Port               int         `json:"port"`
	Region             ProxyRegion `json:"region" gorm:"default:1"`              // 代理地区
	SupportedPlatforms string      `json:"supported_platforms" gorm:"type:text"` // 支持的平台列表，JSON格式
	PlatformStatuses   string      `json:"platform_statuses" gorm:"type:text"`   // 各平台状态，JSON格式
	IsGlobalActive     bool        `json:"is_global_active" gorm:"default:true"` // 全局是否激活
}

func (i *Ip) CountIps(db *gorm.DB) int64 {
	var num int64
	err := db.Model(&i).Where("id > 0 ").Count(&num).Error
	if err != nil {
		fmt.Println("CountIps err :", err)
		return 0
	}
	return num
}

func (i *Ip) AddIp(db *gorm.DB) error {
	return db.Create(i).Error
}

func (i *Ip) DeleteIp(db *gorm.DB) error {
	return db.Delete(i).Error
}

func (i *Ip) GetAllIp(db *gorm.DB) ([]*Ip, error) {
	var ips []*Ip
	err := db.Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// 获取一个代理
func (i *Ip) GetIps(db *gorm.DB) (*Ip, error) {
	var ips *Ip
	err := db.Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// 随机获取一个代理
func (i *Ip) GetRandomIps(db *gorm.DB) (*Ip, error) {
	var ips *Ip
	err := db.Order("rand()").Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// 获取一个私有代理
func (i *Ip) GetOneIp(db *gorm.DB, country int) (*Ip, error) {
	var ips *Ip
	err := db.Where("type = 2 and country = ?", country).Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// 获取所有私有代理
func (i *Ip) GetAllPrivateIp(db *gorm.DB) ([]*Ip, error) {
	var ips []*Ip
	err := db.Where("type = 2").Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// buff 获取所有代理 country = 1 or country = 3
func (i *Ip) GetBuffIps(db *gorm.DB) ([]*Ip, error) {
	var ips []*Ip
	err := db.Where("country = 1 or country = 3").Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// GetSupportedPlatforms 获取支持的平台列表
func (i *Ip) GetSupportedPlatforms() ([]Platform, error) {
	if i.SupportedPlatforms == "" {
		// 根据地区返回默认支持的平台
		return i.getDefaultPlatformsByRegion(), nil
	}

	var platforms []Platform
	if err := json.Unmarshal([]byte(i.SupportedPlatforms), &platforms); err != nil {
		return nil, err
	}
	return platforms, nil
}

// SetSupportedPlatforms 设置支持的平台列表
func (i *Ip) SetSupportedPlatforms(platforms []Platform) error {
	data, err := json.Marshal(platforms)
	if err != nil {
		return err
	}
	i.SupportedPlatforms = string(data)
	return nil
}

// GetPlatformStatuses 获取各平台状态
func (i *Ip) GetPlatformStatuses() (map[Platform]*PlatformStatus, error) {
	if i.PlatformStatuses == "" {
		// 初始化默认状态
		return i.initDefaultPlatformStatuses(), nil
	}

	var statuses map[Platform]*PlatformStatus
	if err := json.Unmarshal([]byte(i.PlatformStatuses), &statuses); err != nil {
		return nil, err
	}
	return statuses, nil
}

// SetPlatformStatuses 设置各平台状态
func (i *Ip) SetPlatformStatuses(statuses map[Platform]*PlatformStatus) error {
	data, err := json.Marshal(statuses)
	if err != nil {
		return err
	}
	i.PlatformStatuses = string(data)
	return nil
}

// IsAvailableForPlatform 检查代理是否可用于指定平台
func (i *Ip) IsAvailableForPlatform(platform Platform) bool {
	if !i.IsGlobalActive {
		return false
	}

	// 检查是否支持该平台
	supportedPlatforms, err := i.GetSupportedPlatforms()
	if err != nil {
		return false
	}

	supported := false
	for _, p := range supportedPlatforms {
		if p == platform {
			supported = true
			break
		}
	}
	if !supported {
		return false
	}

	// 检查平台状态
	statuses, err := i.GetPlatformStatuses()
	if err != nil {
		return false
	}

	status, exists := statuses[platform]
	if !exists {
		return true // 如果没有状态记录，默认可用
	}

	// 检查是否被封禁
	if !status.BannedUntil.IsZero() && time.Now().Before(status.BannedUntil) {
		return false
	}

	return status.IsActive
}

// MarkPlatformFailed 标记平台失败
func (i *Ip) MarkPlatformFailed(platform Platform, maxFailCount int, banDuration time.Duration) error {
	statuses, err := i.GetPlatformStatuses()
	if err != nil {
		return err
	}

	status, exists := statuses[platform]
	if !exists {
		status = &PlatformStatus{IsActive: true}
		statuses[platform] = status
	}

	status.FailCount++
	status.LastFailed = time.Now()

	// 如果失败次数超过阈值，临时封禁
	if status.FailCount >= maxFailCount {
		status.IsActive = false
		status.BannedUntil = time.Now().Add(banDuration)
	}

	return i.SetPlatformStatuses(statuses)
}

// RecoverPlatformStatus 恢复平台状态
func (i *Ip) RecoverPlatformStatus(platform Platform) error {
	statuses, err := i.GetPlatformStatuses()
	if err != nil {
		return err
	}

	status, exists := statuses[platform]
	if !exists {
		return nil
	}

	status.IsActive = true
	status.FailCount = 0
	status.BannedUntil = time.Time{}

	return i.SetPlatformStatuses(statuses)
}

// getDefaultPlatformsByRegion 根据地区获取默认支持的平台
func (i *Ip) getDefaultPlatformsByRegion() []Platform {
	switch i.Region {
	case ProxyRegionDomestic:
		return []Platform{PlatformBuff}
	case ProxyRegionHongKong:
		return []Platform{PlatformBuff, PlatformSteam}
	case ProxyRegionOverseas:
		return []Platform{PlatformSteam}
	default:
		return []Platform{}
	}
}

// initDefaultPlatformStatuses 初始化默认平台状态
func (i *Ip) initDefaultPlatformStatuses() map[Platform]*PlatformStatus {
	statuses := make(map[Platform]*PlatformStatus)
	platforms := i.getDefaultPlatformsByRegion()

	for _, platform := range platforms {
		statuses[platform] = &PlatformStatus{
			IsActive:    true,
			FailCount:   0,
			LastFailed:  time.Time{},
			BannedUntil: time.Time{},
		}
	}

	return statuses
}

// GetProxiesForPlatform 获取指定平台的可用代理
func (i *Ip) GetProxiesForPlatform(db *gorm.DB, platform Platform) ([]*Ip, error) {
	var ips []*Ip

	// 根据平台获取对应地区的代理
	var regions []ProxyRegion
	switch platform {
	case PlatformBuff:
		regions = []ProxyRegion{ProxyRegionDomestic, ProxyRegionHongKong}
	case PlatformSteam:
		regions = []ProxyRegion{ProxyRegionHongKong, ProxyRegionOverseas}
	default:
		// 对于未知平台，返回所有地区的代理
		regions = []ProxyRegion{ProxyRegionDomestic, ProxyRegionHongKong, ProxyRegionOverseas}
	}

	err := db.Where("is_global_active = ? AND region IN ?", true, regions).Find(&ips).Error
	if err != nil {
		return nil, err
	}

	// 进一步过滤，只返回真正可用于该平台的代理
	var availableIps []*Ip
	for _, ip := range ips {
		if ip.IsAvailableForPlatform(platform) {
			availableIps = append(availableIps, ip)
		}
	}

	return availableIps, nil
}

// GetProxiesByRegion 根据地区获取代理
func (i *Ip) GetProxiesByRegion(db *gorm.DB, region ProxyRegion) ([]*Ip, error) {
	var ips []*Ip
	err := db.Where("is_global_active = ? AND region = ?", true, region).Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// UpdatePlatformStatus 更新代理的平台状态
func (i *Ip) UpdatePlatformStatus(db *gorm.DB) error {
	return db.Model(i).Select("platform_statuses").Updates(i).Error
}
