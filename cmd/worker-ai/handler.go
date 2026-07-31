package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/replypilot/backend/internal/usecase/ai"
)

// dmReceivedEvent mirrors instagram.DMReceivedEvent's JSON shape exactly —
// this binary deliberately doesn't import internal/usecase/instagram (a
// worker depending on the API service's usecase package would be a strange
// coupling for two independently-deployed binaries that only agree on a
// wire format), so it keeps its own copy of the shape it expects to
// unmarshal off the queue.
type dmReceivedEvent struct {
	OrganizationID     string `json:"organization_id"`
	ConversationID     string `json:"conversation_id"`
	MessageID          string `json:"message_id"`
	InstagramAccountID string `json:"instagram_account_id"`
}

func handleDelivery(ctx context.Context, logger *zap.Logger, uc *ai.UseCase, body []byte) error {
	var raw dmReceivedEvent
	if err := json.Unmarshal(body, &raw); err != nil {
		// A malformed payload will never parse no matter how many times
		// it's redelivered — log and drop rather than let Consumer.Run
		// requeue it forever (see that method's doc comment on the
		// poison-message gap this doesn't otherwise cover).
		logger.Error("worker-ai: malformed dm.received payload, dropping", zap.Error(err))
		return nil
	}

	ev, err := parseEvent(raw)
	if err != nil {
		logger.Error("worker-ai: invalid dm.received payload, dropping", zap.Error(err))
		return nil
	}

	if err := uc.HandleInboundMessage(ctx, ev); err != nil {
		logger.Error("worker-ai: failed to process inbound message",
			zap.Error(err),
			zap.String("conversation_id", raw.ConversationID),
			zap.String("message_id", raw.MessageID),
		)
		return err // requeue — see ai.UseCase.HandleInboundMessage's doc comment on what's transient vs. not
	}

	logger.Info("worker-ai: processed inbound message", zap.String("conversation_id", raw.ConversationID))
	return nil
}

func parseEvent(raw dmReceivedEvent) (ai.InboundEvent, error) {
	orgID, err := uuid.Parse(raw.OrganizationID)
	if err != nil {
		return ai.InboundEvent{}, fmt.Errorf("parse organization_id: %w", err)
	}
	convID, err := uuid.Parse(raw.ConversationID)
	if err != nil {
		return ai.InboundEvent{}, fmt.Errorf("parse conversation_id: %w", err)
	}
	msgID, err := uuid.Parse(raw.MessageID)
	if err != nil {
		return ai.InboundEvent{}, fmt.Errorf("parse message_id: %w", err)
	}
	accountID, err := uuid.Parse(raw.InstagramAccountID)
	if err != nil {
		return ai.InboundEvent{}, fmt.Errorf("parse instagram_account_id: %w", err)
	}

	return ai.InboundEvent{
		OrganizationID:     orgID,
		ConversationID:     convID,
		MessageID:          msgID,
		InstagramAccountID: accountID,
	}, nil
}
