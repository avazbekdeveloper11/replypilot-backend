package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-ID"

// RequestID assigns a request id (reusing an inbound X-Request-ID if the
// client/load balancer already set one) and stores it in the Gin context
// so Logging and response.Error can attach it — the single value that lets
// you correlate a client-reported error with a specific server log line.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set("request_id", id)
		c.Writer.Header().Set(RequestIDHeader, id)
		c.Next()
	}
}
