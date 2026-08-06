package clickapi

import (
	"crypto/md5" //nolint:gosec // MD5 is Click's own documented signature algorithm, not a choice made here — see VerifySignature.
	"encoding/hex"
	"fmt"
	"math"
	"strconv"

	"github.com/google/uuid"
)

// Click's Shop API action codes — the `action` field on every
// Prepare/Complete callback. https://docs.click.uz/en/merchant-api-request/
const (
	ActionPrepare  = 0
	ActionComplete = 1
)

// Click's own documented error codes for a merchant's Prepare/Complete
// response body. Cross-checked against a widely-used third-party reference
// implementation of the same protocol (Click's own docs site is a
// JS-rendered SPA that doesn't expose these to a plain HTTP fetch —
// https://gist.github.com/uzbekdev1/c46761106460e85eb6ade443f656e1cb is the
// same field list/error codes every PHP/Node Click integration in the wild
// implements against).
const (
	ErrSuccess              = 0
	ErrSignCheckFailed      = -1
	ErrIncorrectAmount      = -2
	ErrActionNotFound       = -3
	ErrAlreadyPaid          = -4
	ErrOrderNotFound        = -5
	ErrTransactionNotFound  = -6
	ErrBadRequest           = -8
	ErrTransactionCancelled = -9
)

// WebhookRequest is the merchant-side view of one Click Prepare or Complete
// callback. Click posts this as application/x-www-form-urlencoded, not
// JSON — field tags match gin's form binding (c.ShouldBind against a
// x-www-form-urlencoded body).
type WebhookRequest struct {
	ClickTransID  int64  `form:"click_trans_id"`
	ServiceID     string `form:"service_id"`
	ClickPaydocID int64  `form:"click_paydoc_id"`
	// MerchantTransID is this codebase's own click_transaction_param,
	// echoed back by Click on every callback for the same transaction.
	MerchantTransID string `form:"merchant_trans_id"`
	// MerchantPrepareID is only present on a Complete callback (action=1)
	// — it's whatever this merchant returned as merchant_prepare_id in its
	// own Prepare response (see PrepareResponse), echoed back so Complete
	// can be matched to the right Prepare. Included in Complete's signature
	// (see VerifySignature) but not Prepare's, since it doesn't exist yet
	// when Prepare is being verified.
	MerchantPrepareID string `form:"merchant_prepare_id"`
	Amount            string `form:"amount"`
	Action            int    `form:"action"`
	Error             int    `form:"error"`
	ErrorNote         string `form:"error_note"`
	SignTime          string `form:"sign_time"`
	SignString        string `form:"sign_string"`
}

// VerifySignature recomputes Click's MD5 signature and compares it against
// SignString. The field order, and whether MerchantPrepareID is included,
// exactly matches Click's documented formula: Prepare (action=0) hashes
// click_trans_id+service_id+secret_key+merchant_trans_id+amount+action+sign_time;
// Complete (action=1) additionally inserts merchant_prepare_id before
// amount. Not constant-time — an MD5 comparison timing side-channel here
// would leak at most "how many leading hex chars matched" of a value this
// package computed independently from a per-org secret Click also holds;
// the actual security boundary is the secret key itself, encrypted at rest
// (see entity.ClickIntegration.SecretKeyEncrypted).
func (r WebhookRequest) VerifySignature(secretKey string) bool {
	var raw string
	if r.Action == ActionComplete {
		raw = fmt.Sprintf("%d%s%s%s%s%s%d%s",
			r.ClickTransID, r.ServiceID, secretKey, r.MerchantTransID, r.MerchantPrepareID, r.Amount, r.Action, r.SignTime)
	} else {
		raw = fmt.Sprintf("%d%s%s%s%s%d%s",
			r.ClickTransID, r.ServiceID, secretKey, r.MerchantTransID, r.Amount, r.Action, r.SignTime)
	}
	sum := md5.Sum([]byte(raw)) //nolint:gosec // see the import comment above
	computed := hex.EncodeToString(sum[:])
	return computed == r.SignString
}

// AmountMatches compares the callback's amount (a decimal string, e.g.
// "150000.00") against an expected price_cents value — guards against a
// stale payment link being paid after the product's price changed, or a
// tampered amount. Compared as parsed floats with a sub-tiyin tolerance
// rather than exact string equality, since Click's own formatting of a
// whole-number amount ("150000" vs "150000.00") isn't guaranteed to match
// FormatAmount's output byte-for-byte.
func (r WebhookRequest) AmountMatches(priceCents int64) bool {
	got, err := strconv.ParseFloat(r.Amount, 64)
	if err != nil {
		return false
	}
	want, err := strconv.ParseFloat(FormatAmount(priceCents), 64)
	if err != nil {
		return false
	}
	return math.Abs(got-want) < 0.01
}

// PrepareResponse is the JSON body a merchant must return for a Prepare
// callback (action=0). MerchantPrepareID is this merchant's own id for the
// (not-yet-confirmed) transaction — internal/usecase/payment sets it to the
// order's own id, which Click then echoes back unchanged on the matching
// Complete callback (see WebhookRequest.MerchantPrepareID).
type PrepareResponse struct {
	ClickTransID      int64  `json:"click_trans_id"`
	MerchantTransID   string `json:"merchant_trans_id"`
	MerchantPrepareID string `json:"merchant_prepare_id,omitempty"`
	Error             int    `json:"error"`
	ErrorNote         string `json:"error_note"`
}

// CompleteResponse is the JSON body a merchant must return for a Complete
// callback (action=1).
type CompleteResponse struct {
	ClickTransID      int64  `json:"click_trans_id"`
	MerchantTransID   string `json:"merchant_trans_id"`
	MerchantConfirmID string `json:"merchant_confirm_id,omitempty"`
	Error             int    `json:"error"`
	ErrorNote         string `json:"error_note"`
}

// uuidStrLen is the fixed length of uuid.UUID.String()'s canonical
// 8-4-4-4-12 hex form — always exactly this many characters, which is what
// makes BuildTransactionParam/ParseTransactionParam a safe split despite a
// UUID's own string form containing hyphens.
const uuidStrLen = 36

// BuildTransactionParam is the single place "{conversationID}-{productID}"
// gets constructed — internal/usecase/ai.buildProductContext calls this
// (rather than formatting the string inline) so this file's
// ParseTransactionParam can never drift out of sync with it.
func BuildTransactionParam(conversationID, productID uuid.UUID) string {
	return conversationID.String() + "-" + productID.String()
}

// ParseTransactionParam reverses BuildTransactionParam. A naive
// strings.Split on "-" would be ambiguous (each UUID's own canonical string
// form already contains four hyphens), so this instead relies on
// uuid.UUID.String() always producing exactly uuidStrLen characters: the
// first uuidStrLen bytes are the conversation id, byte uuidStrLen must be
// the separator, and the remaining uuidStrLen bytes are the product id.
func ParseTransactionParam(s string) (conversationID, productID uuid.UUID, err error) {
	const wantLen = uuidStrLen*2 + 1
	if len(s) != wantLen || s[uuidStrLen] != '-' {
		return uuid.Nil, uuid.Nil, fmt.Errorf("clickapi: malformed transaction param %q", s)
	}
	conversationID, err = uuid.Parse(s[:uuidStrLen])
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("clickapi: parse conversation id from transaction param: %w", err)
	}
	productID, err = uuid.Parse(s[uuidStrLen+1:])
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("clickapi: parse product id from transaction param: %w", err)
	}
	return conversationID, productID, nil
}
