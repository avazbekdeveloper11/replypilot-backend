package v1

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

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
