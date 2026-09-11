package transport

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/order/internal/usecase"
)

// InternalHandler serves Payment's payment-facing contract with Order: read
// an order to size and authorize a payment intent, and report a payment
// outcome back so Order — the only service that owns order lifecycle — can
// validate and apply the resulting transition. Like the other services'
// internal handlers, it is not proxied by the gateway's public route table.
type InternalHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewInternalHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{orders: orders, log: log}
}

func (h *InternalHandler) Get(c *gin.Context) {
	order, err := h.orders.GetForInternal(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toInternalOrderResponse(order))
}

// internalVendorOrderResponse is deliberately separate from the general
// vendorOrderResponse used by buyer/vendor-facing endpoints: it carries the
// buyer's destination and a recomputed package weight, which only
// Shipment's internal vendor-triggered fallback needs — the buyer's
// address must never leak through the ordinary vendor-order list/get
// endpoints (the vendor already sees it via their Shipment record once one
// exists).
type internalVendorOrderResponse struct {
	ID       string `json:"id"`
	OrderID  string `json:"order_id"`
	VendorID string `json:"vendor_id"`
	Status   string `json:"status"`

	BuyerID            string `json:"buyer_id"`
	RecipientName      string `json:"recipient_name"`
	Phone              string `json:"phone"`
	Province           string `json:"province"`
	District           string `json:"district"`
	Ward               string `json:"ward"`
	StreetAddress      string `json:"street_address"`
	PackageWeightGrams int64  `json:"package_weight_grams"`
}

func (h *InternalHandler) GetVendorOrder(c *gin.Context) {
	vo, order, packageWeightGrams, err := h.orders.GetVendorOrderForInternal(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, internalVendorOrderResponse{
		ID: vo.ID, OrderID: vo.OrderID, VendorID: vo.VendorID, Status: string(vo.Status),
		BuyerID: order.BuyerID, RecipientName: order.RecipientName, Phone: order.Phone,
		Province: order.Province, District: order.District, Ward: order.Ward, StreetAddress: order.StreetAddress,
		PackageWeightGrams: packageWeightGrams,
	})
}

func (h *InternalHandler) MarkPaid(c *gin.Context) {
	order, err := h.orders.MarkPaid(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toOrderResponse(order))
}

type quantitySoldResponse struct {
	ProductID    string `json:"product_id"`
	QuantitySold int64  `json:"quantity_sold"`
}

// QuantitySoldByProductIDs lets Catalog resolve units-sold for a batch of
// product ids in one call, for the storefront listing's product cards.
func (h *InternalHandler) QuantitySoldByProductIDs(c *gin.Context) {
	var productIDs []string
	for _, id := range strings.Split(c.Query("product_ids"), ",") {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			productIDs = append(productIDs, trimmed)
		}
	}

	quantities, err := h.orders.QuantitySoldByProductIDs(c.Request.Context(), productIDs)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	out := make([]quantitySoldResponse, 0, len(quantities))
	for productID, quantity := range quantities {
		out = append(out, quantitySoldResponse{ProductID: productID, QuantitySold: quantity})
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *InternalHandler) MarkPaymentFailed(c *gin.Context) {
	var req markPaymentFailedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	order, err := h.orders.MarkPaymentFailed(c.Request.Context(), c.Param("id"), req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toOrderResponse(order))
}
