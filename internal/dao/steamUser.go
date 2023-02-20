package dao

import "buff-go/internal/model"

// GetOneSteamUser 查询状态为 0 的账号 只取一个
func (d *Dao) GetOneSteamUser(UserType int) (steamUser model.SteamUser, err error) {
	return steamUser.GetOneSteamUser(d.engine, UserType)
}

// 更改账号的状态
func (d *Dao) UpdateSteamUserStatus(id int, status int) (err error) {
	return model.UpdateSteamUserStatus(d.engine, id, status)
}

// 查询所有账号
func (d *Dao) GetSteamUserList() (steamUserList []*model.SteamUser, err error) {
	var steamUser model.SteamUser
	return steamUser.GetSteamUserList(d.engine)
}
