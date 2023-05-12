package main

import (
	"buff-go/global"
	"buff-go/internal/model"
	"buff-go/internal/service"
	"fmt"
	"github.com/fatih/color"
	"github.com/go-redis/redis/v8"
	"log"

	"buff-go/pkg/logger"
	"buff-go/pkg/setting"
)

func init() {
	fmt.Println("init")
	fmt.Fprintf(color.Output, "%s\n", color.GreenString(fmt.Sprintf("开始初始化....")))

	err := setupSetting()
	if err != nil {
		log.Fatalf("init.setupSetting err: %v", err)
	}
	err = setupLogger()
	if err != nil {
		log.Fatalf("init.setupLogger err: %v", err)
	}
	err = setupDBEngine()
	if err != nil {
		log.Fatalf("init.setupDBEngine err: %v", err)
	}
	service.Initialize(global.DBEngine)
	fmt.Fprintf(color.Output, "%s\n", color.GreenString(fmt.Sprintf("初始化成功....")))

}

func setupSetting() error {
	setting, err := setting.NewSetting()
	if err != nil {
		return err
	}
	err = setting.ReadSection("Server", &global.ServerSetting)
	if err != nil {
		return err
	}
	err = setting.ReadSection("Log", &global.LoggerSetting)
	if err != nil {
		return err
	}
	err = setting.ReadSection("Database", &global.DatabaseSetting)
	if err != nil {
		return err
	}
	err = setting.ReadSection("Redis", &global.RedisSetting)
	if err != nil {
		return err
	}
	return nil
}

func setupLogger() error {
	logger, err := logger.New(global.LoggerSetting)
	if err != nil {
		return err
	}
	global.Logger = logger
	return nil
}

func setupDBEngine() error {
	var err error
	global.DBEngine, err = model.NewDBEngine(global.DatabaseSetting)
	if err != nil {
		return err
	}

	global.Redis = redis.NewClient(&redis.Options{
		Addr:     global.RedisSetting.Host,
		Password: global.RedisSetting.Password,
		DB:       global.RedisSetting.DB,
	})

	return nil
}
