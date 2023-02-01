package dao

import "buff-go/internal/model"

// GetOneBuffUser 查询状态为 0 的账号 只取一个
func (d *Dao) GetOneBuffUser() (buffUser model.BuffUser, err error) {
	return buffUser.GetOneBuffUser(d.engine), nil
}

// 更改账号的状态
func (d *Dao) UpdateBuffUserStatus(id int, status int) (err error) {
	return model.UpdateBuffUserStatus(d.engine, id, status)
}
