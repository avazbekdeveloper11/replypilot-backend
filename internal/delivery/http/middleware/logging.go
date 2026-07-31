package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logging emits one structured line per request. It runs after the handler
// (via c.Next()) so it can report the final status and total latency,
// including whatever RateLimiter/Auth/ErrorHandler added along the way.
func Logging(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
		}
		if v, ok := c.Get("request_id"); ok {
			fields = append(fields, zap.Any("request_id", v))
		}

		if len(c.Errors) > 0 {
			logger.Error("request completed with errors", append(fields, zap.String("errors", c.Errors.String()))...)
			return
		}

		logger.Info("request completed", fields...)
	}
}
