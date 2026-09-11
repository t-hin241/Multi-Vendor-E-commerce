package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

type VendorAddressHandler struct {
	addresses *usecase.VendorAddressUseCase
	log       zerolog.Logger
}

func NewVendorAddressHandler(addresses *usecase.VendorAddressUseCase, log zerolog.Logger) *VendorAddressHandler {
	return &VendorAddressHandler{addresses: addresses, log: log}
}

func (h *VendorAddressHandler) Add(c *gin.Context) {
	var req addressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	address, err := h.addresses.Add(c.Request.Context(), middleware.GetUserID(c), req.RecipientName, req.Phone, req.Province, req.District, req.Ward, req.StreetAddress)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toVendorAddressResponse(address))
}

func (h *VendorAddressHandler) ListMine(c *gin.Context) {
	addresses, err := h.addresses.ListMine(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toVendorAddressResponseList(addresses))
}

func (h *VendorAddressHandler) Update(c *gin.Context) {
	var req addressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	address, err := h.addresses.Update(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.RecipientName, req.Phone, req.Province, req.District, req.Ward, req.StreetAddress)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toVendorAddressResponse(address))
}

func (h *VendorAddressHandler) Delete(c *gin.Context) {
	if err := h.addresses.Delete(c.Request.Context(), middleware.GetUserID(c), c.Param("id")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"deleted": true})
}

func (h *VendorAddressHandler) SetDefault(c *gin.Context) {
	if err := h.addresses.SetDefault(c.Request.Context(), middleware.GetUserID(c), c.Param("id")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"updated": true})
}
