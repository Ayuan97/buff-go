package routers

import (
	"buff-go/internal/middleware"
	"buff-go/internal/routers/api"
	"github.com/gin-gonic/gin"
	"net/http"
)

func NewRouter() *gin.Engine {
	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.Use(gin.Logger(), gin.Recovery())

	apiGroup := r.Group("/api")
	apiGroup.Use(middleware.Cors())
	{
		apiGroup.POST("/test", api.Test)
	}

	// 默认404
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "弄啥嘞?",
		})
	})
	// 默认405
	r.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"code": 405,
			"msg":  "怼错了!",
		})
	})
	return r
}
