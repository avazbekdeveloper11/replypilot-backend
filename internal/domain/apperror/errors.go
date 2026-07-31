// Package apperror defines the one error type every layer above the
// repository returns. Handlers map apperror.Code to an HTTP status in a
// single place (internal/delivery/http/middleware/recovery.go) instead of
// every handler independently deciding what status a "not found" should be.
package apperror

import "errors"

type Code string

const (
	CodeNotFound     Code = "NOT_FOUND"
	CodeInvalidInput Code = "INVALID_INPUT"
	CodeUnauthorized Code = "UNAUTHORIZED"
	CodeForbidden    Code = "FORBIDDEN"
	CodeConflict     Code = "CONFLICT"
	CodeRateLimited  Code = "RATE_LIMITED"
	CodeInternal     Code = "INTERNAL"
)

type AppError struct {
	Code    Code
	Message string
	Err     error
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *AppError) Unwrap() error { return e.Err }

func New(code Code, message string, err error) *AppError {
	return &AppError{Code: code, Message: message, Err: err}
}

func NotFound(message string) *AppError               { return New(CodeNotFound, message, nil) }
func InvalidInput(message string, err error) *AppError { return New(CodeInvalidInput, message, err) }
func Unauthorized(message string) *AppError            { return New(CodeUnauthorized, message, nil) }
func Forbidden(message string) *AppError               { return New(CodeForbidden, message, nil) }
func Conflict(message string) *AppError                { return New(CodeConflict, message, nil) }
func RateLimited(message string) *AppError              { return New(CodeRateLimited, message, nil) }
func Internal(message string, err error) *AppError     { return New(CodeInternal, message, err) }

// As unwraps err into an *AppError, following the standard errors.As chain
// so an error wrapped N times with fmt.Errorf("...: %w", err) still resolves.
func As(err error) (*AppError, bool) {
	var ae *AppError
	ok := errors.As(err, &ae)
	return ae, ok
}
