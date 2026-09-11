package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/shipment/internal/usecase"
)

type VendorShippingMethodHandler struct {
	methods *usecase.VendorShippingMethodUseCase
	log     zerolog.Logger
}

func NewVendorShippingMethodHandler(methods *usecase.VendorShippingMethodUseCase, log zerolog.Logger) *VendorShippingMethodHandler {
	return &VendorShippingMethodHandler{methods: methods, log: log}
}

func (h *VendorShippingMethodHandler) Enable(c *gin.Context) {
	var req enableShippingMethodRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	method, err := h.methods.Enable(c.Request.Context(), middleware.GetUserID(c), req.CarrierID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toVendorShippingMethodResponse(method))
}

func (h *VendorShippingMethodHandler) ListMine(c *gin.Context) {
	methods, err := h.methods.ListMine(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toVendorShippingMethodResponseList(methods))
}

func (h *VendorShippingMethodHandler) SetDefault(c *gin.Context) {
	if err := h.methods.SetDefault(c.Request.Context(), middleware.GetUserID(c), c.Param("id")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"updated": true})
}

func (h *VendorShippingMethodHandler) SetActive(c *gin.Context) {
	var req setActiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if err := h.methods.SetActive(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.IsActive); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"updated": true})
}
