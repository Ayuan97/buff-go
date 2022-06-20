package global

import (
	"sync"

	"github.com/sirupsen/logrus"
	"paopao-ce/pkg/setting"
)

var (
	ServerSetting   *setting.ServerSettingS
	AppSetting      *setting.AppSettingS
	RuntimeSetting  *setting.RuntimeSettingS
	DatabaseSetting *setting.DatabaseSettingS
	RedisSetting    *setting.RedisSettingS
	SearchSetting   *setting.SearchSettingS
	AliossSetting   *setting.AliossSettingS
	JWTSetting      *setting.JWTSettingS
	LoggerSetting   *setting.LoggerSettingS
	Logger          *logrus.Logger
	Mutex           *sync.Mutex
)
