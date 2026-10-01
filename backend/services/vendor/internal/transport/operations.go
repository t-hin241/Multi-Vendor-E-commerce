package transport

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/vendorsvc/internal/domain"
)

func (h *AdminHandler) Suspend(c *gin.Context) { h.changeStatus(c, false) }
func (h *AdminHandler) Operations(c *gin.Context) {
	out, err := h.vendors.Operations(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, out)
}
func (h *AdminHandler) Restore(c *gin.Context) { h.changeStatus(c, true) }
func (h *AdminHandler) changeStatus(c *gin.Context, restore bool) {
	var req rejectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.HandleError(c, h.log, apperror.Validation("A reason is required"))
		return
	}
	var v *domain.Vendor
	var err error
	if restore {
		v, err = h.vendors.Restore(c.Request.Context(), c.Param("id"), middleware.GetUserID(c), req.Reason)
	} else {
		v, err = h.vendors.Suspend(c.Request.Context(), c.Param("id"), middleware.GetUserID(c), req.Reason)
	}
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusAccepted, toVendorResponse(v))
}
func (h *AdminHandler) Replay(c *gin.Context) {
	var req struct {
		Reason string `json:"reason" binding:"required,max=500"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A reason of at most 500 characters is required")
		return
	}
	if err := h.vendors.Replay(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Reason); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusAccepted, gin.H{"queued": true})
}
func (h *VendorHandler) Resubmit(c *gin.Context) {
	v, err := h.vendors.Resubmit(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toVendorResponse(v))
}
func (h *InternalHandler) SaleStatus(c *gin.Context) {
	after := c.Query("after")
	var ids []string
	if after != "" {
		if _, err := uuid.Parse(after); err != nil {
			httpresponse.HandleError(c, h.log, apperror.Validation("Invalid cursor"))
			return
		}
	}
	if raw := c.Query("ids"); raw != "" {
		ids = strings.Split(raw, ",")
		if len(ids) > 100 {
			httpresponse.HandleError(c, h.log, apperror.Validation("Maximum 100 shops"))
			return
		}
		for _, id := range ids {
			if _, err := uuid.Parse(id); err != nil {
				httpresponse.HandleError(c, h.log, apperror.Validation("Invalid shop ID"))
				return
			}
		}
	}
	data, err := h.vendors.SaleStatus(c.Request.Context(), ids, after)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]vendorsales.Status, 0, len(data))
	for _, v := range data {
		out = append(out, vendorsales.Status{VendorID: v.ID, Status: string(v.Status), Version: v.Version})
	}
	httpresponse.OK(c, http.StatusOK, out)
}
