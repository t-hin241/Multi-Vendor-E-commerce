package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/catalog/internal/usecase"
)

type CategoryHandler struct {
	categories *usecase.CategoryUseCase
	log        zerolog.Logger
}

func NewCategoryHandler(categories *usecase.CategoryUseCase, log zerolog.Logger) *CategoryHandler {
	return &CategoryHandler{categories: categories, log: log}
}

func (h *CategoryHandler) Create(c *gin.Context) {
	var req createCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	category, err := h.categories.Create(c.Request.Context(), req.Name, req.ParentID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toCategoryResponse(category))
}

func (h *CategoryHandler) List(c *gin.Context) {
	categories, err := h.categories.List(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toCategoryResponseList(categories))
}
