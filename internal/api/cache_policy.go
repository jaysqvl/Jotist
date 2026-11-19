package api

import "github.com/gin-gonic/gin"

func privateNoStore(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Next()
}
