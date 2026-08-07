// Package v1 holds every HTTP handler and its request/response DTOs for API
// version 1. DTOs are deliberately separate structs from domain entities —
// binding tags (validation) and JSON shape belong to the transport layer,
// not the domain.
package v1

import "time"

type RegisterRequest struct {
	OrganizationName string `json:"organization_name" binding:"required,min=2,max=120"`
	OrganizationSlug string `json:"organization_slug" binding:"required,min=2,max=60,alphanum"`
	FullName         string `json:"full_name" binding:"required,min=2,max=120"`
	Email            string `json:"email" binding:"required,email"`
	Password         string `json:"password" binding:"required,min=8,max=72"`
	// Code is the 6-digit code sent to Email by POST /auth/register/code —
	// registration fails without it (see auth.UseCase.Register).
	Code string `json:"code" binding:"required,len=6"`
}

type RequestRegistrationCodeRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type LoginRequest struct {
	Email          string `json:"email" binding:"required,email"`
	Password       string `json:"password" binding:"required"`
	OrganizationID string `json:"organization_id" binding:"required,uuid"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type ListOrganizationsQuery struct {
	Email string `form:"email" binding:"required,email"`
}

type OrganizationMembershipResponse struct {
	Organization OrgResponse `json:"organization"`
	MemberStatus string      `json:"member_status"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type ResetPasswordRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Code        string `json:"code" binding:"required,len=6"`
	NewPassword string `json:"new_password" binding:"required,min=8,max=72"`
}

type AuthResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresAt    string       `json:"expires_at"`
	User         UserResponse `json:"user"`
	Organization OrgResponse  `json:"organization"`
}

type UserResponse struct {
	ID              string  `json:"id"`
	Email           string  `json:"email"`
	FullName        string  `json:"full_name"`
	AvatarURL       *string `json:"avatar_url,omitempty"`
	// IsPlatformAdmin is read-only here — there is no request DTO field
	// that sets it (see entity.User's doc comment: it's a manual operator
	// action, not a self-serve API). Exposed so the frontend can decide
	// whether to show the Admin nav link at all.
	IsPlatformAdmin bool `json:"is_platform_admin"`
}

type UpdateProfileRequest struct {
	FullName  string  `json:"full_name" binding:"required,min=2,max=120"`
	AvatarURL *string `json:"avatar_url"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8,max=72"`
}

type UpdateOrganizationRequest struct {
	Name     string `json:"name" binding:"required,min=2,max=120"`
	Timezone string `json:"timezone" binding:"omitempty"`
}

type OrgResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Status   string `json:"status"`
	Timezone string `json:"timezone"`
}

type RoleResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TeamMemberResponse struct {
	ID        string       `json:"id"`
	User      UserResponse `json:"user"`
	Role      RoleResponse `json:"role"`
	Status    string       `json:"status"`
	InvitedAt string       `json:"invited_at"`
	JoinedAt  *string      `json:"joined_at,omitempty"`
}

type InviteMemberRequest struct {
	Email  string `json:"email" binding:"required,email"`
	RoleID string `json:"role_id" binding:"required,uuid"`
}

type UpdateMemberRoleRequest struct {
	RoleID string `json:"role_id" binding:"required,uuid"`
}

