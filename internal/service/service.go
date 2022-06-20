package service

import (
	"gorm.io/gorm"
	"paopao-ce/internal/dao"
	"paopao-ce/pkg/zinc"
)

var (
	myDao *dao.Dao
)

func Initialize(engine *gorm.DB, client *zinc.ZincClient) {
	myDao = dao.New(engine, client)
}
