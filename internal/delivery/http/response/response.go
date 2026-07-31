// Package response defines the single JSON envelope every endpoint returns
// — {"data": ...} on success, {"error": {...}} on failure — and the one
// place that maps an apperror.Code to an HTTP status.
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/domain/apperror"
)

type Envelope struct {
	Data  any        `json:"data,omitempty"`
	Error *ErrorBody `json:"error,omitempty"`
	Meta  any        `json:"meta,omitempty"`
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{Data: data})
}

func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Envelope{Data: data})
}

func OKWithMeta(c *gin.Context, data, meta any) {
	c.JSON(http.StatusOK, Envelope{Data: data, Meta: meta})
}

// Error writes the error envelope. It is normally called by the
// centralized ErrorHandler middleware (internal/delivery/http/middleware),
// not directly by handlers — handlers call c.Error(err) and return.
func Error(c *gin.Context, err error) {
	code, status, message := classify(err)
	c.JSON(status, Envelope{
		Error: &ErrorBody{
			Code:      string(code),
			Message:   message,
			RequestID: requestID(c),
		},
	})
}

func requestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// classify maps an apperror.Code to an HTTP status and a client-safe
// message. Internal errors deliberately do NOT expose err.Error() to the
// client — the wrapped detail (a SQL error, a stack trace) goes to the log
// via the Recovery/Logging middleware, not the response body.
func classify(err error) (apperror.Code, int, string) {
	ae, ok := apperror.As(err)
	if !ok {
		return apperror.CodeInternal, http.StatusInternalServerError, "internal server error"
	}

	switch ae.Code {
	case apperror.CodeNotFound:
		return ae.Code, http.StatusNotFound, ae.Message
	case apperror.CodeInvalidInput:
		return ae.Code, http.StatusBadRequest, ae.Message
	case apperror.CodeUnauthorized:
		return ae.Code, http.StatusUnauthorized, ae.Message
	case apperror.CodeForbidden:
		return ae.Code, http.StatusForbidden, ae.Message
	case apperror.CodeConflict:
		return ae.Code, http.StatusConflict, ae.Message
	case apperror.CodeRateLimited:
		return ae.Code, http.StatusTooManyRequests, ae.Message
	default:
		return apperror.CodeInternal, http.StatusInternalServerError, "internal server error"
	}
}
