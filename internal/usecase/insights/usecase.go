// Package insights builds an org-wide, on-demand AI overview — real
// aggregate numbers (sales confirmed via Click, leads captured,
// conversation volume) narrated alongside a qualitative read of recent
// customer messages (common themes, overall sentiment). The org-wide
// counterpart to internal/usecase/conversation.UseCase.Summarize, cached
// in ai_insights_cache (see entity.AIInsights) until explicitly
// regenerated — nothing here runs automatically or on a schedule.
//
// Deliberately does NOT let Gemini compute any of the numbers itself:
// SalesCount/SalesAmountCents/LeadCount/ConversationCount always come from
// real queries (repository.OrderRepository.Stats, LeadRepository,
// ConversationRepository, DashboardRepository), passed into the prompt as
// facts to restate, not derived by the model. Only the qualitative part —
// themes, sentiment — is something Gemini actually reasons about, over a
// bounded sample of real customer message text.
package insights

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/internal/integration/geminiapi"
)

// Generator mirrors internal/usecase/ai's / internal/usecase/conversation's
// — declared separately per this codebase's established "usecases don't
// depend on each other" convention.
type Generator interface {
	Generate(ctx context.Context, systemPrompt, userMessage string) (string, geminiapi.GenerateUsage, error)
}

type UseCase struct {
	cache     repository.AIInsightsRepository
	orders    repository.OrderRepository
	leads     repository.LeadRepository
	dashboard repository.DashboardRepository
	msgRepo   repository.MessageRepository
	generator Generator
}

func New(
	cache repository.AIInsightsRepository,
	orders repository.OrderRepository,
	leads repository.LeadRepository,
	dashboard repository.DashboardRepository,
	msgRepo repository.MessageRepository,
	generator Generator,
) *UseCase {
	return &UseCase{
		cache:     cache,
		orders:    orders,
		leads:     leads,
		dashboard: dashboard,
		msgRepo:   msgRepo,
		generator: generator,
	}
}

// Get returns (nil, nil) — not a NotFound error — when the org has never
// generated insights, same convention as click.UseCase.Get, so the
// frontend can treat "never generated" as "show a generate button" without
// an error-type switch.
func (uc *UseCase) Get(ctx context.Context, orgID uuid.UUID) (*entity.AIInsights, error) {
	insights, err := uc.cache.Get(ctx, orgID)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil, nil
		}
		return nil, err
	}
	return insights, nil
}

// maxSampledMessages/maxSampledMessageChars bound the qualitative half of
// Regenerate's prompt — same "MVP-sized shop" capping convention as
// internal/usecase/ai's retrievalLimit/maxProductsInPrompt: a fixed,
// recent, capped sample, not the whole org's message history, so this
// stays a small handful of cents' worth of Gemini input even for a busy
// org, and stays fast.
const (
	maxSampledMessages     = 80
	maxSampledMessageChars = 300
)

// insightsSystemPrompt drives Regenerate. See the package doc comment on
// why the numeric facts are handed to Gemini to restate, never computed by
// it.
const insightsSystemPrompt = `Siz ReplyPilot dasturidagi ichki AI tahlilchisiz. Administrator uchun umumiy biznes xulosasi tayyorlaysiz.

Sizga ikki turdagi ma'lumot beriladi:
1. Aniq raqamli statistika — bu raqamlarni AYNAN shu holda, hech qanday o'zgartirmasdan, hisob-kitob qilmasdan qayta yozing.
2. Mijozlarning so'nggi xabarlaridan namuna — shu matnlar asosida umumiy mavzular, ko'p so'raladigan savollar va mijozlarning kayfiyati/fikri haqida xulosa chiqaring.

Javobingizni aynan quyidagi 4 ta bandda, oddiy matn shaklida yozing (markdown formatlash — **, *, # va hokazo — ishlatmang):

Sotuvlar: [statistikani tabiy gap shaklida qayta yozing]
Mijozlar bilan ishlash: [lidlar va suhbatlar sonini tabiy gap shaklida qayta yozing]
Mijozlar nima haqida ko'p so'raydi: [namunadagi xabarlar asosida 2-3 ta asosiy mavzu]
Mijozlar kayfiyati: [ijobiy/salbiy/aralash, 1-2 gaplik qisqa asoslash bilan]

Agar namunada matn kam yoki umuman bo'lmasa, oxirgi ikki band uchun "Xulosa chiqarish uchun yetarli mijoz xabari yo'q" deb yozing — hech narsani o'ylab topmang.`

