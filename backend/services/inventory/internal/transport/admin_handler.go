package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/inventory/internal/usecase"
)

// AdminHandler serves the admin moderation queue for vendor stock-increase
// requests — mirrors Catalog's own AdminHandler shape.
type AdminHandler struct {
	inventory *usecase.InventoryUseCase
	log       zerolog.Logger
}

func NewAdminHandler(inventory *usecase.InventoryUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{inventory: inventory, log: log}
}

func (h *AdminHandler) ListRestockRequests(c *gin.Context) {
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset := parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)

	requests, err := h.inventory.ListRestockRequestsForAdmin(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toRestockRequestResponseList(requests))
}

func (h *AdminHandler) Approve(c *gin.Context) {
	req, err := h.inventory.ApproveRestockRequest(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toRestockRequestResponse(req))
}

func (h *AdminHandler) Reject(c *gin.Context) {
	var body rejectRestockRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	req, err := h.inventory.RejectRestockRequest(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), body.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toRestockRequestResponse(req))
}
