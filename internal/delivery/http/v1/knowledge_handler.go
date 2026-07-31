package v1

import (
	"io"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/knowledgebase"
)

type KnowledgeHandler struct {
	uc *knowledgebase.UseCase
}

func NewKnowledgeHandler(uc *knowledgebase.UseCase) *KnowledgeHandler {
	return &KnowledgeHandler{uc: uc}
}

// List godoc
// @Summary      List knowledge base documents
// @Tags         knowledge-base
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=[]KnowledgeDocumentResponse}
// @Router       /v1/knowledge-base/documents [get]
func (h *KnowledgeHandler) List(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	docs, err := h.uc.List(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]KnowledgeDocumentResponse, 0, len(docs))
	for _, d := range docs {
		out = append(out, toKnowledgeDocumentResponse(d))
	}
	response.OK(c, out)
}

// Upload godoc
// @Summary      Upload a knowledge base document
// @Description  multipart/form-data: `title` (required) plus either `content` (pasted text) or `file` (a .txt/.md upload — PDF/DOCX are not parsed yet). Ingested synchronously: chunked, embedded via Gemini, and written before this responds.
// @Tags         knowledge-base
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        title   formData string false "Document title"
// @Param        content formData string false "Pasted text content"
// @Param        file    formData file   false "A .txt or .md file"
// @Success      201 {object} response.Envelope{data=KnowledgeDocumentResponse}
// @Router       /v1/knowledge-base/documents [post]
func (h *KnowledgeHandler) Upload(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	userID, err := userIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	title := c.PostForm("title")
	if title == "" {
		c.Error(apperror.InvalidInput("title is required", nil))
		return
	}

	content := c.PostForm("content")
	sourceType := entity.KBSourceTypeManualText

	if content == "" {
		fileHeader, ferr := c.FormFile("file")
		if ferr != nil {
			c.Error(apperror.InvalidInput("either content or file is required", nil))
			return
		}
		file, oerr := fileHeader.Open()
		if oerr != nil {
			c.Error(apperror.Internal("open uploaded file", oerr))
			return
		}
		defer file.Close()

		data, rerr := io.ReadAll(file)
		if rerr != nil {
			c.Error(apperror.Internal("read uploaded file", rerr))
			return
		}
		if !utf8.Valid(data) {
			c.Error(apperror.InvalidInput(
				"uploaded file must be plain text (.txt/.md) — PDF/DOCX parsing isn't implemented yet",
				nil,
			))
			return
		}
		content = string(data)
		sourceType = entity.KBSourceTypeFile
	}

	doc, err := h.uc.Upload(c.Request.Context(), knowledgebase.UploadInput{
		OrganizationID: orgID,
		UploadedBy:     userID,
		Title:          title,
		Content:        content,
		SourceType:     sourceType,
	})
	if err != nil {
		c.Error(err)
		return
	}

	response.Created(c, toKnowledgeDocumentResponse(doc))
}

// Get godoc
// @Summary      Get a knowledge base document
// @Tags         knowledge-base
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Document ID"
// @Success      200 {object} response.Envelope{data=KnowledgeDocumentResponse}
// @Router       /v1/knowledge-base/documents/{id} [get]
func (h *KnowledgeHandler) Get(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid document id", err))
		return
	}

	doc, err := h.uc.Get(c.Request.Context(), orgID, id)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toKnowledgeDocumentResponse(doc))
}

// Delete godoc
// @Summary      Delete a knowledge base document (and its chunks)
// @Tags         knowledge-base
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Document ID"
// @Success      200 {object} response.Envelope{data=object}
// @Router       /v1/knowledge-base/documents/{id} [delete]
func (h *KnowledgeHandler) Delete(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid document id", err))
		return
	}

	if err := h.uc.Delete(c.Request.Context(), orgID, id); err != nil {
		c.Error(err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

func toKnowledgeDocumentResponse(d *entity.KnowledgeDocument) KnowledgeDocumentResponse {
	return KnowledgeDocumentResponse{
		ID:           d.ID.String(),
		Title:        d.Title,
		SourceType:   string(d.SourceType),
		Status:       string(d.Status),
		ErrorMessage: d.ErrorMessage,
		CreatedAt:    d.CreatedAt.Format(time.RFC3339),
	}
}
