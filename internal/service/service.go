package service

import (
	"gorm.io/gorm"
	"qingshanyoufeng/internal/dao"
	"qingshanyoufeng/pkg/zinc"
)

var (
	myDao *dao.Dao
)

func Initialize(engine *gorm.DB, client *zinc.ZincClient) {
	myDao = dao.New(engine, client)
}
