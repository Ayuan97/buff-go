package global

import (
	"buff-go/pkg/setting"
	"github.com/sirupsen/logrus"
	"sync"
)

var (
	ServerSetting   *setting.ServerSettingS
	DatabaseSetting *setting.DatabaseSettingS
	RedisSetting    *setting.RedisSettingS
	LoggerSetting   *setting.LoggerSettingS
	Logger          *logrus.Logger
	Mutex           *sync.Mutex
)
