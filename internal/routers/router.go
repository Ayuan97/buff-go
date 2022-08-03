package routers

import (
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func NewRouter() *gin.Engine {
	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// 跨域配置
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AddAllowHeaders("Authorization")
	r.Use(cors.New(corsConfig))

	// 获取version
	//r.GET("/", api.Version)

	//// 无鉴权路由组
	//noAuthApi := r.Group("/")
	//{
	//	noAuthApi.GET("/posts", api.GetPostList)
	//}

	//// 鉴权路由组
	//authApi := r.Group("/").Use(middleware.JWT())
	//privApi := r.Group("/").Use(middleware.JWT()).Use(middleware.Priv())
	//{
	//	authApi.GET("/sync/index", api.SyncSearchIndex)
	//	privApi.POST("/attachment", api.UploadAttachment)
	//
	//}
	// 默认404
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  "Not Found",
		})
	})
	// 默认405
	r.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"code": 405,
			"msg":  "Method Not Allowed",
		})
	})
	return r
}
