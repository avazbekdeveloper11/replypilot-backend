package repository

import (
	"context"

	"github.com/replypilot/backend/internal/domain/entity"
)

// AIResponseRepository persists one AI-generated-and-sent reply plus the
// knowledge-base chunks that grounded it, in a single transaction — an
// AIResponse without its citations (or vice versa) would make the "why did
// the AI say this" audit trail incomplete.
type AIResponseRepository interface {
	Create(ctx context.Context, resp *entity.AIResponse, citations []*entity.AIResponseCitation) error
}
