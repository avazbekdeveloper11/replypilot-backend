package v1

import (
	"errors"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/product"
)

type ProductHandler struct {
	uc *product.UseCase
}

func NewProductHandler(uc *product.UseCase) *ProductHandler {
	return &ProductHandler{uc: uc}
}

// List godoc
// @Summary      List products
// @Tags         products
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=[]ProductResponse}
// @Router       /v1/products [get]
func (h *ProductHandler) List(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	products, err := h.uc.List(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]ProductResponse, 0, len(products))
	for _, p := range products {
		out = append(out, toProductResponse(p))
	}
	response.OK(c, out)
}

// Create godoc
// @Summary      Create a product
// @Tags         products
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body CreateProductRequest true "Product"
// @Success      201 {object} response.Envelope{data=ProductResponse}
// @Router       /v1/products [post]
func (h *ProductHandler) Create(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req CreateProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	p, err := h.uc.Create(c.Request.Context(), product.CreateInput{
		OrganizationID: orgID,
		Name:           req.Name,
		Description:    req.Description,
		PriceCents:     req.PriceCents,
		Currency:       req.Currency,
	})
	if err != nil {
		c.Error(err)
		return
	}
	response.Created(c, toProductResponse(p))
}

// maxImportFileBytes bounds the uploaded .xlsx before it's even opened —
// this codebase has no global multipart size limit (Gin's default 32MB
// in-memory threshold is the only implicit ceiling; see router.go), so
// each upload endpoint that isn't plain text guards its own. 10MB
// comfortably covers a multi-thousand-row product catalog while keeping
// one bad upload from parking a large buffer in memory.
const maxImportFileBytes = 10 << 20 // 10MB

// Import godoc
// @Summary      Bulk-import products from an uploaded Excel file
// @Description  multipart/form-data: `file` (.xlsx). Column names/order/language aren't assumed — Gemini reads the sheet and figures out which column is the product name and which is the price. See product.UseCase.Import's doc comment.
// @Tags         products
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        file formData file true ".xlsx file"
// @Success      201 {object} response.Envelope{data=ProductImportResponse}
// @Router       /v1/products/import [post]
func (h *ProductHandler) Import(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	fileHeader, ferr := c.FormFile("file")
	if ferr != nil {
		c.Error(apperror.InvalidInput("file is required", nil))
		return
	}
	if fileHeader.Size > maxImportFileBytes {
		c.Error(apperror.InvalidInput("file is too large (max 10MB)", nil))
		return
	}

	file, oerr := fileHeader.Open()
	if oerr != nil {
		c.Error(apperror.Internal("open uploaded file", oerr))
		return
	}
	defer file.Close()

	rows, perr := parseXLSXRows(file)
	if perr != nil {
		c.Error(apperror.InvalidInput("couldn't read the uploaded file — make sure it's a valid .xlsx", perr))
		return
	}

	result, err := h.uc.Import(c.Request.Context(), product.ImportInput{
		OrganizationID: orgID,
		Rows:           rows,
	})
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]ProductResponse, 0, len(result.Created))
	for _, p := range result.Created {
		out = append(out, toProductResponse(p))
	}
	response.Created(c, ProductImportResponse{
		Created:       out,
		CreatedCount:  len(out),
		SkippedRows:   result.SkippedRows,
		TotalRowsRead: result.TotalRowsRead,
	})
}

// parseXLSXRows reads every row of an .xlsx file's first sheet into a
// plain [][]string grid — no header/column assumptions made here at all;
// figuring out which column means what is entirely product.UseCase.Import
// (via Gemini)'s job, not this function's. Excelize sometimes includes
// blank trailing rows/cells within a sheet's recorded used-range; Import's
// Gemini prompt is explicitly told to skip blank rows rather than this
// function trying to trim them itself.
func parseXLSXRows(r io.Reader) ([][]string, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, errors.New("workbook has no sheets")
	}
	return f.GetRows(sheets[0])
}

// Update godoc
// @Summary      Update a product
// @Tags         products
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id      path string                true "Product ID"
// @Param        request body UpdateProductRequest   true "Product"
// @Success      200 {object} response.Envelope{data=ProductResponse}
// @Router       /v1/products/{id} [patch]
func (h *ProductHandler) Update(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid product id", err))
		return
	}

	var req UpdateProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	p, err := h.uc.Update(c.Request.Context(), product.UpdateInput{
		OrganizationID: orgID,
		ID:             id,
		Name:           req.Name,
		Description:    req.Description,
		PriceCents:     req.PriceCents,
		Currency:       req.Currency,
		IsActive:       req.IsActive,
	})
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toProductResponse(p))
}

// Delete godoc
// @Summary      Delete a product
// @Tags         products
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Product ID"
// @Success      200 {object} response.Envelope{data=object}
// @Router       /v1/products/{id} [delete]
func (h *ProductHandler) Delete(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid product id", err))
		return
	}

	if err := h.uc.Delete(c.Request.Context(), orgID, id); err != nil {
		c.Error(err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

func toProductResponse(p *entity.Product) ProductResponse {
	return ProductResponse{
		ID:          p.ID.String(),
		Name:        p.Name,
		Description: p.Description,
		PriceCents:  p.PriceCents,
		Currency:    p.Currency,
		IsActive:    p.IsActive,
		CreatedAt:   p.CreatedAt.Format(time.RFC3339),
	}
}
