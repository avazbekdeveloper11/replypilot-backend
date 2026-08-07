// Package product is the CRUD behind an organization's own sellable-item
// catalog — see entity.Product's doc comment for why this exists (the AI
// reply pipeline needs a structured price list, not another RAG document).
package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/internal/integration/geminiapi"
)

const defaultCurrency = "UZS"

// Generator mirrors internal/usecase/campaign's / internal/usecase/ai's —
// declared separately per this codebase's established "usecases don't
// depend on each other" convention. Only Import uses this; Create, Update,
// List, Get, Delete never touch Gemini.
type Generator interface {
	Generate(ctx context.Context, systemPrompt, userMessage string) (string, geminiapi.GenerateUsage, error)
}

type UseCase struct {
	repo      repository.ProductRepository
	generator Generator
}

// generator may be nil (e.g. a caller that never needs Import) — Import
// itself guards against that; Create/Update/List/Get/Delete never
// dereference it, so a nil generator is safe everywhere except Import.
func New(repo repository.ProductRepository, generator Generator) *UseCase {
	return &UseCase{repo: repo, generator: generator}
}

type CreateInput struct {
	OrganizationID uuid.UUID
	Name           string
	Description    *string
	// PriceCents nil means "price on request" — see entity.Product's doc
	// comment. Not "0 unless told otherwise": a caller that wants an
	// explicit free/zero price must say so explicitly via a non-nil 0.
	PriceCents *int64
	Currency   string
}

func (uc *UseCase) Create(ctx context.Context, in CreateInput) (*entity.Product, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperror.InvalidInput("product name is required", nil)
	}
	if in.PriceCents != nil && *in.PriceCents < 0 {
		return nil, apperror.InvalidInput("price cannot be negative", nil)
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = defaultCurrency
	}

	p := &entity.Product{
		OrganizationID: in.OrganizationID,
		Name:           name,
		Description:    in.Description,
		PriceCents:     in.PriceCents,
		Currency:       currency,
		IsActive:       true,
	}
	if err := uc.repo.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (uc *UseCase) List(ctx context.Context, orgID uuid.UUID) ([]*entity.Product, error) {
	return uc.repo.ListByOrganization(ctx, orgID)
}

func (uc *UseCase) Get(ctx context.Context, orgID, id uuid.UUID) (*entity.Product, error) {
	return uc.repo.FindByID(ctx, orgID, id)
}

type UpdateInput struct {
	OrganizationID uuid.UUID
	ID             uuid.UUID
	Name           string
	Description    *string
	// PriceCents nil means "price on request" — same convention as
	// CreateInput.PriceCents. An update that should CLEAR a previously-set
	// price must pass nil explicitly; there's no separate "don't touch
	// price" signal here, matching every other field on this struct
	// (Update always replaces the whole record, never patches).
	PriceCents *int64
	Currency   string
	IsActive   bool
}

func (uc *UseCase) Update(ctx context.Context, in UpdateInput) (*entity.Product, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperror.InvalidInput("product name is required", nil)
	}
	if in.PriceCents != nil && *in.PriceCents < 0 {
		return nil, apperror.InvalidInput("price cannot be negative", nil)
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = defaultCurrency
	}

	existing, err := uc.repo.FindByID(ctx, in.OrganizationID, in.ID)
	if err != nil {
		return nil, err
	}
	existing.Name = name
	existing.Description = in.Description
	existing.PriceCents = in.PriceCents
	existing.Currency = currency
	existing.IsActive = in.IsActive

	if err := uc.repo.Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

func (uc *UseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	return uc.repo.Delete(ctx, orgID, id)
}

// maxImportRows bounds both the Gemini prompt size/cost and the number of
// sequential repo.Create calls one Import can trigger (there's no
// ProductRepository.CreateBatch — see repository.ProductRepository's
// interface). Generous enough for a real shop's catalog, small enough that
// someone accidentally uploading a 50,000-row export doesn't blow up the
// prompt or this request's latency. Rows beyond the cap are dropped and
// folded into ImportResult.SkippedRows, not silently lost without a trace.
const maxImportRows = 300

// ImportInput is the raw spreadsheet grid the HTTP handler already
// extracted from the uploaded .xlsx via excelize — one []string per row,
// in column order, exactly as read (ragged rows are fine). Column
// headers/order/language are deliberately NOT assumed anywhere in this
// package — that's the entire point of routing this through Gemini instead
// of a fixed-column CSV importer.
type ImportInput struct {
	OrganizationID uuid.UUID
	Rows           [][]string
}

// ImportResult reports what actually got created plus how much of the
// uploaded file didn't make it in, so the caller can show something more
// useful than a bare success toast.
type ImportResult struct {
	Created []*entity.Product
	// SkippedRows counts rows beyond maxImportRows, rows Gemini returned
	// with no usable name, rows with a negative price, and rows that failed
	// to insert — one counter, not four, since the frontend only needs "how
	// many didn't make it," not a breakdown.
	SkippedRows int
	// TotalRowsRead is len(in.Rows) before any capping, so the caller can
	// report e.g. "142 / 300 qatordan qo'shildi" instead of just a count.
	TotalRowsRead int
}

