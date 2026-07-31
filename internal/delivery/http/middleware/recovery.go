package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/replypilot/backend/internal/delivery/http/response"
)

// Recovery catches panics so a bug in one request can't take the whole
// process down. A panic here is always a programming error, never expected
// control flow, so it's logged at Error with a stack trace unconditionally
// — this is not the path for expected failures, those go through
// apperror + ErrorHandler below.
func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered", zap.Any("panic", rec), zap.Stack("stack"))
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

// ErrorHandler centralizes error -> HTTP response translation. Handlers
// call c.Error(err) and return without writing a response themselves; this
// is the only place that decides what apperror.Code maps to what HTTP
// status (via response.Error) — so that decision is made once, not
// re-implemented slightly differently in every handler.
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}

		response.Error(c, c.Errors.Last().Err)
	}
}