// KnowledgeDocumentResponse deliberately does not include chunk content or
// embeddings — those are internal to the RAG pipeline, not something the
// Knowledge Base page's document list needs to render. Content is the
// original editable text (see entity.KnowledgeDocument.Content) — nil for
// documents ingested before that column existed. Included here (not a
// separate "detail" DTO) since this codebase doesn't split list/detail
// shapes elsewhere (see ProductResponse); the list view just doesn't
// render it.
type KnowledgeDocumentResponse struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	SourceType   string  `json:"source_type"`
	Content      *string `json:"content,omitempty"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

// UpdateKnowledgeDocumentRequest: omitting content entirely (nil, not "")
// means "title-only edit" — see knowledgebase.UpdateInput's doc comment on
// why that distinction matters (it decides whether chunks/embeddings get
// rebuilt at all).
type UpdateKnowledgeDocumentRequest struct {
	Title   string  `json:"title" binding:"required,min=1,max=200"`
	Content *string `json:"content" binding:"omitempty,min=1"`
}

// ProductResponse mirrors entity.Product for the dashboard's Products page.
// PriceCents stays in the smallest currency unit here too — the frontend
// formats it for display the same way it already formats
// PlanResponse.PriceMonthlyCents, no new formatting convention introduced.
type ProductResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	PriceCents  int64   `json:"price_cents"`
	Currency    string  `json:"currency"`
	IsActive    bool    `json:"is_active"`
	CreatedAt   string  `json:"created_at"`
}

type CreateProductRequest struct {
	Name        string  `json:"name" binding:"required,min=1,max=200"`
	Description *string `json:"description" binding:"omitempty,max=2000"`
	PriceCents  int64   `json:"price_cents" binding:"required,min=1"`
	Currency    string  `json:"currency" binding:"omitempty,len=3"`
}

type UpdateProductRequest struct {
	Name        string  `json:"name" binding:"required,min=1,max=200"`
	Description *string `json:"description" binding:"omitempty,max=2000"`
	PriceCents  int64   `json:"price_cents" binding:"required,min=1"`
	Currency    string  `json:"currency" binding:"omitempty,len=3"`
	IsActive    bool    `json:"is_active"`
}

// ClickIntegrationResponse never includes anything secret — merchant_id and
// service_id are Click's own public identifiers (see
// entity.ClickIntegration's doc comment), safe to round-trip to the
// dashboard as-is so the settings card can show what's currently connected.
// SecretKeyEncrypted deliberately has no field here at all, unlike the two
// above — it's a real secret (webhook signature verification), never sent
// back to the browser once saved.
type ClickIntegrationResponse struct {
	MerchantID     string  `json:"merchant_id"`
	ServiceID      string  `json:"service_id"`
	MerchantUserID *string `json:"merchant_user_id,omitempty"`
	ConnectedAt    string  `json:"connected_at"`
}

// LeadResponse mirrors entity.Lead for the dashboard's Leads page.
// CustomerUsername is joined in from the conversation (see
// entity.Lead.CustomerUsername's doc comment), not a leads column.
type LeadResponse struct {
	ID               string  `json:"id"`
	ConversationID   string  `json:"conversation_id"`
	CustomerUsername *string `json:"customer_username,omitempty"`
	Phone            string  `json:"phone"`
	Summary          string  `json:"summary"`
	Status           string  `json:"status"`
	CreatedAt        string  `json:"created_at"`
}

type UpdateLeadStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=new contacted done"`
}

// AIInsightsResponse mirrors entity.AIInsights — see that type's doc
// comment. SalesCount/SalesAmountCents/LeadCount/ConversationCount are
// real numbers from the last time this was generated (GeneratedAt), not
// live — the frontend shows GeneratedAt alongside them so that's obvious.
type AIInsightsResponse struct {
	Summary           string `json:"summary"`
	SalesCount        int    `json:"sales_count"`
	SalesAmountCents  int64  `json:"sales_amount_cents"`
	LeadCount         int    `json:"lead_count"`
	ConversationCount int    `json:"conversation_count"`
	GeneratedAt       string `json:"generated_at"`
}

// CommentAutomationResponse mirrors entity.CommentAutomationSettings.
// PublicReplyText is omitted entirely when unset — "private reply only",
// see that entity's doc comment.
type CommentAutomationResponse struct {
	Enabled         bool    `json:"enabled"`
	PublicReplyText *string `json:"public_reply_text,omitempty"`
}

