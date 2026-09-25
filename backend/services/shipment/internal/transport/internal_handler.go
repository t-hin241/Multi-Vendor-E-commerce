package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/shipment/internal/usecase"
)

// InternalHandler serves the network-only, unauthenticated routes Order
// calls to create and cancel shipments — same trust boundary as every
// other /internal/... route in this codebase (Catalog's
// /internal/products/:id, Inventory's /internal/inventory/reserve, etc.).
type InternalHandler struct {
	shipments *usecase.ShipmentUseCase
	log       zerolog.Logger
}

func NewInternalHandler(shipments *usecase.ShipmentUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{shipments: shipments, log: log}
}

func (h *InternalHandler) CreateShipment(c *gin.Context) {
	var req internalCreateShipmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	shipment, err := h.shipments.CreateAuto(c.Request.Context(), usecase.CreateShipmentInput{
		VendorOrderID: req.VendorOrderID, VendorID: req.VendorID, BuyerID: req.BuyerID,
		PackageWeightGrams: req.PackageWeightGrams,
		RecipientName:      req.RecipientName, Phone: req.Phone, Province: req.Province,
		District: req.District, Ward: req.Ward, StreetAddress: req.StreetAddress,
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toInternalShipmentResponse(shipment))
}

func (h *InternalHandler) CancelForVendorOrder(c *gin.Context) {
	if err := h.shipments.CancelForVendorOrder(c.Request.Context(), c.Param("id")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"cancelled": true})
}
