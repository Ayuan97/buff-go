package api

import (
	"buff-go/internal/config"
	"buff-go/internal/model"
	"buff-go/internal/service"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ConfigHandler 配置处理器
type ConfigHandler struct {
	configManager config.SystemConfigManager
}

// NewConfigHandler 创建配置处理器
func NewConfigHandler() *ConfigHandler {
	return &ConfigHandler{
		configManager: config.GetConfigManager(),
	}
}

// GetConfig 获取配置
func (h *ConfigHandler) GetConfig(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的配置ID",
		})
		return
	}

	config := h.configManager.GetConfig(id)
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": config,
		"msg":  "获取配置成功",
	})
}

// UpdateConfig 更新配置
func (h *ConfigHandler) UpdateConfig(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的配置ID",
		})
		return
	}

	var config model.Config
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "请求参数错误: " + err.Error(),
		})
		return
	}

	// 验证配置有效性
	if err := service.ValidateConfig(config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "配置验证失败: " + err.Error(),
		})
		return
	}

	// 设置配置ID
	config.ID = id

	// 更新配置
	if err := h.configManager.UpdateConfig(id, &config); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "更新配置失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": config,
		"msg":  "配置更新成功",
	})
}

// GetCurrentConfig 获取当前游戏配置
func (h *ConfigHandler) GetCurrentConfig(c *gin.Context) {
	config, game, err := service.GetCurrentGameConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "获取当前配置失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": gin.H{
			"config": config,
			"game":   game,
		},
		"msg": "获取当前配置成功",
	})
}

// RefreshConfig 刷新配置缓存
func (h *ConfigHandler) RefreshConfig(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的配置ID",
		})
		return
	}

	h.configManager.RefreshConfig(id)

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "配置缓存刷新成功",
	})
}

// TestConfigChange 测试配置变更通知
func (h *ConfigHandler) TestConfigChange(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的配置ID",
		})
		return
	}

	// 模拟配置变更通知
	message := map[string]interface{}{
		"config_id": id,
		"timestamp": fmt.Sprintf("%d", service.GetCurrentTimestamp()),
		"action":    "test",
	}

	messageBytes, _ := json.Marshal(message)
	channel := fmt.Sprintf("config_change:%d", id)

	// 发布测试消息
	err = service.PublishMessage(channel, string(messageBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "发布测试消息失败: " + err.Error(),
		})
		return
	}

	// 同时发布到全局频道
	service.PublishMessage("config_change:all", string(messageBytes))

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": gin.H{
			"channel": channel,
			"message": message,
		},
		"msg": "配置变更测试消息发送成功",
	})
}

// GetAllConfigs 获取所有配置
func (h *ConfigHandler) GetAllConfigs(c *gin.Context) {
	// 使用新的方法获取所有配置
	allConfigs := service.GetAllGameConfigs()

	// 获取当前活跃配置
	currentConfig, currentGame, _ := service.GetCurrentGameConfig()

	result := gin.H{
		"configs": allConfigs,
		"current": gin.H{
			"config": currentConfig,
			"game":   currentGame,
		},
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": result,
		"msg":  "获取所有配置成功",
	})
}

// GetConfigByGameName 根据游戏名称获取配置
func (h *ConfigHandler) GetConfigByGameName(c *gin.Context) {
	gameName := c.Param("game")
	if gameName != "csgo" && gameName != "dota2" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "无效的游戏名称，支持: csgo, dota2",
		})
		return
	}

	config := service.GetConfigByGameName(gameName)
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": config,
		"msg":  fmt.Sprintf("获取%s配置成功", gameName),
	})
}

// RegisterConfigRoutes 注册配置相关路由
func RegisterConfigRoutes(r *gin.Engine) {
	handler := NewConfigHandler()

	configGroup := r.Group("/api/config")
	{
		configGroup.GET("/:id", handler.GetConfig)                  // 获取指定配置
		configGroup.PUT("/:id", handler.UpdateConfig)               // 更新指定配置
		configGroup.POST("/:id/refresh", handler.RefreshConfig)     // 刷新配置缓存
		configGroup.POST("/:id/test", handler.TestConfigChange)     // 测试配置变更
		configGroup.GET("/current", handler.GetCurrentConfig)       // 获取当前配置
		configGroup.GET("/all", handler.GetAllConfigs)              // 获取所有配置
		configGroup.GET("/game/:game", handler.GetConfigByGameName) // 根据游戏名称获取配置
	}
}
