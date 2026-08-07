// Package campaign turns a free-text admin instruction ("1 oy oldin
// gaplashgan mijozlarga yozib chiq") into a concrete, reviewable broadcast:
// Gemini translates the instruction into a segment (how long quiet, which
// channel, exclude buyers or not) plus a draft message, UseCase.Draft
// resolves that segment against real conversations and reports exactly who
// would receive it (and who's excluded, and why), and only after an admin
// reviews/edits and explicitly calls UseCase.Send does anything actually go
// out. There is deliberately no "send immediately" path — an AI picking the
// wrong segment or writing a bad message and having it reach real customers
// unreviewed is a worse failure mode than the extra click, and Meta itself
// treats unreviewed bulk outbound as a spam signal.
//
// Draft/Send are deliberately stateless across the two calls — no
// campaign_drafts table, no draft id. The frontend round-trips the message
// text and the chosen conversation ids back on Send, and Send re-resolves
// each recipient's Instagram-window eligibility fresh rather than trusting
// Draft's (possibly minutes-stale) snapshot. This also means there's no
// server-side draft cleanup job to run, at the cost of the client being the
// one source of truth for "what did the admin actually approve" between
// the two calls.
package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/internal/integration/geminiapi"
	"github.com/replypilot/backend/pkg/crypto"
)

// Generator mirrors internal/usecase/ai's / internal/usecase/conversation's
// — declared separately per this codebase's established "usecases don't
// depend on each other" convention.
type Generator interface {
	Generate(ctx context.Context, systemPrompt, userMessage string) (string, geminiapi.GenerateUsage, error)
}

// Sender mirrors internal/usecase/conversation's — see that package's doc
// comment for why this isn't a shared type.
type Sender interface {
	SendMessage(ctx context.Context, accessToken, recipientIGID, text string) error
}

// TelegramSender mirrors internal/usecase/conversation's.
type TelegramSender interface {
	SendMessage(ctx context.Context, botToken, businessConnectionID, chatID, text string) error
}

// TelegramAccountLookup mirrors internal/usecase/conversation's.
type TelegramAccountLookup interface {
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.TelegramAccount, error)
}

// ProductLister is the narrow read this package needs out of
// repository.ProductRepository — active products only, read once per
// Draft call to give Gemini something real to reference in the message it
// writes, mirroring internal/usecase/ai's buildProductContext in spirit but
// far lighter (names/prices only, no payment links — a campaign message
// nudges a stale conversation back to life, it doesn't quote a checkout
// link the way a live AI reply does).
type ProductLister interface {
	ListActiveByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.Product, error)
}

// authError mirrors internal/usecase/conversation's.
type authError interface {
	IsAuthError() bool
	IsExpired() bool
}

// telegramAuthError mirrors internal/usecase/conversation's.
type telegramAuthError interface {
	IsAuthError() bool
}

type UseCase struct {
	convRepo         repository.ConversationRepository
	msgRepo          repository.MessageRepository
	accountRepo      repository.InstagramAccountRepository
	products         ProductLister
	sender           Sender
	encryptor        *crypto.AESGCMEncryptor
	telegramAccounts TelegramAccountLookup
	telegramSender   TelegramSender
	generator        Generator
}

func New(
	convRepo repository.ConversationRepository,
	msgRepo repository.MessageRepository,
	accountRepo repository.InstagramAccountRepository,
	products ProductLister,
	sender Sender,
	encryptor *crypto.AESGCMEncryptor,
	telegramAccounts TelegramAccountLookup,
	telegramSender TelegramSender,
	generator Generator,
) *UseCase {
	return &UseCase{
		convRepo:         convRepo,
		msgRepo:          msgRepo,
		accountRepo:      accountRepo,
		products:         products,
		sender:           sender,
		encryptor:        encryptor,
		telegramAccounts: telegramAccounts,
		telegramSender:   telegramSender,
		generator:        generator,
	}
}

// instagramMessageWindow is Meta's own rule, not a number this codebase
// chose — a business may message an Instagram customer only within 24
// hours of that customer's own last message. There is no automated-message
// exception; the HUMAN_AGENT tag that extends this to 7 days is explicitly
// reserved for human-sent support replies and Meta prohibits (and detects)
// its use for bot/automated sends, so it is deliberately not used anywhere
// in this codebase. Telegram has no equivalent restriction.
const instagramMessageWindow = 24 * time.Hour

