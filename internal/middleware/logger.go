// Package middleware provides Gin middleware for JX Horizon.
package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// RequestID generates a UUID per incoming request, stores it in the Gin context,
// and sets it as the X-Request-ID response header.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Honour any upstream-provided request ID (e.g. from a load balancer).
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// StructuredLogger logs each HTTP request as a single JSON line using zerolog.
// It records: timestamp, method, path, status_code, latency_ms, user_id, request_id.
func StructuredLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		latencyMs := time.Since(start).Milliseconds()
		status := c.Writer.Status()
		requestID, _ := c.Get("request_id")
		userIDRaw, _ := c.Get("user_id")

		userIDStr := ""
		if id, ok := userIDRaw.(uuid.UUID); ok {
			userIDStr = id.String()
		}

		event := log.Info()
		if status >= 500 {
			event = log.Error()
		} else if status >= 400 {
			event = log.Warn()
		}

		event.
			Str("request_id", func() string {
				if s, ok := requestID.(string); ok {
					return s
				}
				return ""
			}()).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status_code", status).
			Int64("latency_ms", latencyMs).
			Str("user_id", userIDStr).
			Str("client_ip", c.ClientIP()).
			Msg("request")
	}
}

// TenantGuard extracts the authenticated user_id UUID from the session (set by
// AuthRequired), validates it is non-nil and non-zero, and re-sets it in the
// context so downstream handlers can use ctx.MustGet("user_id").(uuid.UUID).
// Returns 401 Unauthorized if the user_id is missing or invalid.
func TenantGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, exists := c.Get("user_id")
		if !exists {
			c.AbortWithStatusJSON(401, gin.H{"error": "Unauthorized: missing user identity"})
			return
		}

		id, ok := raw.(uuid.UUID)
		if !ok || id == uuid.Nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "Unauthorized: invalid user identity"})
			return
		}

		// Re-set as canonical uuid.UUID in case upstream set it as a string.
		c.Set("user_id", id)
		c.Next()
	}
}
