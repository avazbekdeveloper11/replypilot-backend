// Package dashboard aggregates data that already lives behind other
// usecases (conversations, instagram accounts) plus a couple of new
// read-only queries (repository.DashboardRepository) into the shapes the
// Dashboard page's six widgets need. It deliberately does not introduce a
// "notifications" domain concept: see Notifications below.
package dashboard

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

// firstResponseWindow bounds the AvgFirstResponseSeconds query to the
// trailing 30 days — an org that's been live for a year shouldn't have
// every dashboard load average across its entire history.
const firstResponseWindow = 30 * 24 * time.Hour

// maxNotifications caps the unread-conversation scan in Notifications so a
// busy org's dashboard load doesn't have to walk an unbounded conversation
// list — 50 is comfortably more than any reasonable "recent" cutoff.
const maxNotifications = 50

type UseCase struct {
	repo     repository.DashboardRepository
	convRepo repository.ConversationRepository
	igRepo   repository.InstagramAccountRepository
}

func New(repo repository.DashboardRepository, convRepo repository.ConversationRepository, igRepo repository.InstagramAccountRepository) *UseCase {
	return &UseCase{repo: repo, convRepo: convRepo, igRepo: igRepo}
}

// Stats bundles the Statistics Cards and Response Time widgets — one round
// trip from the handler's point of view, three queries underneath (only
// one of which, the instagram account list, was already built for another
// feature).
type Stats struct {
	Conversations        *repository.ConversationStats
	ConnectedAccounts     int
	AvgFirstResponseSecs *float64
}

func (uc *UseCase) Stats(ctx context.Context, orgID uuid.UUID) (*Stats, error) {
	convStats, err := uc.repo.ConversationStats(ctx, orgID)
	if err != nil {
		return nil, err
	}

	accounts, err := uc.igRepo.ListByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	since := time.Now().UTC().Add(-firstResponseWindow)
	avgResponse, err := uc.repo.AvgFirstResponseSeconds(ctx, orgID, since)
	if err != nil {
		return nil, err
	}

	return &Stats{
		Conversations:        convStats,
		ConnectedAccounts:    len(accounts),
		AvgFirstResponseSecs: avgResponse,
	}, nil
}

func (uc *UseCase) TimeSeries(ctx context.Context, orgID uuid.UUID, days int) ([]repository.ConversationsPerDay, error) {
	return uc.repo.ConversationsPerDay(ctx, orgID, days)
}

func (uc *UseCase) AIPerformance(ctx context.Context, orgID uuid.UUID) (*repository.AIPerformanceStats, error) {
	return uc.repo.AIPerformance(ctx, orgID)
}

// Notifications derives lightweight notification items from a signal that
// already exists and is already kept correct by the webhook ingestion path
// — Conversation.UnreadCount — rather than a dedicated notifications
// table/producer. `notification_channels` in the schema is about where to
// deliver alerts (webhook/email/Slack config), not a notification feed,
// and nothing in this codebase generates notification events yet; building
// that is a separate, larger feature than "the Dashboard page['s]
// Notifications widget". This is real backend-connected data (unread
// conversations, sorted newest first), not a mock — it's just a narrower
// definition of "notification" than a full events system would give you.
func (uc *UseCase) Notifications(ctx context.Context, orgID uuid.UUID, limit int) ([]*entity.Conversation, error) {
	if limit <= 0 {
		limit = 10
	}

	convs, err := uc.convRepo.List(ctx, repository.ConversationListParams{
		OrganizationID: orgID,
		Limit:          maxNotifications,
	})
	if err != nil {
		return nil, err
	}

	out := make([]*entity.Conversation, 0, limit)
	for _, c := range convs {
		if c.UnreadCount <= 0 {
			continue
		}
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
