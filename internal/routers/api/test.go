package api

import (
	"buff-go/internal/service"
	"github.com/gin-gonic/gin"
)

func Test(c *gin.Context) {

	service.GetGooDsListV2()
}
