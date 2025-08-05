package main

import (
	"buff-go/internal/dao"
	"buff-go/internal/scraper_new/components"
	"buff-go/internal/scraper_new/core/factory"
	"buff-go/internal/scraper_new/interfaces"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
)

var (
	scraperTypes = flag.String("scrapers", "buff_buy", "Comma-separated list of scrapers to run (buff_buy,buff_sell,steam_buy,steam_sell)")
	configFile   = flag.String("config", "", "Path to config file")
)

func main() {
	flag.Parse()

	fmt.Fprintf(color.Output, "%s\n", color.GreenString("=== 新架构数据抓取系统启动 ==="))

	// 简化初始化，创建一个基本的DAO（可以为nil用于演示）
	var dao *dao.Dao = nil

	// 创建默认管理器
	httpManager, proxyManager, configManager, cacheManager, errorHandler, taskManager := components.CreateDefaultManagers()

	// 创建抓取器工厂
	scraperFactory := factory.NewScraperFactory(dao)
	scraperFactory.SetManagers(httpManager, proxyManager, configManager, cacheManager, errorHandler, taskManager)

	// 创建抓取器管理器
	scraperManager := factory.NewScraperManager(scraperFactory)

	// 解析要启动的抓取器类型
	types := parseScraperTypes(*scraperTypes)

	// 启动抓取器
	for _, scraperType := range types {
		fmt.Printf("启动抓取器: %s\n", scraperType)
		if err := scraperManager.StartScraper(scraperType); err != nil {
			log.Printf("启动抓取器 %s 失败: %v", scraperType, err)
		} else {
			fmt.Fprintf(color.Output, "%s\n", color.GreenString(fmt.Sprintf("抓取器 %s 启动成功", scraperType)))
		}
	}

	// 等待信号
	waitForSignal(scraperManager)

	fmt.Fprintf(color.Output, "%s\n", color.YellowString("正在关闭抓取器..."))

	// 停止所有抓取器
	if err := scraperManager.StopAllScrapers(); err != nil {
		log.Printf("停止抓取器失败: %v", err)
	}

	fmt.Fprintf(color.Output, "%s\n", color.GreenString("抓取器已安全关闭"))
}

// parseScraperTypes 解析抓取器类型
func parseScraperTypes(typesStr string) []interfaces.ScraperType {
	var types []interfaces.ScraperType

	parts := strings.Split(typesStr, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			types = append(types, interfaces.ScraperType(part))
		}
	}

	return types
}

// waitForSignal 等待信号
func waitForSignal(scraperManager *factory.ScraperManager) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 启动状态监控
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				status := scraperManager.GetAllScraperStatus()
				fmt.Printf("抓取器状态: %+v\n", status)
			case <-sigChan:
				return
			}
		}
	}()

	<-sigChan
}
