package transport

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

type AdminHandler struct {
	vendors *usecase.VendorUseCase
	log     zerolog.Logger
}

func NewAdminHandler(vendors *usecase.VendorUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{vendors: vendors, log: log}
}

func (h *AdminHandler) ListApplications(c *gin.Context) {
	status := c.Query("status")
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset := parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)

	vendors, err := h.vendors.ListApplications(c.Request.Context(), status, limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponseList(vendors))
}

func (h *AdminHandler) Approve(c *gin.Context) {
	v, err := h.vendors.Approve(c.Request.Context(), c.Param("id"), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponse(v))
}

// GetAuditLog returns the full approve/reject decision history for one
// vendor application, newest first.
func (h *AdminHandler) GetAuditLog(c *gin.Context) {
	entries, err := h.vendors.ListAuditLog(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toAuditLogEntryResponseList(entries))
}

func (h *AdminHandler) Reject(c *gin.Context) {
	var req rejectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	v, err := h.vendors.Reject(c.Request.Context(), c.Param("id"), middleware.GetUserID(c), req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponse(v))
}

func parseIntDefault(raw string, fallback, min, max int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min || v > max {
		return fallback
	}
	return v
}
