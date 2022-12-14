package model

import (
	"fmt"
	"gorm.io/gorm"
)

type Ip struct {
	*Model
	Ip      string `json:"data"`
	Type    int    `json:"type"`
	IsHttps string `json:"is_https"`
	Speed   int    `json:"speed"`
	Source  string `json:"source"`
	Port    int    `json:"port"`
}

func (i *Ip) CountIps(db *gorm.DB) int64 {
	var num int64
	err := db.Model(i).Count(&num)
	if err != nil {
		fmt.Println("CountIps err : %v", err)
	}
	return num
}

func (i *Ip) AddIp(db *gorm.DB) error {
	return db.Create(i).Error
}