// maxCampaignMessageLen mirrors internal/usecase/conversation's
// maxSendMessageLen — same reasoning (fail fast on an overlong message
// rather than let Meta's/Telegram's API reject it partway through a bulk
// send).
const maxCampaignMessageLen = 1000

// maxSendRecipients caps one Send call — see the package doc comment on
// why sends are synchronous, not queued: a very large org running this
// against thousands of stale conversations in one HTTP request risks
// timing out or tripping Meta's/Telegram's own rate limits. This is a
// known MVP limit, not a hard product ceiling — a background-job version
// (queue one message per recipient, worker sends with pacing) is the
// natural next step if an org's stale-conversation count regularly
// exceeds this.
const maxSendRecipients = 500

// RecipientPreview is one candidate conversation in a CampaignDraft —
// Eligible/IneligibleReason is what lets the dashboard show, honestly,
// why an Instagram customer who's gone quiet for a month can't be
// messaged (Meta's 24-hour window, not a bug) rather than silently
// dropping them from the list.
type RecipientPreview struct {
	ConversationID        uuid.UUID
	CustomerUsername      *string
	Channel               entity.ConversationChannel
	LastCustomerMessageAt time.Time
	Eligible              bool
	IneligibleReason      *string
}

// CampaignDraft is UseCase.Draft's full result — echoes back the segment
// Gemini derived (so the admin can sanity-check "did it understand me
// right", not just see the final recipient count) alongside the message
// and the resolved, eligibility-annotated recipient list.
type CampaignDraft struct {
	Message                 string
	MinDaysSinceLastMessage int
	MaxDaysSinceLastMessage *int
	Channel                 string
	ExcludeCustomersWhoPaid bool
	Recipients              []RecipientPreview
	EligibleCount           int
	IneligibleCount         int
}

// SkippedRecipient is one entry in CampaignResult.Skipped — every
// conversation Send didn't successfully message, with a human-readable
// reason (not found, window closed since Draft, the actual send error).
type SkippedRecipient struct {
	ConversationID uuid.UUID
	Reason         string
}

type CampaignResult struct {
	SentCount int
	Skipped   []SkippedRecipient
}

// draftSystemPrompt drives Draft's single Gemini call — translates a free
// text instruction into both a machine-readable segment and the actual
// customer-facing message, in one pass. See the package doc comment on why
// nothing downstream trusts this output blindly (Draft still resolves and
// displays the segment for review; Send never fires off of this call
// directly).
const draftSystemPrompt = `Siz ReplyPilot dasturidagi ichki AI yordamchisiz. Administrator sizga o'z so'zi bilan qanday mijozlarga xabar yubormoqchi ekanini va nima uchun yozayotganini tasvirlab beradi (masalan: "1 oy oldin gaplashgan lekin sotib olmagan mijozlarga eslatma yubor"). Sizning vazifangiz — bu tavsifni aniq mezonlarga aylantirish va o'sha mijozlarga yuboriladigan xabar matnini yozish.

Faqat quyidagi JSON formatida javob bering. Hech qanday izoh, hech qanday tushuntirish, hech qanday ` + "```" + ` belgisi — faqat toza JSON, boshqa hech narsa:

{"min_days_since_last_message": <butun son>, "max_days_since_last_message": <butun son yoki null>, "channel": "any" | "telegram" | "instagram", "exclude_customers_who_paid": true yoki false, "message": "<xabar matni>"}

Qoidalar:
- min_days_since_last_message — mijoz oxirgi marta yozganidan beri KAMIDA nechchi kun o'tgan bo'lishi kerak. "1 oy oldin" ~ 28, "2 hafta oldin" ~ 14, "1 hafta oldin" ~ 7, "bir necha kun" ~ 3 deb hisoblang.
- max_days_since_last_message — yuqori chegara. Agar administrator faqat pastki chegara aytgan bo'lsa (masalan "1 oy oldin VA UNDAN KO'P jim bo'lganlar"), null qo'ying. Agar aniq oraliq aytilsa (masalan "1-2 hafta oldin yozganlar"), ikkala qiymatni ham to'ldiring.
- Agar administrator aniq muddat aytmasa (masalan shunchaki "jim bo'lib qolgan mijozlarga yozib chiq" desa), min_days_since_last_message=7, max_days_since_last_message=null qo'ying.
- channel ko'rsatilmasa "any" qo'ying.
- exclude_customers_who_paid — administrator "sotib olmagan", "hali xarid qilmagan", "buyurtma bermagan" kabi so'zlar ishlatsa true, aks holda false.
- message maydoni — bu haqiqiy Instagram yoki Telegram DM sifatida haqiqiy mijozga to'g'ridan-to'g'ri yuboriladi. Uni xuddi jonli, samimiy jamoa a'zosi yozgandek qiling: 1-3 qisqa gap, tabiiy va iliq ohang, "hurmatli mijoz" kabi rasmiy murojaat yoki "sifatli xizmat"/"eng yaxshi narxlar" kabi reklama klişelari ISHLATMANG. Nega yozayotganingizni tabiiy tarzda bildiring — masalan oldingi suhbatni eslatib, hol-ahvol so'rab yoki yordam kerakmi deb so'rang — bosim o'tkazmasdan va mijozni noqulay qilib qo'ymasdan.
- Agar quyida mahsulotlar ro'yxati berilgan bo'lsa va tabiiy ravishda mos kelsa, xabarda eslatib o'tishingiz mumkin, lekin narx yoki to'lov havolasini o'zingiz to'qimang — faqat umumiy tarzda eslating.
- message maydonida markdown formatlash ishlatmang (**, *, # va h.k.).`

