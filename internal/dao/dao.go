package dao

import (
	"gorm.io/gorm"
	"paopao-ce/pkg/zinc"
)

type Dao struct {
	engine *gorm.DB
	zinc   *zinc.ZincClient
}

func New(engine *gorm.DB, zinc *zinc.ZincClient) *Dao {
	return &Dao{
		engine: engine,
		zinc:   zinc,
	}
}
