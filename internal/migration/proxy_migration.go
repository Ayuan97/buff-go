package migration

import (
	"buff-go/internal/model"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
)

// MigrateProxyTable 迁移代理表，添加新的平台相关字段
func MigrateProxyTable(db *gorm.DB) error {
	// 自动迁移表结构
	if err := db.AutoMigrate(&model.Ip{}); err != nil {
		return fmt.Errorf("failed to migrate proxy table: %v", err)
	}

	// 更新现有记录的默认值
	if err := updateExistingProxyRecords(db); err != nil {
		return fmt.Errorf("failed to update existing proxy records: %v", err)
	}

	return nil
}

// updateExistingProxyRecords 更新现有代理记录的默认值
func updateExistingProxyRecords(db *gorm.DB) error {
	var ips []*model.Ip

	// 获取所有需要更新的代理记录
	if err := db.Where("region = 0 OR supported_platforms = '' OR platform_statuses = ''").Find(&ips).Error; err != nil {
		return err
	}

	for _, ip := range ips {
		updated := false

		// 设置默认地区
		if ip.Region == 0 {
			ip.Region = determineRegionByCountry(ip.Country)
			updated = true
		}

		// 设置默认支持的平台
		if ip.SupportedPlatforms == "" {
			platforms := getDefaultPlatformsByRegion(ip.Region)
			if data, err := json.Marshal(platforms); err == nil {
				ip.SupportedPlatforms = string(data)
				updated = true
			}
		}

		// 初始化平台状态
		if ip.PlatformStatuses == "" {
			statuses := initDefaultPlatformStatuses(ip.Region)
			if data, err := json.Marshal(statuses); err == nil {
				ip.PlatformStatuses = string(data)
				updated = true
			}
		}

		// 设置全局激活状态
		if !ip.IsGlobalActive {
			ip.IsGlobalActive = true
			updated = true
		}

		// 保存更新
		if updated {
			if err := db.Save(ip).Error; err != nil {
				return fmt.Errorf("failed to update proxy %d: %v", ip.ID, err)
			}
		}
	}

	return nil
}

// determineRegionByCountry 根据国家字段确定地区
func determineRegionByCountry(country int) model.ProxyRegion {
	switch country {
	case 1: // 中国
		return model.ProxyRegionDomestic
	case 2: // 香港
		return model.ProxyRegionHongKong
	case 3: // 海外
		return model.ProxyRegionOverseas
	default:
		return model.ProxyRegionDomestic
	}
}

// getDefaultPlatformsByRegion 根据地区获取默认支持的平台
func getDefaultPlatformsByRegion(region model.ProxyRegion) []model.Platform {
	switch region {
	case model.ProxyRegionDomestic:
		return []model.Platform{model.PlatformBuff}
	case model.ProxyRegionHongKong:
		return []model.Platform{model.PlatformBuff, model.PlatformSteam}
	case model.ProxyRegionOverseas:
		return []model.Platform{model.PlatformSteam}
	default:
		return []model.Platform{}
	}
}

// initDefaultPlatformStatuses 初始化默认平台状态
func initDefaultPlatformStatuses(region model.ProxyRegion) map[model.Platform]*model.PlatformStatus {
	statuses := make(map[model.Platform]*model.PlatformStatus)
	platforms := getDefaultPlatformsByRegion(region)

	for _, platform := range platforms {
		statuses[platform] = &model.PlatformStatus{
			IsActive:  true,
			FailCount: 0,
		}
	}

	return statuses
}

// CreateIndexes 创建必要的索引
func CreateIndexes(db *gorm.DB) error {
	// 为region字段创建索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_ips_region ON ips(region)").Error; err != nil {
		return fmt.Errorf("failed to create region index: %v", err)
	}

	// 为is_global_active字段创建索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_ips_global_active ON ips(is_global_active)").Error; err != nil {
		return fmt.Errorf("failed to create global_active index: %v", err)
	}

	// 为region和is_global_active的组合索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_ips_region_active ON ips(region, is_global_active)").Error; err != nil {
		return fmt.Errorf("failed to create region_active index: %v", err)
	}

	return nil
}

// RollbackMigration 回滚迁移（如果需要）
func RollbackMigration(db *gorm.DB) error {
	// 删除新添加的字段（注意：这会丢失数据）
	if err := db.Exec("ALTER TABLE ips DROP COLUMN IF EXISTS region").Error; err != nil {
		return fmt.Errorf("failed to drop region column: %v", err)
	}

	if err := db.Exec("ALTER TABLE ips DROP COLUMN IF EXISTS supported_platforms").Error; err != nil {
		return fmt.Errorf("failed to drop supported_platforms column: %v", err)
	}

	if err := db.Exec("ALTER TABLE ips DROP COLUMN IF EXISTS platform_statuses").Error; err != nil {
		return fmt.Errorf("failed to drop platform_statuses column: %v", err)
	}

	if err := db.Exec("ALTER TABLE ips DROP COLUMN IF EXISTS is_global_active").Error; err != nil {
		return fmt.Errorf("failed to drop is_global_active column: %v", err)
	}

	return nil
}

// ValidateMigration 验证迁移是否成功
func ValidateMigration(db *gorm.DB) error {
	var count int64

	// 检查是否有记录没有正确设置region
	if err := db.Model(&model.Ip{}).Where("region = 0").Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("found %d records with invalid region", count)
	}

	// 检查是否有记录没有设置supported_platforms
	if err := db.Model(&model.Ip{}).Where("supported_platforms = '' OR supported_platforms IS NULL").Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("found %d records without supported_platforms", count)
	}

	// 检查是否有记录没有设置platform_statuses
	if err := db.Model(&model.Ip{}).Where("platform_statuses = '' OR platform_statuses IS NULL").Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("found %d records without platform_statuses", count)
	}

	return nil
}