// Draft asks Gemini to translate instruction into a segment + message,
// resolves that segment against real conversations, and annotates each
// candidate with whether it can actually be messaged right now — see the
// package doc comment on the Instagram 24-hour window this enforces.
func (uc *UseCase) Draft(ctx context.Context, orgID uuid.UUID, instruction string) (*CampaignDraft, error) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return nil, apperror.InvalidInput("instruction is required", nil)
	}

	products, err := uc.products.ListActiveByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	userMessage := instruction + "\n\n" + buildCampaignProductContext(products)
	raw, _, err := uc.generator.Generate(ctx, draftSystemPrompt, userMessage)
	if err != nil {
		return nil, apperror.Internal("draft campaign", err)
	}

	segment, err := parseCampaignSegmentJSON(raw)
	if err != nil {
		return nil, apperror.Internal("draft campaign", fmt.Errorf("gemini returned an unparseable segment: %w", err))
	}

	message := strings.TrimSpace(stripCampaignMarkdownEmphasis(segment.Message))
	if message == "" {
		return nil, apperror.Internal("draft campaign", errors.New("gemini returned an empty message"))
	}

	var channelFilter *entity.ConversationChannel
	switch segment.Channel {
	case string(entity.ConversationChannelTelegram):
		c := entity.ConversationChannelTelegram
		channelFilter = &c
	case string(entity.ConversationChannelInstagram):
		c := entity.ConversationChannelInstagram
		channelFilter = &c
	}

	candidates, err := uc.convRepo.ListBroadcastCandidates(ctx, orgID, segment.MinDaysSinceLastMessage, segment.MaxDaysSinceLastMessage, channelFilter, 0)
	if err != nil {
		return nil, err
	}

	draft := &CampaignDraft{
		Message:                 message,
		MinDaysSinceLastMessage: segment.MinDaysSinceLastMessage,
		MaxDaysSinceLastMessage: segment.MaxDaysSinceLastMessage,
		Channel:                 segment.Channel,
		ExcludeCustomersWhoPaid: segment.ExcludeCustomersWhoPaid,
		Recipients:              make([]RecipientPreview, 0, len(candidates)),
	}

	for _, c := range candidates {
		if segment.ExcludeCustomersWhoPaid && c.HasPaidOrder {
			continue
		}
		preview := RecipientPreview{
			ConversationID:        c.Conversation.ID,
			CustomerUsername:      c.Conversation.CustomerUsername,
			Channel:               c.Conversation.Channel,
			LastCustomerMessageAt: c.LastCustomerMessageAt,
		}
		preview.Eligible, preview.IneligibleReason = checkInstagramWindow(c.Conversation.Channel, c.LastCustomerMessageAt)
		if preview.Eligible {
			draft.EligibleCount++
		} else {
			draft.IneligibleCount++
		}
		draft.Recipients = append(draft.Recipients, preview)
	}

	return draft, nil
}