// importRowJSON is Gemini's per-product output shape for Import — see
// importSystemPrompt. Price is a plain so'm number (or null for "no
// price"/"price on request"), NOT price_cents: the so'm-to-tiyin
// multiplication happens once, deterministically, in Go (see Import) —
// an LLM is good at picking out which column is the price, not at
// reliably getting a unit conversion right on every single row.
type importRowJSON struct {
	Name  string   `json:"name"`
	Price *float64 `json:"price"`
}

// importSystemPrompt asks Gemini to read an arbitrary product spreadsheet
// (unknown column names/order/language — "Nomi"/"Narxi",
// "Название"/"Цена", "Name"/"Price", extra columns, a header row or none)
// and extract exactly one {name, price} pair per real product row. Mirrors
// campaign.draftSystemPrompt's "demand bare JSON, no fences" convention —
// see parseImportRowsJSON for the same defensive fence-stripping.
const importSystemPrompt = `Sizga Excel jadvalidan olingan qatorlar TSV (tab bilan ajratilgan) formatda beriladi. Bu — do'kon egasi yuklagan mahsulotlar ro'yxati, lekin ustunlar nomi, tartibi va tili turlicha bo'lishi mumkin (masalan "Nomi"/"Narxi", "Название"/"Цена", "Name"/"Price", yoki umuman sarlavhasiz). Ba'zi qatorlar sarlavha, bo'sh qator yoki mahsulotga aloqasi yo'q axlat bo'lishi mumkin — ularni o'tkazib yuboring.

Sizning vazifangiz: har bir HAQIQIY mahsulot qatoridan mahsulot nomini va narxini (agar bo'lsa) aniqlash.

Faqat quyidagi JSON formatida javob bering — hech qanday izoh, hech qanday tushuntirish, hech qanday ` + "```" + ` belgisi, faqat toza JSON massiv:

[{"name": "<mahsulot nomi>", "price": <son yoki null>}, ...]

Qoidalar:
- price — oddiy son, so'mda (masalan 150000), tiyin yoki valyuta belgisisiz. Agar narx ustuni bo'sh, "so'rov asosida", "kelishiladi" yoki shunga o'xshash bo'lsa, price=null qo'ying — 0 EMAS.
- name bo'sh yoki aniqlab bo'lmaydigan qatorni butunlay o'tkazib yuboring (natija massivida ko'rsatmang).
- Sarlavha qatorini (masalan "Nomi", "Narxi" so'zlarining o'zi) natijaga qo'shmang.
- Bitta qatordan bitta mahsulot chiqaring — qatorlarni birlashtirmang yoki bo'lmang.`

// Import turns an uploaded spreadsheet's rows into real products: sends
// the grid to Gemini as TSV, asks it to pick out name+price per row
// (column layout unknown/untrusted), then creates one entity.Product per
// row it returns. Every created product lands active with the default
// currency, matching Create's own behavior — there's no bulk "review
// before committing" step here, unlike campaign.UseCase.Draft/Send,
// because a wrong product row is a low-stakes, easily-deleted mistake, not
// an outbound message that already reached a real customer.
func (uc *UseCase) Import(ctx context.Context, in ImportInput) (*ImportResult, error) {
	if uc.generator == nil {
		return nil, apperror.InvalidInput("import is not configured", nil)
	}
	totalRows := len(in.Rows)
	if totalRows == 0 {
		return nil, apperror.InvalidInput("the uploaded file has no rows", nil)
	}

	rows := in.Rows
	skipped := 0
	if len(rows) > maxImportRows {
		skipped = len(rows) - maxImportRows
		rows = rows[:maxImportRows]
	}

	raw, _, err := uc.generator.Generate(ctx, importSystemPrompt, rowsToTSV(rows))
	if err != nil {
		return nil, apperror.Internal("import products", err)
	}

	parsed, err := parseImportRowsJSON(raw)
	if err != nil {
		return nil, apperror.Internal("import products", fmt.Errorf("gemini returned an unparseable product list: %w", err))
	}

	result := &ImportResult{TotalRowsRead: totalRows, SkippedRows: skipped}
	for _, row := range parsed {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			result.SkippedRows++
			continue
		}

		var priceCents *int64
		if row.Price != nil {
			if *row.Price < 0 {
				result.SkippedRows++
				continue
			}
			// Round half up to the nearest tiyin — prices are never
			// negative here (checked above), so this never needs to round
			// toward zero for a negative value.
			cents := int64(*row.Price*100 + 0.5)
			priceCents = &cents
		}

		p := &entity.Product{
			OrganizationID: in.OrganizationID,
			Name:           name,
			PriceCents:     priceCents,
			Currency:       defaultCurrency,
			IsActive:       true,
		}
		if err := uc.repo.Create(ctx, p); err != nil {
			result.SkippedRows++
			continue
		}
		result.Created = append(result.Created, p)
	}

	return result, nil
}

// rowsToTSV renders the spreadsheet grid as tab-separated text — compact,
// unambiguous for Gemini to read column-by-column, and doesn't need the
// escaping CSV would for cells containing commas (product names often
// have them).
func rowsToTSV(rows [][]string) string {
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.Join(row, "\t"))
	}
	return b.String()
}

// parseImportRowsJSON mirrors campaign.parseCampaignSegmentJSON's
// fence-stripping — Gemini occasionally wraps its JSON in ```json despite
// importSystemPrompt explicitly asking it not to.
func parseImportRowsJSON(raw string) ([]importRowJSON, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return nil, errors.New("empty response")
	}

	var rows []importRowJSON
	if err := json.Unmarshal([]byte(cleaned), &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
