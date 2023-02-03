package model

import "gorm.io/gorm"

type SteamUser struct {
	*Model
	SessionId        string `json:"sessionid"`
	Account          string `json:"account"`
	Password         string `json:"password"`
	Status           int    `json:"status"`
	SteamCountry     string `json:"steam_country"`
	BrowserId        string `json:"browser_id"`
	SteamLoginSecure string `json:"steam_login_secure"`
}

// 查询状态为 0 的账号 只取一个
func (a SteamUser) GetOneSteamUser(db *gorm.DB) (SteamUser, error) {
	var steamUser SteamUser
	err := db.Where("status = ?", 0).First(&steamUser).Error
	if err != nil {
		return steamUser, err
	}
	return steamUser, err
}

// 更改账号的状态
func UpdateSteamUserStatus(db *gorm.DB, id int, status int) error {
	return db.Model(&SteamUser{}).Where("id = ?", id).Update("status", status).Error
}
