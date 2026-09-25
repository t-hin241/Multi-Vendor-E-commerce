package transport

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

func (h *OrderHandler) Checkout(c *gin.Context) {
	token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if !ok || token == "" {
		httpresponse.Error(c, http.StatusUnauthorized, "unauthorized", "Missing bearer token")
		return
	}

	var req checkoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	order, err := h.orders.Checkout(c.Request.Context(), middleware.GetUserID(c), token, req.AddressID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toOrderResponse(order))
}

func (h *OrderHandler) Cancel(c *gin.Context) {
	order, err := h.orders.Cancel(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toOrderResponse(order))
}

func (h *OrderHandler) Get(c *gin.Context) {
	order, items, vendorOrders, err := h.orders.GetOwnedByBuyer(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toOrderResponseWithDetails(order, items, vendorOrders))
}

func (h *OrderHandler) ListMine(c *gin.Context) {
	limit, offset := paginationParams(c)

	orders, err := h.orders.ListMine(c.Request.Context(), middleware.GetUserID(c), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toOrderResponseList(orders))
}

func (h *OrderHandler) ListVendorMine(c *gin.Context) {
	limit, offset := paginationParams(c)

	vendorOrders, items, err := h.orders.ListVendorMine(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorOrderResponseList(vendorOrders, items))
}

func (h *OrderHandler) UpdateVendorOrderStatus(c *gin.Context) {
	var req updateVendorOrderStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
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

// ExportCSV serves a vendor's own vendor orders as CSV — a "basic order
// export" for the vendor to open in a spreadsheet, not a paginated API
// response, so it fetches a single large page rather than the usual
// limit/offset a buyer or admin listing would use.
func (h *OrderHandler) ExportCSV(c *gin.Context) {
	const maxExportRows = 5000

	vendorOrders, itemsByVendorOrder, err := h.orders.ListVendorMine(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), maxExportRows, 0)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", `attachment; filename="vendor-orders.csv"`)

	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{
		"vendor_order_id", "order_id", "status", "product_name", "variant_sku", "variant_label",
		"quantity", "price_amount", "subtotal_amount", "shipping_fee_amount", "commission_amount", "net_amount", "currency", "created_at",
	})
	for _, vo := range vendorOrders {
		items := itemsByVendorOrder[vo.ID]
		if len(items) == 0 {
			// A vendor order should always have at least one item, but
			// don't let a data gap silently drop the row from the export.
			_ = w.Write([]string{
				vo.ID, vo.OrderID, string(vo.Status), "", "", "",
				"", "", strconv.FormatInt(vo.SubtotalAmount, 10), strconv.FormatInt(vo.ShippingFeeAmount, 10),
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
				strconv.FormatInt(vo.ShippingFeeAmount, 10),
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
	offset = parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)
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
