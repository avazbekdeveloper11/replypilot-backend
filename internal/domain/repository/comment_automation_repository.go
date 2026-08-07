package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type CommentAutomationRepository interface {
	// Get returns apperror.NotFound when the org has never configured
	// comment automation — the usecase translates that into "disabled",
	// not an error (see entity.CommentAutomationSettings' doc comment on
	// why absent means off).
	//
	// Safe to call from the webhook path too, unlike
	// InstagramAccountRepository.FindByIGUserID: by the time this is
	// reached the org has already been resolved from instagram_accounts,
	// so the normal tenant-scoped path applies and no
	// app.webhook_lookup bypass policy is needed (see migration 000017's
	// closing comment).
	Get(ctx context.Context, orgID uuid.UUID) (*entity.CommentAutomationSettings, error)
	Upsert(ctx context.Context, settings *entity.CommentAutomationSettings) error
	// ClaimComment inserts a processed_comments row, returning
	// (false, nil) — NOT an error — when this comment was already claimed.
	// Insert-first-then-act: the unique index is what serializes two
	// concurrent redeliveries of the same comment, so a caller that gets
	// true is guaranteed to be the only one sending a private reply for
	// it. See migration 000017's comment on why check-then-act would race.
	ClaimComment(ctx context.Context, claim *entity.ProcessedComment) (claimed bool, err error)
	// ReleaseComment deletes a claim, so a comment whose private reply
	// ultimately failed to send can be retried on Meta's next redelivery
	// instead of being permanently swallowed by the claim above.
	ReleaseComment(ctx context.Context, orgID uuid.UUID, igCommentID string) error
}
