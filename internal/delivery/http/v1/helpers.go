package v1

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/middleware"
	"github.com/replypilot/backend/internal/delivery/http/validator"
	"github.com/replypilot/backend/internal/domain/apperror"
)

// bindError wraps a gin ShouldBindJSON error as a client-safe
// apperror.InvalidInput, formatted via the validator package rather than
// exposing validator.ValidationErrors' default (Go-struct-shaped) message.
func bindError(err error) error {
	return apperror.InvalidInput(validator.FormatError(err), err)
}

// orgIDFromContext reads the tenant the request is authenticated for
// (set by middleware.Auth). Every handler behind that middleware calls
// this before touching any repository — it's the source of the org_id
// that flows into row-level-security scoping.
func orgIDFromContext(c *gin.Context) (uuid.UUID, error) {
	v, ok := c.Get(middleware.CtxOrgID)
	if !ok {
		return uuid.Nil, apperror.Unauthorized("missing organization context")
	}
	id, ok := v.(uuid.UUID)
	if !ok {
		return uuid.Nil, apperror.Internal("organization context has unexpected type", nil)
	}
	return id, nil
}

func userIDFromContext(c *gin.Context) (uuid.UUID, error) {
	v, ok := c.Get(middleware.CtxUserID)
	if !ok {
		return uuid.Nil, apperror.Unauthorized("missing user context")
	}
	id, ok := v.(uuid.UUID)
	if !ok {
		return uuid.Nil, apperror.Internal("user context has unexpected type", nil)
	}
	return id, nil
}
