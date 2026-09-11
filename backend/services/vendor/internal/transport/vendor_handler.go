package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

type VendorHandler struct {
	vendors *usecase.VendorUseCase
	log     zerolog.Logger
}

func NewVendorHandler(vendors *usecase.VendorUseCase, log zerolog.Logger) *VendorHandler {
	return &VendorHandler{vendors: vendors, log: log}
}

func (h *VendorHandler) Apply(c *gin.Context) {
	var req applyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	v, err := h.vendors.Apply(c.Request.Context(), middleware.GetUserID(c), req.ShopName, req.Description)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toVendorResponse(v))
}

func (h *VendorHandler) Me(c *gin.Context) {
	v, err := h.vendors.GetByUserID(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponse(v))
}

func (h *VendorHandler) UpdateProfile(c *gin.Context) {
	var req updateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	v, err := h.vendors.UpdateProfile(c.Request.Context(), middleware.GetUserID(c), req.ShopName, req.Description)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponse(v))
}
