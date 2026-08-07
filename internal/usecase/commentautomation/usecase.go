// Package commentautomation is the settings CRUD behind comment-to-DM
// automation — the dashboard-facing half. The actual comment handling
// lives in internal/usecase/instagram (WebhookUseCase.handleComment),
// where the webhook arrives; this package only reads and writes the
// per-org on/off switch and public reply text.
package commentautomation

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

// maxPublicReplyLen bounds the public reply text. Instagram's own comment
// limit is far higher, but a public auto-reply that runs long reads as
// spam under every single comment — this is a product guardrail, not an
// API constraint.
const maxPublicReplyLen = 280

type UseCase struct {
	repo repository.CommentAutomationRepository
}

func New(repo repository.CommentAutomationRepository) *UseCase {
	return &UseCase{repo: repo}
}

// Get returns a disabled zero-value settings object rather than
// (nil, nil) or an error when the org has never configured this — see
// entity.CommentAutomationSettings' doc comment on why absent means off.
// The dashboard renders the same form either way, so there's nothing for a
// caller to branch on.
func (uc *UseCase) Get(ctx context.Context, orgID uuid.UUID) (*entity.CommentAutomationSettings, error) {
	settings, err := uc.repo.Get(ctx, orgID)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return &entity.CommentAutomationSettings{OrganizationID: orgID, Enabled: false}, nil
		}
		return nil, err
	}
	return settings, nil
}

type UpdateInput struct {
	OrganizationID  uuid.UUID
	Enabled         bool
	PublicReplyText *string
}

func (uc *UseCase) Update(ctx context.Context, in UpdateInput) (*entity.CommentAutomationSettings, error) {
	var publicReply *string
	if in.PublicReplyText != nil {
		trimmed := strings.TrimSpace(*in.PublicReplyText)
		if len([]rune(trimmed)) > maxPublicReplyLen {
			return nil, apperror.InvalidInput("public reply text is too long", nil)
		}
		if trimmed != "" {
			publicReply = &trimmed
		}
	}

	settings := &entity.CommentAutomationSettings{
		OrganizationID:  in.OrganizationID,
		Enabled:         in.Enabled,
		PublicReplyText: publicReply,
	}
	if err := uc.repo.Upsert(ctx, settings); err != nil {
		return nil, err
	}
	return settings, nil
}
