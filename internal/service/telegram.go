package service

import (
	"buff-go/internal/model"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"strconv"
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
		"<a href='%s'>buff链接</a>" + "\n\r" +
		"<a href='%s'>stema链接</a>" + "\n\r"
	//替换模板中的变量
	text := fmt.Sprintf(str,
		info.UpdatedAt,
		info.Name,
		strconv.Itoa(int(info.BuyMaxPrice)),
		strconv.Itoa(int(info.SteamSellPrice)),
		strconv.Itoa(int(info.Proportion)),
		fmt.Sprintf("https://buff.163.com/goods/ %s ?from=market#tab=buying",
			strconv.Itoa(info.GoodsId)),
		info.SteamMarketUrl,
	)
	msg := tgbotapi.NewMessage(-842545535, text)
	msg.ParseMode = "HTML"

	bot.Send(msg)
}
