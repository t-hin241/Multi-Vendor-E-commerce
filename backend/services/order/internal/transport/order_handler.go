package transport

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

type OrderHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewOrderHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *OrderHandler {
	return &OrderHandler{orders: orders, log: log}
}

// Checkout places an order. The Idempotency-Key header identifies the
// attempt: retrying with the same key and body returns the same order
// (200) instead of creating another one (201).
func (h *OrderHandler) Checkout(c *gin.Context) {
	var req checkoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"address_id must be a valid id; cart_version and expected_total_amount, when given, must be positive")
		return
	}
	order, replayed, err := h.orders.Checkout(c.Request.Context(), middleware.GetUserID(c), usecase.CheckoutInput{
		AddressID: req.AddressID, CartVersion: req.CartVersion, ExpectedTotal: req.ExpectedTotalAmount,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	httpresponse.OK(c, status, toOrderResponse(order))
}

// Preview prices the cart for an address with a shipping quote per shop.
func (h *OrderHandler) Preview(c *gin.Context) {
	var req previewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "address_id must be a valid id")
		return
	}
	preview, err := h.orders.Preview(c.Request.Context(), middleware.GetUserID(c), req.AddressID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toPreviewResponse(preview))
}

func (h *OrderHandler) Cancel(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	order, err := h.orders.Cancel(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toOrderResponse(order))
}

func (h *OrderHandler) Get(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	detail, err := h.orders.GetOwnedByBuyer(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toOrderDetailResponse(detail, false))
}

// ListMine is the buyer's order list (?status=&limit=&offset=). The total
// number of matching orders is in the X-Total-Count header.
func (h *OrderHandler) ListMine(c *gin.Context) {
	limit, offset := paginationParams(c)
	orders, total, err := h.orders.ListMine(c.Request.Context(), middleware.GetUserID(c), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	httpresponse.OK(c, http.StatusOK, toOrderResponseList(orders))
}

func (h *OrderHandler) ListVendorMine(c *gin.Context) {
	limit, offset := paginationParams(c)
	vendorOrders, items, err := h.orders.ListVendorMine(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toVendorOrderResponseList(vendorOrders, items))
}

func (h *OrderHandler) UpdateVendorOrderStatus(c *gin.Context) {
	if !validID(c, c.Param("vendorOrderID")) {
		return
	}
	var req updateVendorOrderStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "status must be processing, shipped or completed")
		return
	}
	vo, err := h.orders.UpdateVendorOrderStatus(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorOrderID"), domain.Status(req.Status))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toVendorOrderResponse(vo, nil))
}

func (h *OrderHandler) Summary(c *gin.Context) {
	summary, topProducts, err := h.orders.GetVendorSummary(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toVendorSummaryResponse(summary, topProducts))
}

// ExportCSV serves a vendor's own vendor orders as CSV.
func (h *OrderHandler) ExportCSV(c *gin.Context) {
	const maxExportRows = 5000

	vendorOrders, itemsByVendorOrder, err := h.orders.ListVendorMine(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), c.Query("status"), maxExportRows, 0)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", `attachment; filename="vendor-orders.csv"`)

	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{
		"vendor_order_id", "order_id", "status", "product_name", "variant_sku", "variant_label",
		"quantity", "price_amount", "subtotal_amount", "shipping_fee_amount", "refunded_amount", "commission_amount", "net_amount", "currency", "created_at",
	})
	for _, vo := range vendorOrders {
		items := itemsByVendorOrder[vo.ID]
		if len(items) == 0 {
			_ = w.Write([]string{
				vo.ID, vo.OrderID, string(vo.Status), "", "", "",
				"", "", strconv.FormatInt(vo.SubtotalAmount, 10), strconv.FormatInt(vo.ShippingFeeAmount, 10), strconv.FormatInt(vo.RefundedAmount, 10),
				formatNullableInt64(vo.CommissionAmount), formatNullableInt64(vo.NetAmount),
				vo.Currency, vo.CreatedAt.Format(time.RFC3339),
			})
			continue
		}
		for _, item := range items {
			_ = w.Write([]string{
				vo.ID, vo.OrderID, string(vo.Status),
				item.ProductName, formatNullableString(item.VariantSKU), formatNullableString(item.VariantLabel),
				strconv.FormatInt(item.Quantity, 10), strconv.FormatInt(item.PriceAmount, 10), strconv.FormatInt(item.SubtotalAmount, 10),
				strconv.FormatInt(vo.ShippingFeeAmount, 10), strconv.FormatInt(vo.RefundedAmount, 10),
				formatNullableInt64(vo.CommissionAmount), formatNullableInt64(vo.NetAmount),
				vo.Currency, vo.CreatedAt.Format(time.RFC3339),
			})
		}
	}
	w.Flush()
}

func formatNullableString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func formatNullableInt64(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

func paginationParams(c *gin.Context) (limit, offset int) {
	limit = parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset = parseIntDefault(c.Query("offset"), 0, 0, 10_000)
	return limit, offset
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

// validID answers 400 for a malformed id instead of letting the database
// reject it as an internal error.
func validID(c *gin.Context, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid id")
		return false
	}
	return true
}
