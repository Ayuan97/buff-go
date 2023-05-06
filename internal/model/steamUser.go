package model

import "gorm.io/gorm"

type SteamUser struct {
	*Model
	SessionId        string `json:"sessionid"`
	Account          string `json:"account"`
	Password         string `json:"password"`
	Status           int    `json:"status"`
	Type             int    `json:"type"`
	SteamCountry     string `json:"steam_country"`
	BrowserId        string `json:"browser_id"`
	SteamLoginSecure string `json:"steam_login_secure"`
}

// 查询所有账号
func (a SteamUser) GetSteamUserList(db *gorm.DB) ([]*SteamUser, error) {
	var steamUserList []*SteamUser
	err := db.Find(&steamUserList).Error
	if err != nil {
		return steamUserList, err
	}
	return steamUserList, err
}

// 查询状态为 0 的账号 只取一个
func (a SteamUser) GetOneSteamUser(db *gorm.DB, UserType int) (SteamUser, error) {
	var steamUser SteamUser
	err := db.Where("status = ? and type = ?", 0, UserType).First(&steamUser).Error
	if err != nil {
		return steamUser, err
	}
	return steamUser, err
}

// 更改账号的状态
func UpdateSteamUserStatus(db *gorm.DB, id int, status int) error {
	return db.Model(&SteamUser{}).Where("id = ?", id).Update("status", status).Error
}

// 更新账号信息
func UpdateSteamUserInfo(db *gorm.DB, id int, steamUser SteamUser) error {
	return db.Model(&SteamUser{}).Where("id = ?", id).Updates(steamUser).Error
}

// 根据id查询账号信息
func GetSteamUserInfo(db *gorm.DB, id int) (SteamUser, error) {
	var steamUser SteamUser
	err := db.Where("id = ?", id).First(&steamUser).Error
	if err != nil {
		return steamUser, err
	}
	return steamUser, err
}
