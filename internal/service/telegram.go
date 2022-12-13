package service

import (
	"buff-go/internal/model"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"net/url"
	"strconv"
	"time"
)

func sendTelegram(info *model.Goods) {
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
		"<b>%s</b>" + "\n\r" +
		"<b>%s</b>" + "\n\r"
	//所有变量转换为string
	upTime := time.Unix(info.UpdatedAt, 0).Format("2006-01-02 15:04:05")
	buffPrice := strconv.FormatFloat(info.BuyMaxPrice, 'f', 2, 64)
	steamPrice := strconv.FormatFloat(info.SteamSellPrice, 'f', 2, 64)
	Proportion := strconv.FormatFloat(info.Proportion, 'f', 2, 64)
	//url 编码
	buffUrl := "https://buff.163.com/goods/" + strconv.Itoa(info.GoodsId) + "?from=market#tab=buying"

	//替换模板中的变量
	text := fmt.Sprintf(str,
		upTime,
		info.Name,
		buffPrice,
		steamPrice,
		Proportion,
		buffUrl,
		url.QueryEscape(info.SteamMarketUrl),
	)
	msg := tgbotapi.NewMessage(-842545535, text)
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = true

	bot.Send(msg)
}