// checkInstagramWindow is the one place this package decides whether a
// message can legally go out right now — Telegram is always eligible;
// Instagram only within instagramMessageWindow of the customer's last
// inbound message. Called from both Draft (a snapshot, for display) and
// Send (the real, load-bearing check).
func checkInstagramWindow(channel entity.ConversationChannel, lastCustomerMessageAt time.Time) (eligible bool, reason *string) {
	if channel != entity.ConversationChannelInstagram {
		return true, nil
	}
	if time.Since(lastCustomerMessageAt) <= instagramMessageWindow {
		return true, nil
	}
	msg := fmt.Sprintf(
		"Instagram xabar oynasi yopilgan — mijoz oxirgi marta %s oldin yozgan. Meta qoidasiga ko'ra, mijoz o'zi yozmaguncha, 24 soatdan keyin unga DM yuborib bo'lmaydi.",
		formatDaysAgo(lastCustomerMessageAt),
	)
	return false, &msg
}

func formatDaysAgo(t time.Time) string {
	days := int(time.Since(t).Hours() / 24)
	if days <= 0 {
		return "kam bo'lmagan"
	}
	return fmt.Sprintf("%d kun", days)
}

// Send messages every conversation id the admin approved, in order,
// stopping for none of them individually — a failure on one recipient is
// recorded in CampaignResult.Skipped and the rest still get attempted.
// Re-checks Instagram-window eligibility fresh per recipient (see the
// package doc comment on why Draft's snapshot isn't trusted here) rather
// than relying on the client to have only sent back ids Draft marked
// eligible.
func (uc *UseCase) Send(ctx context.Context, orgID, userID uuid.UUID, message string, conversationIDs []uuid.UUID) (*CampaignResult, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, apperror.InvalidInput("message is required", nil)
	}
	if len(message) > maxCampaignMessageLen {
		return nil, apperror.InvalidInput("message is too long", nil)
	}
	if len(conversationIDs) == 0 {
		return nil, apperror.InvalidInput("at least one recipient is required", nil)
	}
	if len(conversationIDs) > maxSendRecipients {
		return nil, apperror.InvalidInput(fmt.Sprintf("too many recipients in one send (max %d) — see campaign.UseCase's doc comment on why sends are synchronous", maxSendRecipients), nil)
	}

	result := &CampaignResult{}
	for _, id := range conversationIDs {
		if err := uc.sendOne(ctx, orgID, userID, id, message); err != nil {
			result.Skipped = append(result.Skipped, SkippedRecipient{ConversationID: id, Reason: err.Error()})
			continue
		}
		result.SentCount++
	}
	return result, nil
}

