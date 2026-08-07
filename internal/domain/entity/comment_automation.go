package entity

import (
	"time"

	"github.com/google/uuid"
)

// CommentAutomationSettings is one organization's configuration for
// comment-to-DM automation — see migration 000017's header comment for
// what the feature does and the two Meta constraints that shape it.
//
// Off by default (Enabled=false): auto-DMing everyone who comments on a
// post is a meaningful change in how a business talks to its audience, and
// silently switching it on for every existing org at deploy time would be
// the wrong default. An org with no row at all is treated exactly like
// Enabled=false.
//
// PublicReplyText is nil/empty for "private reply only". When set, it's
// posted verbatim as a public reply under the comment — never
// AI-generated, since a public reply is permanent and visible to everyone.
type CommentAutomationSettings struct {
	OrganizationID  uuid.UUID
	Enabled         bool
	PublicReplyText *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ProcessedComment records that this org has already sent a private reply
// for one Instagram comment. Exists purely as an idempotency guard around
// Meta's one-private-reply-per-comment-ever rule — see
// metaapi.Client.SendPrivateReply's doc comment. It carries no link to the
// resulting conversation, deliberately: see migration 000017's comment on
// the table.
type ProcessedComment struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	IGCommentID    string
	CreatedAt      time.Time
}
