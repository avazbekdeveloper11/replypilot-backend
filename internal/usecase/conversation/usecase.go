package conversation

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

type UseCase struct {
	convRepo repository.ConversationRepository
	msgRepo  repository.MessageRepository
}

func New(convRepo repository.ConversationRepository, msgRepo repository.MessageRepository) *UseCase {
	return &UseCase{convRepo: convRepo, msgRepo: msgRepo}
}

func (uc *UseCase) List(ctx context.Context, params repository.ConversationListParams) ([]*entity.Conversation, error) {
	return uc.convRepo.List(ctx, params)
}

func (uc *UseCase) Get(ctx context.Context, orgID, id uuid.UUID) (*entity.Conversation, error) {
	return uc.convRepo.FindByID(ctx, orgID, id)
}

// TakeOver is the AI Inbox's core action: a human agent claims a
// conversation the AI pipeline handed off (see internal/usecase/ai's
// confidence-gate doc comment for how a conversation gets to
// pending_human in the first place). Deliberately restricted to
// pending_human — not "any status" — because that's the one state that
// actually means "waiting for a human to pick this up"; taking over an
// ai_active or already-human_active conversation is a different action
// (not built here — see the frontend's honest scope note on the
// Conversation Detail page still having no message composer, which is
// what taking over a still-AI-owned thread would need to be useful for).
func (uc *UseCase) TakeOver(ctx context.Context, orgID, id, userID uuid.UUID) (*entity.Conversation, error) {
	conv, err := uc.convRepo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if conv.Status != entity.ConversationStatusPendingHuman {
		return nil, apperror.InvalidInput("only a conversation pending human handoff can be taken over", nil)
	}

	conv.Status = entity.ConversationStatusHumanActive
	conv.AssignedUserID = &userID
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// Resolve is the other half of the handoff state machine TakeOver starts —
// see entity.Conversation's doc comment on the AI_ACTIVE -> PENDING_HUMAN
// -> HUMAN_ACTIVE -> RESOLVED chain. Before this method existed, nothing
// anywhere ever set status=resolved, so the "Resolved" filter tab on the
// Conversations page could never show anything — not a frontend bug, the
// backend simply had no way to reach that state.
//
// Allowed from human_active (the normal path: a human wraps up after
// TakeOver) or pending_human directly (a human looks at something the AI
// flagged and decides it doesn't need a reply at all — e.g. spam, or a
// question that answered itself). Not allowed from ai_active: resolving a
// conversation the AI still owns would silently stop it from ever
// replying again on that thread with no human having actually looked at
// it, which is a worse failure mode than just leaving it ai_active.
func (uc *UseCase) Resolve(ctx context.Context, orgID, id uuid.UUID) (*entity.Conversation, error) {
	conv, err := uc.convRepo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if conv.Status != entity.ConversationStatusHumanActive && conv.Status != entity.ConversationStatusPendingHuman {
		return nil, apperror.InvalidInput("only a conversation being handled by a human can be marked resolved", nil)
	}

	conv.Status = entity.ConversationStatusResolved
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// ListMessages checks the conversation belongs to orgID before returning
// any messages. This is a deliberate second check on top of row-level
// security, not redundant with it: RLS protects against a missing WHERE
// clause inside a repository method, but a caller who passes a
// conversation_id belonging to a *different, valid* org context could
// still get past a naive "just query messages by conversation_id" — this
// FindByID call is what turns that into a 404 instead of a data leak.
func (uc *UseCase) ListMessages(ctx context.Context, orgID, conversationID uuid.UUID, cursorBefore *time.Time, limit int) ([]*entity.Message, error) {
	if _, err := uc.convRepo.FindByID(ctx, orgID, conversationID); err != nil {
		return nil, err
	}

	return uc.msgRepo.List(ctx, repository.MessageListParams{
		OrganizationID: orgID,
		ConversationID: conversationID,
		CursorBefore:   cursorBefore,
		Limit:          limit,
	})
}
