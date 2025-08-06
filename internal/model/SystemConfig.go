package model

import (
	"gorm.io/gorm"
)

type Config struct {
	*Model
	GameName           string  `json:"game_name" gorm:"column:game_name"`
	Status             int     `json:"status" gorm:"column:status"` //1启用 0禁用
	AppId              string  `json:"app_id" gorm:"column:app_id"`
	BuffBuyStatus      int     `json:"buff_buy_status"`
	BuffSellStatus     int     `json:"buff_sell_status"`
	SteamBuyStatus     int     `json:"steam_buy_status"`
	SteamSellStatus    int     `json:"steam_sell_status"`
	BuffBuyDelay       int     `json:"buff_buy_delay"`
	BuffSellDelay      int     `json:"buff_sell_delay"`
	SteamBuyDelay      int     `json:"steam_buy_delay"`
	SteamSellDelay     int     `json:"steam_sell_delay"`
	BotFilter          string  `json:"bot_filter"`
	BuffPageNum        int     `json:"buff_page_num"`
	SteamPageNum       int     `json:"steam_page_num"`
	BotBuffProportion  float64 `json:"bot_buff_proportion"`
	BotSteamProportion float64 `json:"bot_steam_proportion"`
	BotPrice           float64 `json:"bot_price"`
	MinPrice           float64 `json:"min_price"`
	MaxPrice           float64 `json:"max_price"`
}

// 表名
func (Config) TableName() string {
	return "system_config"
}

// 根据id  获取单个配置
func (s *Config) GetConfigOne(db *gorm.DB) (*Config, error) {
	var SystemConfig Config
	//使用First方法，如果找不到记录会返回错误
	err := db.Where("id = ?", s.ID).Find(&SystemConfig).Error
	if err != nil {
		return nil, err
	}
	return &SystemConfig, nil
}