func (uc *UseCase) sendOne(ctx context.Context, orgID, userID, conversationID uuid.UUID, message string) error {
	conv, err := uc.convRepo.FindByID(ctx, orgID, conversationID)
	if err != nil {
		return errors.New("suhbat topilmadi")
	}

	if conv.Channel == entity.ConversationChannelInstagram {
		lastCustomerMessageAt, err := uc.msgRepo.LastCustomerMessageAt(ctx, orgID, conv.ID)
		if err != nil {
			return errors.New("oxirgi mijoz xabarini tekshirib bo'lmadi")
		}
		if lastCustomerMessageAt == nil {
			return errors.New("mijozdan hali xabar kelmagan")
		}
		if eligible, reason := checkInstagramWindow(conv.Channel, *lastCustomerMessageAt); !eligible {
			return errors.New(*reason)
		}
	}

	switch conv.Channel {
	case entity.ConversationChannelTelegram:
		if err := uc.sendTelegram(ctx, orgID, conv, message); err != nil {
			return err
		}
	default:
		if err := uc.sendInstagram(ctx, orgID, conv, message); err != nil {
			return err
		}
	}

	outbound := &entity.Message{
		OrganizationID: orgID,
		ConversationID: conv.ID,
		Direction:      entity.MessageDirectionOutbound,
		SenderType:     entity.MessageSenderHuman,
		SenderUserID:   &userID,
		MessageType:    entity.MessageTypeText,
		Content:        &message,
	}
	if err := uc.msgRepo.Create(ctx, outbound); err != nil {
		// The message already reached the customer — see
		// internal/usecase/conversation.UseCase.SendMessage's identical
		// comment on its own outbound-message Create call for why this
		// still surfaces as an error rather than silently succeeding.
		return errors.New("xabar yuborildi, lekin yozib qolishda xatolik ketdi")
	}

	preview := message
	if len(preview) > maxCampaignPreviewLen {
		preview = preview[:maxCampaignPreviewLen]
	}
	now := time.Now()
	conv.LastMessageAt = &now
	conv.LastMessagePreview = &preview
	conv.UnreadCount = 0
	_ = uc.convRepo.Update(ctx, conv)

	return nil
}

// maxCampaignPreviewLen mirrors internal/usecase/conversation's
// maxReplyPreviewLen — same list-view-teaser reasoning.
const maxCampaignPreviewLen = 140

func (uc *UseCase) sendInstagram(ctx context.Context, orgID uuid.UUID, conv *entity.Conversation, message string) error {
	account, err := uc.accountRepo.FindByID(ctx, orgID, conv.InstagramAccountID)
	if err != nil {
		return errors.New("instagram akkaunt topilmadi")
	}
	accessToken, err := uc.encryptor.Decrypt(account.AccessTokenEncrypted)
	if err != nil {
		return errors.New("access token'ni ochib bo'lmadi")
	}
	if err := uc.sender.SendMessage(ctx, accessToken, conv.CustomerIGID, message); err != nil {
		uc.handleSendFailure(ctx, account, err)
		return errors.New("instagram orqali yuborib bo'lmadi: " + err.Error())
	}
	return nil
}

func (uc *UseCase) sendTelegram(ctx context.Context, orgID uuid.UUID, conv *entity.Conversation, message string) error {
	if conv.TelegramAccountID == nil {
		return errors.New("telegram akkaunt biriktirilmagan")
	}
	account, err := uc.telegramAccounts.FindByID(ctx, orgID, *conv.TelegramAccountID)
	if err != nil {
		return errors.New("telegram akkaunt topilmadi")
	}
	if account.BusinessConnectionID == nil {
		return errors.New("telegram akkaunt ulanmagan")
	}
	botToken, err := uc.encryptor.Decrypt(account.BotTokenEncrypted)
	if err != nil {
		return errors.New("bot token'ni ochib bo'lmadi")
	}
	if err := uc.telegramSender.SendMessage(ctx, botToken, *account.BusinessConnectionID, conv.CustomerIGID, message); err != nil {
		uc.handleTelegramSendFailure(ctx, account, err)
		return errors.New("telegram orqali yuborib bo'lmadi: " + err.Error())
	}
	return nil
}

// handleSendFailure mirrors internal/usecase/conversation's method of the
// same name — see its doc comment for the full reasoning. Duplicated for
// the same "usecases don't depend on each other" reason as Sender above.
func (uc *UseCase) handleSendFailure(ctx context.Context, account *entity.InstagramAccount, sendErr error) {
	var ae authError
	if !errors.As(sendErr, &ae) || !ae.IsAuthError() {
		return
	}
	newStatus := entity.InstagramAccountStatusRevoked
	if ae.IsExpired() {
		newStatus = entity.InstagramAccountStatusExpired
	}
	if account.Status == newStatus {
		return
	}
	account.Status = newStatus
	_ = uc.accountRepo.Update(ctx, account)
}