type UpdateCommentAutomationRequest struct {
	// No `binding:"required"` on Enabled — a required bool rejects `false`,
	// which is the exact value someone turning the feature OFF sends.
	Enabled         bool    `json:"enabled"`
	PublicReplyText *string `json:"public_reply_text"`
}

type ConnectClickRequest struct {
	MerchantID     string  `json:"merchant_id" binding:"required"`
	ServiceID      string  `json:"service_id" binding:"required"`
	MerchantUserID *string `json:"merchant_user_id"`
	// SecretKey is never echoed back — see ClickIntegrationResponse's doc
	// comment for why the response type has no field for it at all.
	SecretKey string `json:"secret_key" binding:"required"`
}

type PlanResponse struct {
	Code              string         `json:"code"`
	Name              string         `json:"name"`
	PriceMonthlyCents int            `json:"price_monthly_cents"`
	PriceYearlyCents  int            `json:"price_yearly_cents"`
	MessageLimit      *int           `json:"message_limit"`
	SeatLimit         *int           `json:"seat_limit"`
	Features          map[string]any `json:"features"`
	// SelfServe is false for a plan with no Stripe price configured for
	// either billing period (e.g. 'enterprise') — the frontend uses this to
	// show "Contact sales" instead of an "Upgrade" button.
	SelfServe bool `json:"self_serve"`
}

type SubscriptionResponse struct {
	Status             string  `json:"status"`
	PlanCode           string  `json:"plan_code"`
	PlanName           string  `json:"plan_name"`
	CurrentPeriodEnd   *string `json:"current_period_end"`
	CancelAtPeriodEnd  bool    `json:"cancel_at_period_end"`
}

type CreateCheckoutSessionRequest struct {
	PlanCode string `json:"plan_code" binding:"required"`
	// Period defaults to monthly when omitted — see the handler.
	Period string `json:"period" binding:"omitempty,oneof=monthly yearly"`
}

type CheckoutSessionResponse struct {
	URL string `json:"url"`
}

type PortalSessionResponse struct {
	URL string `json:"url"`
}

type ResponseTimePoint struct {
	Date       string   `json:"date"`
	AvgSeconds *float64 `json:"avg_seconds"`
}

type AIUsagePoint struct {
	Date          string   `json:"date"`
	ResponseCount int64    `json:"response_count"`
	TotalTokens   int64    `json:"total_tokens"`
	AvgConfidence *float64 `json:"avg_confidence"`
}

type ConversationOutcomesResponse struct {
	AIActive     int64 `json:"ai_active"`
	PendingHuman int64 `json:"pending_human"`
	HumanActive  int64 `json:"human_active"`
	Resolved     int64 `json:"resolved"`
	Closed       int64 `json:"closed"`
}

// AdminOrganizationResponse is the admin panel's per-row shape —
// OrgResponse plus the cross-tenant aggregates only a platform admin can
// see (internal/domain/repository.OrganizationSummary).
type AdminOrganizationResponse struct {
	Organization       OrgResponse `json:"organization"`
	MemberCount        int64       `json:"member_count"`
	PlanCode           *string     `json:"plan_code,omitempty"`
	SubscriptionStatus *string     `json:"subscription_status,omitempty"`
}

type AdminPlanSubscriptionCountResponse struct {
	PlanCode string `json:"plan_code"`
	PlanName string `json:"plan_name"`
	Count    int64  `json:"count"`
}

type DraftCampaignRequest struct {
	Instruction string `json:"instruction" binding:"required"`
}

// CampaignRecipientResponse mirrors campaign.RecipientPreview. Eligible is
// always true for a Telegram recipient; for Instagram it reflects Meta's
// 24-hour messaging window at the moment the draft was generated —
// IneligibleReason explains why in plain Uzbek when false, so the
// dashboard can show it directly rather than needing its own copy.
type CampaignRecipientResponse struct {
	ConversationID        string  `json:"conversation_id"`
	CustomerUsername      *string `json:"customer_username,omitempty"`
	Channel               string  `json:"channel"`
	LastCustomerMessageAt string  `json:"last_customer_message_at"`
	Eligible              bool    `json:"eligible"`
	IneligibleReason      *string `json:"ineligible_reason,omitempty"`
}

