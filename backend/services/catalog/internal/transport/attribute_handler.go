package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/usecase"
)

// AttributeHandler serves the attribute catalog's admin-authoring endpoints
// (create attribute/option, set a category's attribute rules) and the
// public attribute-template endpoint the vendor form renders itself from.
type AttributeHandler struct {
	attributes *usecase.AttributeUseCase
	log        zerolog.Logger
}

func NewAttributeHandler(attributes *usecase.AttributeUseCase, log zerolog.Logger) *AttributeHandler {
	return &AttributeHandler{attributes: attributes, log: log}
}

func (h *AttributeHandler) Create(c *gin.Context) {
	var req createAttributeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	a, err := h.attributes.CreateAttribute(c.Request.Context(), req.Code, req.Name, domain.DataType(req.DataType), req.Unit, req.IsVariantDefining)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toAttributeResponse(a, nil))
}

func (h *AttributeHandler) List(c *gin.Context) {
	attributes, options, err := h.attributes.ListWithOptions(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toAttributeResponseList(attributes, options))
}

func (h *AttributeHandler) AddOption(c *gin.Context) {
	var req addAttributeOptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	o, err := h.attributes.AddOption(c.Request.Context(), c.Param("id"), req.Value)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toAttributeOptionResponse(o))
}

func (h *AttributeHandler) SetCategoryRule(c *gin.Context) {
	var req setCategoryAttributeRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	rule, err := h.attributes.SetCategoryRule(c.Request.Context(), c.Param("id"), req.AttributeID, req.IsRequired, req.IsExcluded, req.Position, middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toCategoryAttributeRuleResponse(rule))
}

// GetTemplate is public (same trust level as GET /categories): it only
// describes which fields apply to a category, nothing sensitive.
func (h *AttributeHandler) GetTemplate(c *gin.Context) {
	resolved, err := h.attributes.ResolveTemplate(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toAttributeTemplateResponse(resolved))
}