// Regenerate recomputes every real number fresh, samples recent customer
// messages, asks Gemini to narrate the two together, and overwrites the
// org's cached row (see entity.AIInsights' doc comment on why there's no
// history kept — every call replaces, not appends).
func (uc *UseCase) Regenerate(ctx context.Context, orgID uuid.UUID) (*entity.AIInsights, error) {
	orderStats, err := uc.orders.Stats(ctx, orgID)
	if err != nil {
		return nil, err
	}

	leads, err := uc.leads.ListByOrganization(ctx, orgID, nil)
	if err != nil {
		return nil, err
	}

	convStats, err := uc.dashboard.ConversationStats(ctx, orgID)
	if err != nil {
		return nil, err
	}

	samples, err := uc.msgRepo.ListRecentInboundByOrganization(ctx, orgID, maxSampledMessages)
	if err != nil {
		return nil, err
	}

	prompt := buildInsightsInput(orderStats, len(leads), int(convStats.Total), samples)
	summary, _, err := uc.generator.Generate(ctx, insightsSystemPrompt, prompt)
	if err != nil {
		return nil, apperror.Internal("generate ai insights", err)
	}
	summary = strings.TrimSpace(stripInsightsMarkdownEmphasis(summary))
	if summary == "" {
		return nil, apperror.Internal("generate ai insights", fmt.Errorf("gemini returned an empty summary"))
	}

	insights := &entity.AIInsights{
		OrganizationID:    orgID,
		Summary:           summary,
		SalesCount:        orderStats.PaidCount,
		SalesAmountCents:  orderStats.PaidAmountCents,
		LeadCount:         len(leads),
		ConversationCount: int(convStats.Total),
		GeneratedAt:       time.Now(),
	}
	if err := uc.cache.Upsert(ctx, insights); err != nil {
		return nil, err
	}
	return insights, nil
}

// buildInsightsInput is the single "user" turn passed to Gemini — a facts
// block (never to be altered, per insightsSystemPrompt) followed by a
// capped, truncated sample of real customer message text.
func buildInsightsInput(orderStats *repository.OrderStats, leadCount, conversationCount int, samples []*entity.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Statistika:\n")
	fmt.Fprintf(&b, "- To'langan buyurtmalar (Click orqali): %d ta, jami %s so'm\n", orderStats.PaidCount, formatInsightsSom(orderStats.PaidAmountCents))
	fmt.Fprintf(&b, "- Yig'ilgan lidlar: %d ta\n", leadCount)
	fmt.Fprintf(&b, "- Jami suhbatlar: %d ta\n\n", conversationCount)

	if len(samples) == 0 {
		fmt.Fprintf(&b, "Mijozlarning so'nggi xabarlaridan namuna: yo'q.")
		return b.String()
	}

	fmt.Fprintf(&b, "Mijozlarning so'nggi xabarlaridan namuna (%d ta):\n", len(samples))
	for _, m := range samples {
		if m.Content == nil {
			continue
		}
		text := strings.TrimSpace(*m.Content)
		if text == "" {
			continue
		}
		if len(text) > maxSampledMessageChars {
			text = text[:maxSampledMessageChars]
		}
		fmt.Fprintf(&b, "- %s\n", text)
	}
	return b.String()
}

// formatInsightsSom mirrors internal/usecase/ai's formatSom exactly (same
// UZS-without-decimals-when-whole convention) — duplicated, not shared,
// for the same "usecases don't depend on each other" reason as everything
// else in this file.
func formatInsightsSom(priceCents int64) string {
	som := priceCents / 100
	remainder := priceCents % 100
	if remainder == 0 {
		return fmt.Sprintf("%d", som)
	}
	return fmt.Sprintf("%d.%02d", som, remainder)
}

// insightsMarkdownEmphasisRegex / stripInsightsMarkdownEmphasis mirror
// internal/usecase/ai's markdownEmphasisRegex/stripMarkdownEmphasis exactly
// — same backstop against Gemini emitting **bold**/*italic* despite being
// told not to. Duplicated, not shared, for the same reason as
// formatInsightsSom above.
var insightsMarkdownEmphasisRegex = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__|\*(.+?)\*|_(.+?)_`)

func stripInsightsMarkdownEmphasis(text string) string {
	return insightsMarkdownEmphasisRegex.ReplaceAllStringFunc(text, func(match string) string {
		groups := insightsMarkdownEmphasisRegex.FindStringSubmatch(match)
		for _, g := range groups[1:] {
			if g != "" {
				return g
			}
		}
		return match
	})
}