// CampaignDraftResponse mirrors campaign.CampaignDraft. Message is
// pre-filled but editable client-side — POST /v1/campaigns/send takes
// whatever the admin ends up with, not this exact string.
type CampaignDraftResponse struct {
	Message                 string                       `json:"message"`
	MinDaysSinceLastMessage int                          `json:"min_days_since_last_message"`
	MaxDaysSinceLastMessage *int                         `json:"max_days_since_last_message"`
	Channel                 string                       `json:"channel"`
	ExcludeCustomersWhoPaid bool                         `json:"exclude_customers_who_paid"`
	Recipients              []CampaignRecipientResponse `json:"recipients"`
	EligibleCount           int                          `json:"eligible_count"`
	IneligibleCount         int                          `json:"ineligible_count"`
}

type SendCampaignRequest struct {
	Message string `json:"message" binding:"required"`
	// ConversationIDs is exactly what the admin approved on the draft
	// screen — see campaign.UseCase's doc comment on why Draft/Send are
	// stateless: there's no server-side draft id, this list IS the
	// approval.
	ConversationIDs []string `json:"conversation_ids" binding:"required,min=1"`
}

// CampaignSkippedResponse mirrors campaign.SkippedRecipient.
type CampaignSkippedResponse struct {
	ConversationID string `json:"conversation_id"`
	Reason         string `json:"reason"`
}

type CampaignSendResponse struct {
	SentCount int                        `json:"sent_count"`
	Skipped   []CampaignSkippedResponse `json:"skipped"`
}

// AdminPlatformStatsResponse mirrors repository.PlatformStats exactly —
// see that struct's doc comment on MRRCentsApprox for why it's labeled
// "approx", not a precise revenue figure.
type AdminPlatformStatsResponse struct {
	TotalOrganizations  int64                                 `json:"total_organizations"`
	TotalUsers          int64                                 `json:"total_users"`
	TotalConversations  int64                                 `json:"total_conversations"`
	TotalMessages       int64                                 `json:"total_messages"`
	ActiveSubscriptions int64                                 `json:"active_subscriptions"`
	MRRCentsApprox      int64                                 `json:"mrr_cents_approx"`
	SubscriptionsByPlan []AdminPlanSubscriptionCountResponse `json:"subscriptions_by_plan"`
}