// handleTelegramSendFailure mirrors internal/usecase/conversation's method
// of the same name.
func (uc *UseCase) handleTelegramSendFailure(ctx context.Context, account *entity.TelegramAccount, sendErr error) {
	var ae telegramAuthError
	if !errors.As(sendErr, &ae) || !ae.IsAuthError() {
		return
	}
	if account.Status == entity.TelegramAccountStatusError {
		return
	}
	updater, ok := uc.telegramAccounts.(interface {
		Update(ctx context.Context, account *entity.TelegramAccount) error
	})
	if !ok {
		return
	}
	account.Status = entity.TelegramAccountStatusError
	_ = updater.Update(ctx, account)
}

// buildCampaignProductContext is a much lighter version of
// internal/usecase/ai's buildProductContext — names and prices only, no
// payment links (a campaign message nudges someone back into the
// conversation; if they're ready to pay, the normal AI reply pipeline
// picks up from their response and generates a real link then, grounded
// the same way every other reply is).
func buildCampaignProductContext(products []*entity.Product) string {
	if len(products) == 0 {
		return "Mahsulotlar: (ro'yxat bo'sh)"
	}
	var b strings.Builder
	b.WriteString("Mahsulotlar:\n")
	for _, p := range products {
		fmt.Fprintf(&b, "- %s (%s so'm)\n", p.Name, formatCampaignSom(p.PriceCents))
	}
	return b.String()
}

// formatCampaignSom mirrors internal/usecase/ai's formatSom exactly — same
// duplication reasoning as everywhere else in this codebase.
func formatCampaignSom(priceCents int64) string {
	som := priceCents / 100
	remainder := priceCents % 100
	if remainder == 0 {
		return fmt.Sprintf("%d", som)
	}
	return fmt.Sprintf("%d.%02d", som, remainder)
}

// campaignSegmentJSON is draftSystemPrompt's exact response shape.
type campaignSegmentJSON struct {
	MinDaysSinceLastMessage int    `json:"min_days_since_last_message"`
	MaxDaysSinceLastMessage *int   `json:"max_days_since_last_message"`
	Channel                 string `json:"channel"`
	ExcludeCustomersWhoPaid bool   `json:"exclude_customers_who_paid"`
	Message                 string `json:"message"`
}

// parseCampaignSegmentJSON tolerates Gemini wrapping its JSON in a
// ```json fence despite draftSystemPrompt explicitly asking it not to —
// the same defensive-parsing posture as every other place in this
// codebase that asks Gemini for a specific plain-text shape and then
// double-checks it (see systemPromptTemplate's own markdown-stripping
// backstop for the same reasoning applied to a different failure mode).
// Also clamps/normalizes fields that would otherwise let a malformed or
// out-of-range Gemini response resolve a nonsensical segment (a negative
// day count, an unrecognized channel string).
func parseCampaignSegmentJSON(raw string) (*campaignSegmentJSON, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var segment campaignSegmentJSON
	if err := json.Unmarshal([]byte(cleaned), &segment); err != nil {
		return nil, err
	}

	if segment.MinDaysSinceLastMessage < 0 {
		segment.MinDaysSinceLastMessage = 0
	}
	if segment.MaxDaysSinceLastMessage != nil && *segment.MaxDaysSinceLastMessage < segment.MinDaysSinceLastMessage {
		segment.MaxDaysSinceLastMessage = nil
	}
	switch segment.Channel {
	case string(entity.ConversationChannelTelegram), string(entity.ConversationChannelInstagram):
		// valid as-is
	default:
		segment.Channel = "any"
	}

	return &segment, nil
}

// campaignMarkdownEmphasisRegex / stripCampaignMarkdownEmphasis mirror
// internal/usecase/ai's markdownEmphasisRegex/stripMarkdownEmphasis
// exactly — same backstop against Gemini emitting **bold**/*italic*
// despite being told not to, applied here to a message that's about to be
// sent to a real customer over Instagram/Telegram DM, which render plain
// text only.
var campaignMarkdownEmphasisRegex = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__|\*(.+?)\*|_(.+?)_`)

func stripCampaignMarkdownEmphasis(text string) string {
	return campaignMarkdownEmphasisRegex.ReplaceAllStringFunc(text, func(match string) string {
		groups := campaignMarkdownEmphasisRegex.FindStringSubmatch(match)
		for _, g := range groups[1:] {
			if g != "" {
				return g
			}
		}
		return match
	})
}
