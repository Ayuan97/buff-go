package service

import (
	"buff-go/pkg/util"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"net/url"
	"time"
)

func send(data map[string]string) {
	bot, err := tgbotapi.NewBotAPI("5972902393:AAEWNlCSZ0YUqRHcNfHA9nu4jtxPqEeqNb0")
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = true
	//log.Printf("Authorized on account %s", bot.Self.UserName)
	//tgbotapi.NewMessage(-842545535, "开始推送")

	if data["change_type"] == "1" || data["change_type"] == "4" {
		str := "%s" + "\n\r" +
			"<b>%s</b>" + "\n\r" +
			"<u>buff求购:%s</u>" + "                " + "<u>buff出售:%s</u>" + "\n\r" +
			"<u>steam出售:%s</u>" + "                " + "<u>steam求购:%s</u>" + "\n\r" +
			"<strong>比例:%s</strong>" + "\n\r" +
			"<a>%s</a>" + "\n\r" +
			"<a>%s</a>" + "\n\r"

		//所有变量转换为string
		upTime := time.Now().Format("2006-01-02 15:04:05")
		buffPrice := data["buff_buy_price"]
		buffSellPrice := data["buff_sell_price"]
		steamPrice := data["steam_sell_price"]
		steamBuyPrice := data["steam_buy_price"]
		Proportion := data["proportion"]
		//url 编码
		buffUrl := "https://buff.163.com/goods/" + data["buff_goods_id"] + "?from=market#tab=buying"
		steamUrl := "https://steamcommunity.com/market/listings/730/" + url.PathEscape(data["market_hash_name"])

		//替换模板中的变量
		text := fmt.Sprintf(str,
			upTime,
			data["name"],
			buffPrice,
			buffSellPrice,
			steamPrice,
			steamBuyPrice,
			Proportion,
			buffUrl,
			steamUrl,
		)
		msg := tgbotapi.NewMessage(-870095753, text)
		msg.ParseMode = "HTML"
		msg.DisableWebPagePreview = true
		bot.Send(msg)
	} else {
		str := "%s" + "\n\r" +
			"<b>%s</b>" + "\n\r" +
			"<u>buff求购:%s</u>" + "                " + "<u>buff出售:%s</u>" + "\n\r" +
			"<u>steam出售:%s</u>" + "                " + "<u>steam求购:%s  --->(%s)</u>" + "\n\r" +
			"<strong>比例:%s</strong>" + "\n\r" +
			"<a>%s</a>" + "\n\r" +
			"<a>%s</a>" + "\n\r"

		//所有变量转换为string
		upTime := time.Now().Format("2006-01-02 15:04:05")
		buffPrice := data["buff_buy_price"]
		buffSellPrice := data["buff_sell_price"]
		steamPrice := data["steam_sell_price"]
		steamBuyPrice := data["steam_buy_price"]
		Proportion := data["proportion"]
		//url 编码
		buffUrl := "https://buff.163.com/goods/" + data["buff_goods_id"] + "?from=market#tab=buying"
		steamUrl := "https://steamcommunity.com/market/listings/730/" + url.PathEscape(data["market_hash_name"])

		steamBuyPriceFold := fmt.Sprintf("%.2f", util.StringToFloat64(data["steam_buy_price"])*0.85)

		//替换模板中的变量
		text := fmt.Sprintf(str,
			upTime,
			data["name"],
			buffPrice,
			buffSellPrice,
			steamPrice,
			steamBuyPrice,
			steamBuyPriceFold,
			Proportion,
			buffUrl,
			steamUrl,
		)
		msg := tgbotapi.NewMessage(-942510623, text)
		msg.ParseMode = "HTML"
		msg.DisableWebPagePreview = true
		bot.Send(msg)
	}

}

//func sendTelegram() {
//	//当更新时间为0时，不推送 于当前时间相差10分钟时，不推送
//
//	bot, err := tgbotapi.NewBotAPI("5972902393:AAEWNlCSZ0YUqRHcNfHA9nu4jtxPqEeqNb0")
//	if err != nil {
//		log.Panic(err)
//	}
//	bot.Debug = true
//	//log.Printf("Authorized on account %s", bot.Self.UserName)
//	//tgbotapi.NewMessage(-842545535, "开始推送")
//	str := ""
//	if source == 1 {
//		//创建html消息模板
//		str = "%s -buff" + "\n\r" +
//			"<b>%s</b>" + "\n\r" +
//			"<u>buff求购:%s</u>" + "                " + "<u>buff出售:%s</u>" + "\n\r" +
//			"<u>steam出售:%s</u>" + "                " + "<u>steam求购:%s</u>" + "\n\r" +
//			"<strong>比例:%s</strong>" + "\n\r" +
//			"<a>%s</a>" + "\n\r" +
//			"<a>%s</a>" + "\n\r"
//	} else {
//		//创建html消息模板
//		str = "%s -steam" + "\n\r" +
//			"<b>%s</b>" + "\n\r" +
//			"<u>buff求购:%s</u>" + "                " + "<u>buff出售:%s</u>" + "\n\r" +
//			"<u>steam出售:%s</u>" + "                " + "<u>steam求购:%s</u>" + "\n\r" +
//			"<strong>比例:%s</strong>" + "\n\r" +
//			"<a>%s</a>" + "\n\r" +
//			"<a>%s</a>" + "\n\r"
//	}
//
//	//所有变量转换为string
//	upTime := time.Now().Format("2006-01-02 15:04:05")
//	buffPrice := strconv.FormatFloat(info.BuyMaxPrice, 'f', 2, 64)
//	buffSellPrice := strconv.FormatFloat(info.SellMinPrice, 'f', 2, 64)
//	steamPrice := strconv.FormatFloat(info.SteamSellPrice, 'f', 2, 64)
//	steamBuyPrice := strconv.FormatFloat(info.HighestBuyOrder, 'f', 2, 64)
//	Proportion := strconv.FormatFloat(info.Proportion, 'f', 2, 64)
//
//	//url 编码
//	buffUrl := "https://buff.163.com/goods/" + strconv.Itoa(info.GoodsId) + "?from=market#tab=buying"
//	steamUrl := "https://steamcommunity.com/market/listings/730/" + url.PathEscape(info.MarketHashName)
//
//	//替换模板中的变量
//	text := fmt.Sprintf(str,
//		upTime,
//		info.Name,
//		buffPrice,
//		buffSellPrice,
//		steamPrice,
//		steamBuyPrice,
//		Proportion,
//		buffUrl,
//		steamUrl,
//	)
//	msg := tgbotapi.NewMessage(-852932792, text)
//	msg.ParseMode = "HTML"
//	msg.DisableWebPagePreview = true
//
//	bot.Send(msg)
//
//}
