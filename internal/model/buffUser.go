package model

import "gorm.io/gorm"

type BuffUser struct {
	*Model
	Sessionid  string `json:"sessionid"`
	Account    string `json:"account"`
	Password   string `json:"password"`
	DeviceId   string `json:"device_id"`
	CsrfToken  string `json:"csrf_token"`
	RememberMe string `json:"remember_me"`
	Status     int    `json:"status"`
}

// 查询所有账号
func (a BuffUser) GetBuffUserList(db *gorm.DB) ([]*BuffUser, error) {
	var buffUserList []*BuffUser
	err := db.Find(&buffUserList).Error
	if err != nil {
		return buffUserList, err
	}
	return buffUserList, err
}

// 查询状态为 0 的账号 只取一个
func (a BuffUser) GetOneBuffUser(db *gorm.DB) (BuffUser, error) {
	var buffUser BuffUser
	err := db.Where("status = ?", 0).First(&buffUser).Error
	if err != nil {
		return buffUser, err
	}
	return buffUser, err
}

// 更改账号的状态
func UpdateBuffUserStatus(db *gorm.DB, id int, status int) error {
	return db.Model(&BuffUser{}).Where("id = ?", id).Update("status", status).Error
}
