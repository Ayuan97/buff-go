package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"buff-go/global"
	"buff-go/pkg/setting"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"gopkg.in/resty.v1"
)

type ZincLogIndex struct {
	Index map[string]string `json:"index"`
}

type ZincLogData struct {
	Time    time.Time     `json:"time"`
	Level   logrus.Level  `json:"level"`
	Message string        `json:"message"`
	Data    logrus.Fields `json:"data"`
}

type ZincLogHook struct {
	Fired bool
}

func (hook *ZincLogHook) Fire(entry *logrus.Entry) error {
	index := &ZincLogIndex{
		Index: map[string]string{
			"_index": global.LoggerSetting.LogZincIndex,
		},
	}
	indexBytes, _ := json.Marshal(index)

	data := &ZincLogData{
		Time:    entry.Time,
		Level:   entry.Level,
		Message: entry.Message,
		Data:    entry.Data,
	}
	dataBytes, _ := json.Marshal(data)

	logStr := string(indexBytes) + "\n" + string(dataBytes) + "\n"
	client := resty.New()

	if _, err := client.SetDisableWarn(true).R().
		SetHeader("Content-Type", "application/json").
		SetBasicAuth(global.LoggerSetting.LogZincUser, global.LoggerSetting.LogZincPassword).
		SetBody(logStr).
		Post(global.LoggerSetting.LogZincHost); err != nil {
		fmt.Println(err.Error())
	}

	return nil
}

func (hook *ZincLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func New(s *setting.LoggerSettingS) (*logrus.Logger, error) {
	log := logrus.New()
	log.Formatter = &logrus.JSONFormatter{}

	switch s.LogType {
	case setting.LogFileType:
		// 创建文件输出
		fileWriter := &lumberjack.Logger{
			Filename:  s.LogFileSavePath + "/" + s.LogFileName + s.LogFileExt,
			MaxSize:   100,
			MaxAge:    10,
			LocalTime: true,
		}
		// 同时输出到控制台和文件
		log.Out = io.MultiWriter(os.Stdout, fileWriter)
	case setting.LogZincType:
		log.Out = io.Discard
		log.AddHook(&ZincLogHook{})
	default:
		// 默认输出到控制台
		log.Out = os.Stdout
	}

	return log, nil
}
