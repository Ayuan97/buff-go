package service

import (
	"buff-go/internal/dao"
	"gorm.io/gorm"
)

var (
	myDao *dao.Dao
)

func Initialize(engine *gorm.DB) {
	myDao = dao.New(engine)
}
