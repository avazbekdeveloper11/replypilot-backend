// Package postgres implements every domain/repository interface against
// GORM + PostgreSQL. GORM models here are intentionally separate structs
// from internal/domain/entity — the domain layer must not import gorm, so
// each repository file converts model <-> entity explicitly. It's more
// boilerplate than tagging the entity structs directly with gorm tags, but
// it means the business logic layer has zero dependency on the persistence
// framework: swapping GORM for sqlc, or Postgres for something else,
// touches only this package.
//
// Column names/types mirror database/schema.sql exactly. GORM AutoMigrate
// is never called anywhere in this codebase — schema.sql (applied via
// golang-migrate, see Makefile) is the single source of truth for the
// database structure.
package postgres

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type OrganizationModel struct {
	ID        uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	Name      string         `gorm:"column:name"`
	Slug      string         `gorm:"column:slug"`
	Status    string         `gorm:"column:status"`
	Timezone  string         `gorm:"column:timezone"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index"`
	CreatedBy *uuid.UUID     `gorm:"column:created_by;type:uuid"`
	UpdatedBy *uuid.UUID     `gorm:"column:updated_by;type:uuid"`
}

func (OrganizationModel) TableName() string { return "organizations" }

type UserModel struct {
	ID              uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	Email           string         `gorm:"column:email"`
	PasswordHash    *string        `gorm:"column:password_hash"`
	FullName        string         `gorm:"column:full_name"`
	AvatarURL       *string        `gorm:"column:avatar_url"`
	Status          string         `gorm:"column:status"`
	IsPlatformAdmin bool           `gorm:"column:is_platform_admin"`
	LastLoginAt     *time.Time     `gorm:"column:last_login_at"`
	CreatedAt       time.Time      `gorm:"column:created_at"`
	UpdatedAt       time.Time      `gorm:"column:updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (UserModel) TableName() string { return "users" }

type RoleModel struct {
	ID             uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID *uuid.UUID     `gorm:"column:organization_id;type:uuid"`
	Name           string         `gorm:"column:name"`
	Description    *string        `gorm:"column:description"`
	IsSystem       bool           `gorm:"column:is_system"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
	UpdatedAt      time.Time      `gorm:"column:updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (RoleModel) TableName() string { return "roles" }

type TeamMemberModel struct {
	ID             uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID uuid.UUID      `gorm:"column:organization_id;type:uuid"`
	UserID         uuid.UUID      `gorm:"column:user_id;type:uuid"`
	RoleID         uuid.UUID      `gorm:"column:role_id;type:uuid"`
	Status         string         `gorm:"column:status"`
	InvitedBy      *uuid.UUID     `gorm:"column:invited_by;type:uuid"`
	InvitedAt      time.Time      `gorm:"column:invited_at"`
	JoinedAt       *time.Time     `gorm:"column:joined_at"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
	UpdatedAt      time.Time      `gorm:"column:updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (TeamMemberModel) TableName() string { return "team_members" }

type InstagramAccountModel struct {
	ID                   uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID       uuid.UUID      `gorm:"column:organization_id;type:uuid"`
	IGUserID             string         `gorm:"column:ig_user_id"`
	Username             *string        `gorm:"column:username"`
	AccessTokenEncrypted []byte         `gorm:"column:access_token_encrypted"`
	TokenExpiresAt       *time.Time     `gorm:"column:token_expires_at"`
	Status               string         `gorm:"column:status"`
	WebhookSubscribed    bool           `gorm:"column:webhook_subscribed"`
	ConnectedByUserID    *uuid.UUID     `gorm:"column:connected_by_user_id;type:uuid"`
	CreatedAt            time.Time      `gorm:"column:created_at"`
	UpdatedAt            time.Time      `gorm:"column:updated_at"`
	DeletedAt            gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (InstagramAccountModel) TableName() string { return "instagram_accounts" }

// TelegramAccountModel maps telegram_accounts — see migration 000014 and
// entity.TelegramAccount's doc comment for what this table is.
type TelegramAccountModel struct {
	ID                   uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID       uuid.UUID      `gorm:"column:organization_id;type:uuid"`
	BotTokenEncrypted    []byte         `gorm:"column:bot_token_encrypted"`
	BotUsername          *string        `gorm:"column:bot_username"`
	BusinessConnectionID *string        `gorm:"column:business_connection_id"`
	Status               string         `gorm:"column:status"`
	ConnectedByUserID    *uuid.UUID     `gorm:"column:connected_by_user_id;type:uuid"`
	CreatedAt            time.Time      `gorm:"column:created_at"`
	UpdatedAt            time.Time      `gorm:"column:updated_at"`
	DeletedAt            gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (TelegramAccountModel) TableName() string { return "telegram_accounts" }

// ConversationModel's InstagramAccountID/TelegramAccountID are both
// pointer-typed at the GORM level (unlike entity.Conversation, where only
// TelegramAccountID is a pointer) purely so a nil one maps to a SQL NULL on
// write — see conversationToModel/modelToConversation for the translation
// to/from entity.Conversation's non-pointer InstagramAccountID, which stays
// uuid.Nil (never actually read) on a Telegram-channel row.
type ConversationModel struct {
	ID                  uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID      uuid.UUID      `gorm:"column:organization_id;type:uuid"`
	InstagramAccountID  *uuid.UUID     `gorm:"column:instagram_account_id;type:uuid"`
	TelegramAccountID   *uuid.UUID     `gorm:"column:telegram_account_id;type:uuid"`
	Channel             string         `gorm:"column:channel"`
	CustomerIGID        string         `gorm:"column:customer_ig_id"`
	CustomerUsername    *string        `gorm:"column:customer_username"`
	Status              string         `gorm:"column:status"`
	AssignedUserID      *uuid.UUID     `gorm:"column:assigned_user_id;type:uuid"`
	LastMessageAt       *time.Time     `gorm:"column:last_message_at"`
	LastMessagePreview  *string        `gorm:"column:last_message_preview"`
	UnreadCount         int            `gorm:"column:unread_count"`
	CreatedAt           time.Time      `gorm:"column:created_at"`
	UpdatedAt           time.Time      `gorm:"column:updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (ConversationModel) TableName() string { return "conversations" }

// MessageModel maps to a table partitioned by RANGE(created_at). Postgres
// requires a partitioned table's primary key to include the partition key,
// so the primary key here is composite (id, created_at) — matching
// database/schema.sql exactly, not a simplification.
type MessageModel struct {
	ID             uuid.UUID  `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID uuid.UUID  `gorm:"column:organization_id;type:uuid"`
	ConversationID uuid.UUID  `gorm:"column:conversation_id;type:uuid"`
	Direction      string     `gorm:"column:direction"`
	SenderType     string     `gorm:"column:sender_type"`
	SenderUserID   *uuid.UUID `gorm:"column:sender_user_id;type:uuid"`
	MessageType    string     `gorm:"column:message_type"`
	Content        *string    `gorm:"column:content"`
	AttachmentURL  *string    `gorm:"column:attachment_url"`
	IGMessageID    *string    `gorm:"column:ig_message_id"`
	Metadata       []byte     `gorm:"column:metadata;type:jsonb"`
	CreatedAt      time.Time  `gorm:"column:created_at;primaryKey"`
	DeletedAt      *time.Time `gorm:"column:deleted_at"`
}

func (MessageModel) TableName() string { return "messages" }

// WebhookLogModel maps to a table partitioned by RANGE(received_at); same
// composite-primary-key reasoning as MessageModel above.
type WebhookLogModel struct {
	ID             uuid.UUID  `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID *uuid.UUID `gorm:"column:organization_id;type:uuid"`
	Source         string     `gorm:"column:source"`
	EventType      *string    `gorm:"column:event_type"`
	Payload        []byte     `gorm:"column:payload;type:jsonb"`
	SignatureValid bool       `gorm:"column:signature_valid"`
	Status         string     `gorm:"column:status"`
	ErrorMessage   *string    `gorm:"column:error_message"`
	ReceivedAt     time.Time  `gorm:"column:received_at;primaryKey"`
	ProcessedAt    *time.Time `gorm:"column:processed_at"`
}

func (WebhookLogModel) TableName() string { return "webhook_logs" }

type KnowledgeDocumentModel struct {
	ID             uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID uuid.UUID      `gorm:"column:organization_id;type:uuid"`
	Title          string         `gorm:"column:title"`
	SourceType     string         `gorm:"column:source_type"`
	FileURL        *string        `gorm:"column:file_url"`
	Content        *string        `gorm:"column:content"`
	Status         string         `gorm:"column:status"`
	ErrorMessage   *string        `gorm:"column:error_message"`
	UploadedBy     *uuid.UUID     `gorm:"column:uploaded_by;type:uuid"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
	UpdatedAt      time.Time      `gorm:"column:updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (KnowledgeDocumentModel) TableName() string { return "knowledge_base_documents" }

// KnowledgeChunkModel deliberately omits the `embedding` column — GORM
// never reads or writes it through this struct. It's set via one raw SQL
// INSERT ( ...::vector) at chunk-creation time and read via raw SQL in
// similarity search queries (ORDER BY embedding <=> $1::vector). See
// postgres/knowledge_chunk_repository.go's doc comment for why there's no
// pgvector Go driver dependency to map it onto a Go field instead.
type KnowledgeChunkModel struct {
	ID             uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID uuid.UUID      `gorm:"column:organization_id;type:uuid"`
	DocumentID     uuid.UUID      `gorm:"column:document_id;type:uuid"`
	ChunkIndex     int            `gorm:"column:chunk_index"`
	Content        string         `gorm:"column:content"`
	TokenCount     *int           `gorm:"column:token_count"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
	UpdatedAt      time.Time      `gorm:"column:updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (KnowledgeChunkModel) TableName() string { return "knowledge_base_chunks" }

// AIResponseModel deliberately omits TotalTokens — it's a Postgres
// GENERATED ALWAYS AS (prompt_tokens + completion_tokens) STORED column
// (see migrations/000001), so GORM never writes it and this repository has
// no need to read it back immediately after insert.
type AIResponseModel struct {
	ID                  uuid.UUID  `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID      uuid.UUID  `gorm:"column:organization_id;type:uuid"`
	ConversationID      uuid.UUID  `gorm:"column:conversation_id;type:uuid"`
	MessageID           uuid.UUID  `gorm:"column:message_id;type:uuid"`
	MessageCreatedAt    time.Time  `gorm:"column:message_created_at"`
	ModelUsed           string     `gorm:"column:model_used"`
	PromptTokens        int        `gorm:"column:prompt_tokens"`
	CompletionTokens    int        `gorm:"column:completion_tokens"`
	ConfidenceScore     *float64   `gorm:"column:confidence_score"`
	WasHandoffTriggered bool       `gorm:"column:was_handoff_triggered"`
	LatencyMs           *int       `gorm:"column:latency_ms"`
	CreatedAt           time.Time  `gorm:"column:created_at"`
}

func (AIResponseModel) TableName() string { return "ai_responses" }

type AIResponseCitationModel struct {
	AIResponseID    uuid.UUID `gorm:"column:ai_response_id;type:uuid;primaryKey"`
	KnowledgeChunkID uuid.UUID `gorm:"column:kb_chunk_id;type:uuid;primaryKey"`
	OrganizationID  uuid.UUID `gorm:"column:organization_id;type:uuid"`
	SimilarityScore *float64  `gorm:"column:similarity_score"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

func (AIResponseCitationModel) TableName() string { return "ai_response_citations" }

// PlanModel has no organization_id / RLS — plans is global reference data
// (seeded in migrations/000002), not a tenant-scoped table. Features is
// raw jsonb bytes, converted to/from map[string]any at the repository
// boundary, same convention as MessageModel.Metadata.
type PlanModel struct {
	ID                   uuid.UUID `gorm:"column:id;type:uuid;primaryKey"`
	Code                 string    `gorm:"column:code"`
	Name                 string    `gorm:"column:name"`
	PriceMonthlyCents    int       `gorm:"column:price_monthly_cents"`
	PriceYearlyCents     int       `gorm:"column:price_yearly_cents"`
	MessageLimit         *int      `gorm:"column:message_limit"`
	SeatLimit            *int      `gorm:"column:seat_limit"`
	Features             []byte    `gorm:"column:features;type:jsonb"`
	StripePriceIDMonthly *string   `gorm:"column:stripe_price_id_monthly"`
	StripePriceIDYearly  *string   `gorm:"column:stripe_price_id_yearly"`
	IsActive             bool      `gorm:"column:is_active"`
	CreatedAt            time.Time `gorm:"column:created_at"`
	UpdatedAt            time.Time `gorm:"column:updated_at"`
}

func (PlanModel) TableName() string { return "plans" }

type SubscriptionModel struct {
	ID                   uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID       uuid.UUID      `gorm:"column:organization_id;type:uuid"`
	PlanID               uuid.UUID      `gorm:"column:plan_id;type:uuid"`
	StripeSubscriptionID *string        `gorm:"column:stripe_subscription_id"`
	StripeCustomerID     *string        `gorm:"column:stripe_customer_id"`
	Status               string         `gorm:"column:status"`
	CurrentPeriodStart   *time.Time     `gorm:"column:current_period_start"`
	CurrentPeriodEnd     *time.Time     `gorm:"column:current_period_end"`
	CancelAtPeriodEnd    bool           `gorm:"column:cancel_at_period_end"`
	CreatedAt            time.Time      `gorm:"column:created_at"`
	UpdatedAt            time.Time      `gorm:"column:updated_at"`
	DeletedAt            gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (SubscriptionModel) TableName() string { return "subscriptions" }

type ProductModel struct {
	ID             uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID uuid.UUID      `gorm:"column:organization_id;type:uuid"`
	Name           string         `gorm:"column:name"`
	Description    *string        `gorm:"column:description"`
	PriceCents     int64          `gorm:"column:price_cents"`
	Currency       string         `gorm:"column:currency"`
	IsActive       bool           `gorm:"column:is_active"`
	CreatedAt      time.Time      `gorm:"column:created_at"`
	UpdatedAt      time.Time      `gorm:"column:updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (ProductModel) TableName() string { return "products" }

type ClickIntegrationModel struct {
	ID                uuid.UUID      `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID    uuid.UUID      `gorm:"column:organization_id;type:uuid"`
	MerchantID        string         `gorm:"column:merchant_id"`
	ServiceID         string         `gorm:"column:service_id"`
	MerchantUserID    *string        `gorm:"column:merchant_user_id"`
	ConnectedByUserID *uuid.UUID     `gorm:"column:connected_by_user_id;type:uuid"`
	CreatedAt         time.Time      `gorm:"column:created_at"`
	UpdatedAt         time.Time      `gorm:"column:updated_at"`
	DeletedAt         gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (ClickIntegrationModel) TableName() string { return "click_integrations" }

// LeadModel maps to dm_leads, not leads: migration 000001 already owns an
// unrelated "leads" table (dead CRM-style schema, no Go code behind it) —
// see 000013_leads.up.sql's doc comment for the full story.
type LeadModel struct {
	ID             uuid.UUID `gorm:"column:id;type:uuid;primaryKey"`
	OrganizationID uuid.UUID `gorm:"column:organization_id;type:uuid"`
	ConversationID uuid.UUID `gorm:"column:conversation_id;type:uuid"`
	Phone          string    `gorm:"column:phone"`
	Summary        string    `gorm:"column:summary"`
	Status         string    `gorm:"column:status"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (LeadModel) TableName() string { return "dm_leads" }
