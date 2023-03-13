package model

import (
	"fmt"
	"gorm.io/gorm"
)

type Ip struct {
	*Model
	Ip        string `json:"data"`
	Type      int    `json:"type"`
	Name      string `json:"name"`
	PassWord  string `json:"pass_word"`
	ProxyType int    `json:"proxy_type"`
	Country   int    `json:"country"`
	IsHttps   string `json:"is_https"`
	Speed     int    `json:"speed"`
	Source    string `json:"source"`
	Port      int    `json:"port"`
}

func (i *Ip) CountIps(db *gorm.DB) int64 {
	var num int64
	err := db.Model(&i).Where("id > 0 ").Count(&num).Error
	if err != nil {
		fmt.Println("CountIps err :", err)
		return 0
	}
	return num
}

func (i *Ip) AddIp(db *gorm.DB) error {
	return db.Create(i).Error
}

func (i *Ip) DeleteIp(db *gorm.DB) error {
	return db.Delete(i).Error
}

func (i *Ip) GetAllIp(db *gorm.DB) ([]*Ip, error) {
	var ips []*Ip
	err := db.Where("type = 1").Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// 获取一个代理
func (i *Ip) GetIps(db *gorm.DB) (*Ip, error) {
	var ips *Ip
	err := db.Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// 随机获取一个代理
func (i *Ip) GetRandomIps(db *gorm.DB) (*Ip, error) {
	var ips *Ip
	err := db.Order("rand()").Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// 获取一个私有代理
func (i *Ip) GetOneIp(db *gorm.DB, country int) (*Ip, error) {
	var ips *Ip
	err := db.Where("type = 2 and country = ?", country).Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// 获取所有私有代理
func (i *Ip) GetAllPrivateIp(db *gorm.DB) ([]*Ip, error) {
	var ips []*Ip
	err := db.Where("type = 2").Find(&ips).Error
	if err != nil {
		return nil, err
	}
	return ips, nil
}
