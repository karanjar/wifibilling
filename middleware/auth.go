package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// AdminAuth validates an admin JWT from the Authorization: Bearer header.
func AdminAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
			return []byte(secret), nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !tok.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Next()
	}
}

// GatewayAuth protects endpoints used by the router/hotspot controller.
func GatewayAuth(key string) gin.HandlerFunc {
	return func(c *gin.Context) {
		got := c.GetHeader("X-API-Key")
		if subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}
		c.Next()
	}
}

func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*") // tighten to your portal origin in production
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
