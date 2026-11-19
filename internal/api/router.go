package api

import (
	"fmt"
	"github.com/jaysqvl/Jotist/internal/auth"
	"github.com/jaysqvl/Jotist/pkg/logger"
	"github.com/jaysqvl/Jotist/pkg/middleware"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SetupRoutes sets up all API routes
func SetupRoutes(handler *Handler, authService *auth.AuthService) *gin.Engine {
	// Suppress all GIN debug output
	gin.SetMode(gin.ReleaseMode)
	logger.SetGinOutput()

	// Create Gin router without default middleware
	router := gin.New()
	trustedProxies := []string(nil)
	if handler != nil && handler.config != nil && len(handler.config.TrustedProxies) > 0 {
		trustedProxies = handler.config.TrustedProxies
	}
	if err := router.SetTrustedProxies(trustedProxies); err != nil {
		logger.Warn("Invalid trusted proxy configuration, disabling trusted proxies", "error", err)
		_ = router.SetTrustedProxies(nil)
	}

	// Add recovery middleware
	router.Use(gin.Recovery())

	// Add custom logger middleware
	router.Use(logger.GinLogger())
	router.Use(func(c *gin.Context) {
		c.Header("Content-Security-Policy", "frame-ancestors 'none'")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Next()
	})

	// Add compression middleware first for maximum benefit
	router.Use(middleware.CompressionMiddleware())

	// Add CORS middleware (uses config from handler)
	router.Use(func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// Determine allowed origin based on config
		allowOrigin := "*"
		if handler.config.IsProduction() && len(handler.config.AllowedOrigins) > 0 {
			// In production, validate against configured origins
			allowOrigin = ""
			for _, allowed := range handler.config.AllowedOrigins {
				if origin == allowed {
					allowOrigin = origin
					break
				}
			}
		} else if origin != "" {
			// In development, echo back the origin for credentials support
			allowOrigin = origin
		}

		if allowOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowOrigin)
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Content-Range, Accept-Encoding, X-CSRF-Token, Authorization, X-API-Key, X-Upload-Token, X-Chunk-SHA256")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	spec := registerRoutes(router, handler, authService)
	document, err := marshalOpenAPI(spec)
	if err != nil {
		panic(fmt.Errorf("marshal API specification: %w", err))
	}
	router.GET("/api/v1/docs/spec", privateNoStore, middleware.JWTOnlyMiddleware(authService), func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json; charset=utf-8", document)
	})
	return router
}
