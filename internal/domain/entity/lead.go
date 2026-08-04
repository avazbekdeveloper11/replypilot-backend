package entity

import (
	"time"

	"github.com/google/uuid"
)

type LeadStatus string

const (
	// LeadStatusNew: just captured, nobody has acted on it yet. This is
	// the "needs attention" state the Leads dashboard page filters on by
	// default.
	LeadStatusNew LeadStatus = "new"
	// LeadStatusContacted: a human has reached out (called, messaged)
	// but the outcome isn't final yet.
	LeadStatusContacted LeadStatus = "contacted"
	// LeadStatusDone: fully handled — sold, declined, or otherwise
	// closed out. Terminal, same idea as Conversation's resolved status
	// but deliberately a separate concept — see this file's package
	// doc comment on why leads and conversation status don't share one
	// state machine.
	LeadStatusDone LeadStatus = "done"
)

// Lead is a customer who left a phone number in an Instagram DM — the
// moment a conversation stops being "just chat" and becomes something a
// human needs to actually act on (call them, arrange delivery, close the
// sale). Captured automatically by internal/usecase/ai's phone-number
// detection (see that package's captureLeadIfPresent), never by a human
// typing one in.
//
// Deliberately its own entity/state machine, not a reuse of
// Conversation.Status: the AI keeps replying to the customer's questions
// after a lead is captured (see ai.UseCase's package doc comment on why
// pending_human — which stops the AI entirely — was the wrong fit here).
// A lead's own status (new -> contacted -> done) tracks whether a human
// has followed up, completely independent of who's currently driving the
// DM thread.
type Lead struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	ConversationID uuid.UUID
	// Phone is normalized to +998XXXXXXXXX where the source text made
	// that unambiguous (bare 9-digit, 0-prefixed 10-digit, or already
	// +998/998-prefixed) — see ai package's extractPhone. Not a secret,
	// stored in plain text like CustomerUsername.
	Phone string
	// Summary is Gemini's best-effort one-two-sentence answer to "what
	// does this customer want and what should the team do" — generated
	// once, at capture time, from the conversation transcript so far.
	// Falls back to a generic "review the conversation" line if that
	// generation call fails; never blocks lead capture itself.
	Summary string
	Status  LeadStatus
	// CustomerUsername is NOT a column on leads — it's the conversation's
	// customer_username, joined in by
	// postgres.LeadRepository.ListByOrganization/FindByID purely so the
	// Leads dashboard page can show who a lead actually is without a
	// second round trip per row. Never set on a Lead that came from
	// Create/UpdateStatus.
	CustomerUsername *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
