package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/usecase"
)

type BuyerAddressHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewBuyerAddressHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *BuyerAddressHandler {
	return &BuyerAddressHandler{orders: orders, log: log}
}

func (h *BuyerAddressHandler) Add(c *gin.Context) {
	var req addressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	address, err := h.orders.AddAddress(c.Request.Context(), middleware.GetUserID(c), req.RecipientName, req.Phone, req.Province, req.District, req.Ward, req.StreetAddress)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toBuyerAddressResponse(address))
}

func (h *BuyerAddressHandler) ListMine(c *gin.Context) {
	addresses, err := h.orders.ListMyAddresses(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toBuyerAddressResponseList(addresses))
}

func (h *BuyerAddressHandler) Update(c *gin.Context) {
	var req addressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	address, err := h.orders.UpdateAddress(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.RecipientName, req.Phone, req.Province, req.District, req.Ward, req.StreetAddress)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toBuyerAddressResponse(address))
}

func (h *BuyerAddressHandler) Delete(c *gin.Context) {
	if err := h.orders.DeleteAddress(c.Request.Context(), middleware.GetUserID(c), c.Param("id")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"deleted": true})
}

func (h *BuyerAddressHandler) SetDefault(c *gin.Context) {
	if err := h.orders.SetDefaultAddress(c.Request.Context(), middleware.GetUserID(c), c.Param("id")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"updated": true})
}
