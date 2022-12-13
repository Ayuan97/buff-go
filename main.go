package main

import (
	"buff-go/global"
	"fmt"
	"github.com/gin-gonic/gin"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	gin.SetMode(global.ServerSetting.RunMode)
	bot, err := tgbotapi.NewBotAPI("5972902393:AAEWNlCSZ0YUqRHcNfHA9nu4jtxPqEeqNb0")
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true
	log.Printf("Authorized on account %s", bot.Self.UserName)
	//tgbotapi.NewMessage(-842545535, "开始推送")
	//创建html消息模板
	str := "%s" + "\n\r" +
		"<b>%s</b>" + "\n\r" +
		"<u>buff求购:%s</u>" + "\n\r" +
		"<u>steam出售:%s</u>" + "\n\r" +
		"<strong>比例:%s</strong>" + "\n\r" +
		"<a href='%s'>buff链接</a>" + "\n\r" +
		"<a href='%s'>stema链接</a>" + "\n\r"
	//替换模板中的变量
	text := fmt.Sprintf(str, "2022/12/11 23:16:21", "2017年克拉科夫锦标赛挑战组亲笔签名胶囊", "1200", "1300", "0.98", "https://buff.163.com/goods/35097?from=market#tab=buying", "https://steamcommunity.com/market/listings/730/Krakow%202017%20Challengers%20Autograph%20Capsule")
	msg := tgbotapi.NewMessage(-842545535, text)
	msg.ParseMode = "HTML"

	bot.Send(msg)

	//u := tgbotapi.NewUpdate(0)
	//u.Timeout = 60
	//updates := bot.GetUpdatesChan(u)
	//for update := range updates {
	//
	//	if update.Message != nil { // If we got a message
	//		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
	//
	//		msg := tgbotapi.NewMessage(update.Message.Chat.ID, "目前还没有对接回复功能")
	//		msg.ReplyToMessageID = update.Message.MessageID
	//		//新建菜单消息
	//		msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(
	//			tgbotapi.NewKeyboardButtonRow(
	//				tgbotapi.NewKeyboardButton("开启推送"),
	//				tgbotapi.NewKeyboardButton("关闭推送"),
	//			),
	//		)
	//
	//
	//		bot.Send(msg)
	//	}
	//}

}