// AdminGeminiSettingsResponse deliberately has no field for the key
// itself — see platformsettings.GeminiKeyStatus's doc comment on why this
// is write-only from the HTTP layer's perspective.
type AdminGeminiSettingsResponse struct {
	Configured bool       `json:"configured"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type SetAdminGeminiSettingsRequest struct {
	APIKey string `json:"api_key" binding:"required"`
}

type InstagramConnectResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
}

type InstagramAccountResponse struct {
	ID       string  `json:"id"`
	Username *string `json:"username,omitempty"`
	Status   string  `json:"status"`
}

type TelegramConnectRequest struct {
	// BotToken is the string @BotFather gives back after /newbot — this
	// codebase never generates one itself.
	BotToken string `json:"bot_token" binding:"required"`
}

type TelegramAccountResponse struct {
	ID       string  `json:"id"`
	Username *string `json:"bot_username,omitempty"`
	Status   string  `json:"status"`
	// Paired reports whether the org has finished connecting this bot
	// inside their own Telegram app yet (entity.TelegramAccount.BusinessConnectionID
	// != nil) — the frontend settings card uses this to show "waiting for
	// you to finish pairing in Telegram" vs. "connected and receiving
	// messages".
	Paired bool `json:"paired"`
}

type ConversationResponse struct {
	ID                 string  `json:"id"`
	Status             string  `json:"status"`
	// Channel is "instagram" or "telegram" — see entity.ConversationChannel.
	// Added when Telegram support landed; not yet surfaced as a badge in
	// the frontend inbox, but available for that follow-up.
	Channel            string  `json:"channel"`
	CustomerUsername   *string `json:"customer_username,omitempty"`
	LastMessagePreview *string `json:"last_message_preview,omitempty"`
	LastMessageAt      *string `json:"last_message_at,omitempty"`
	UnreadCount        int     `json:"unread_count"`
	// AISummary/AISummaryGeneratedAt are nil until an admin generates one
	// (POST .../summary) — see entity.Conversation.AISummary's doc comment
	// on why this isn't kept live automatically.
	AISummary            *string `json:"ai_summary,omitempty"`
	AISummaryGeneratedAt *string `json:"ai_summary_generated_at,omitempty"`
}

type MessageResponse struct {
	ID          string  `json:"id"`
	Direction   string  `json:"direction"`
	SenderType  string  `json:"sender_type"`
	MessageType string  `json:"message_type"`
	Content     *string `json:"content,omitempty"`
	// AttachmentURL is omitted (nil) even when the message has an
	// attachment if the underlying URL would leak a secret — see
	// toMessageResponse's doc comment. The frontend falls back to a plain
	// "[Voice message]"-style label using MessageType alone in that case.
	AttachmentURL *string `json:"attachment_url,omitempty"`
	CreatedAt     string  `json:"created_at"`
}

// SendMessageRequest is a human agent's reply from the dashboard — see
// conversation.UseCase.SendMessage's doc comment on why this only succeeds
// once the conversation is human_active (take over first).
type SendMessageRequest struct {
	Content string `json:"content" binding:"required,min=1,max=1000"`
}

type DashboardStatsResponse struct {
	TotalConversations         int64    `json:"total_conversations"`
	AIActiveConversations      int64    `json:"ai_active_conversations"`
	PendingHumanConversations  int64    `json:"pending_human_conversations"`
	HumanActiveConversations   int64    `json:"human_active_conversations"`
	ResolvedConversations      int64    `json:"resolved_conversations"`
	ClosedConversations        int64    `json:"closed_conversations"`
	UnreadConversations        int64    `json:"unread_conversations"`
	MessagesToday              int64    `json:"messages_today"`
	ConnectedInstagramAccounts int      `json:"connected_instagram_accounts"`
	// AvgFirstResponseSeconds is null if no conversation in the trailing
	// 30 days has both an inbound message and a subsequent outbound reply
	// yet — distinct from "0 seconds".
	AvgFirstResponseSeconds *float64 `json:"avg_first_response_seconds"`
}

type DashboardTimeSeriesPoint struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// DashboardAIPerformanceResponse reflects ai_responses as it is today: this
// codebase has no AI reply pipeline implemented yet (see
// docs/DASHBOARD_MILESTONE.md), so TotalResponses is 0 and the rest null
// until that pipeline exists and starts writing rows. Real query, real
// (currently empty) data — not a mock.
type DashboardAIPerformanceResponse struct {
	TotalResponses int64    `json:"total_responses"`
	AvgConfidence  *float64 `json:"avg_confidence"`
	AvgLatencyMs   *float64 `json:"avg_latency_ms"`
	HandoffRate    *float64 `json:"handoff_rate"`
}

// DashboardNotificationResponse is an unread conversation surfaced as a
// notification item — see the doc comment on
// internal/usecase/dashboard.UseCase.Notifications for why this codebase
// doesn't have a dedicated notifications table/feed yet.
type DashboardNotificationResponse struct {
	ConversationID   string  `json:"conversation_id"`
	CustomerUsername *string `json:"customer_username,omitempty"`
	Preview          *string `json:"preview,omitempty"`
	UnreadCount      int     `json:"unread_count"`
	LastMessageAt    *string `json:"last_message_at,omitempty"`
}
