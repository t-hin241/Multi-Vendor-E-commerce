package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

type StockSelfServiceHandler struct {
	UseCase usecase.StockSelfService
	Log     zerolog.Logger
}

func RegisterStockSelfService(r *gin.Engine, jwt *authjwt.Manager, adminGuard gin.HandlerFunc, h StockSelfServiceHandler) {
	v := r.Group("/api/inventory/vendor", middleware.RequireAuth(jwt), middleware.SellerConsole(), stockRequest)
	v.GET("/self-service-policy", h.policy)
	v.POST("/stock-adjustments", h.adjust)
	v.POST("/stock-counts", h.count)
	v.GET("/stock-adjustments/:id", h.operation)
	v.PUT("/items/:id/alert-settings", h.alerts)
	v.GET("/movements", h.movements)
	a := r.Group("/api/inventory/admin", middleware.RequireAuth(jwt), middleware.RequireRole("admin"), adminGuard, stockRequest)
	a.GET("/vendors/:id/self-service-policy", h.policy)
	a.POST("/vendors/:id/self-service-policy", h.updatePolicy)
	a.GET("/stock-adjustments", h.work)
	a.POST("/stock-adjustments/:id/decisions", h.decide)
	a.GET("/stock-alerts", h.alertWork)
	a.POST("/stock-alerts/:id/retry", h.retryAlert)
}

func stockRequest(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	c.Header("Cache-Control", "no-store")
	c.Next()
}

func (h StockSelfServiceHandler) alertWork(c *gin.Context) {
	out, err := h.UseCase.AlertWork(c.Request.Context(), middleware.GetUserID(c), c.Query("cursor"), stockLimit(c))
	h.respond(c, out, err)
}
func (h StockSelfServiceHandler) retryAlert(c *gin.Context) {
	if !stockID(c, c.Param("id")) {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !stockJSON(c, &in) {
		return
	}
	err := h.UseCase.RetryAlert(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), c.GetHeader("Idempotency-Key"), in.Reason)
	h.respond(c, gin.H{"status": "pending"}, err)
}
func stockJSON(c *gin.Context, v any) bool {
	d := json.NewDecoder(c.Request.Body)
	var raw json.RawMessage
	if err := d.Decode(&raw); err != nil {
		httpresponse.Error(c, 422, "validation_error", "Invalid fields or values in request body")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		httpresponse.Error(c, 422, "validation_error", "Expected one JSON object")
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		httpresponse.Error(c, 422, "validation_error", "Expected a JSON object")
		return false
	}
	var required []string
	switch v.(type) {
	case *domain.PolicyChange:
		required = []string{"enabled", "reason", "expected_version"}
	case *domain.AlertSettings:
		required = []string{"enabled", "low_stock_threshold", "expected_version"}
	case *domain.StockAdjustment:
		required = []string{"item_id", "reason", "evidence_ref", "expected_version"}
	case *usecase.StockDecision:
		required = []string{"decision", "reason", "expected_version"}
	}
	for _, field := range required {
		value, ok := fields[field]
		if !ok || bytes.Equal(value, []byte("null")) {
			httpresponse.Error(c, 422, "validation_error", field+" is required")
			return false
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		httpresponse.Error(c, 422, "validation_error", "Invalid fields or values in request body")
		return false
	}
	return true
}
func stockID(c *gin.Context, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		httpresponse.Error(c, 422, "validation_error", "Invalid resource id")
		return false
	}
	return true
}
func (h StockSelfServiceHandler) respond(c *gin.Context, out any, err error) {
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, out)
}
func (h StockSelfServiceHandler) policy(c *gin.Context) {
	vendor := c.Param("id")
	admin := vendor != ""
	if !admin {
		vendor = c.Query("vendor_id")
	}
	if !stockID(c, vendor) {
		return
	}
	out, err := h.UseCase.Policy(c.Request.Context(), middleware.GetUserID(c), vendor, admin)
	h.respond(c, out, err)
}
func (h StockSelfServiceHandler) updatePolicy(c *gin.Context) {
	if !stockID(c, c.Param("id")) {
		return
	}
	var in domain.PolicyChange
	if !stockJSON(c, &in) {
		return
	}
	out, err := h.UseCase.UpdatePolicy(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), c.GetHeader("Idempotency-Key"), in)
	h.respond(c, out, err)
}
func (h StockSelfServiceHandler) adjustmentResponse(c *gin.Context, out domain.AdjustmentReceipt, err error) {
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	status := http.StatusOK
	if out.Status == "approval_required" {
		status = http.StatusAccepted
	}
	if out.Status == "reserved_stock_conflict" {
		c.Header("Location", out.StatusURL)
		httpresponse.Error(c, 409, "reserved_stock_conflict", "Physical count is below reserved stock; a shortage work item has been recorded")
		return
	}
	httpresponse.OK(c, status, out)
}
func (h StockSelfServiceHandler) adjust(c *gin.Context) {
	var in domain.StockAdjustment
	if !stockJSON(c, &in) {
		return
	}
	if in.Kind == "count" {
		httpresponse.Error(c, 422, "validation_error", "Use stock-counts for a physical count")
		return
	}
	out, err := h.UseCase.Adjust(c.Request.Context(), middleware.GetUserID(c), c.GetHeader("Idempotency-Key"), in)
	h.adjustmentResponse(c, out, err)
}
func (h StockSelfServiceHandler) count(c *gin.Context) {
	var in domain.StockAdjustment
	if !stockJSON(c, &in) {
		return
	}
	if in.Kind != "" && in.Kind != "count" {
		httpresponse.Error(c, 422, "validation_error", "kind must be count")
		return
	}
	in.Kind = "count"
	out, err := h.UseCase.Adjust(c.Request.Context(), middleware.GetUserID(c), c.GetHeader("Idempotency-Key"), in)
	h.adjustmentResponse(c, out, err)
}
func (h StockSelfServiceHandler) alerts(c *gin.Context) {
	var in domain.AlertSettings
	if !stockJSON(c, &in) {
		return
	}
	out, err := h.UseCase.Alerts(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), c.GetHeader("Idempotency-Key"), in)
	h.respond(c, out, err)
}
func (h StockSelfServiceHandler) operation(c *gin.Context) {
	if !stockID(c, c.Param("id")) {
		return
	}
	out, err := h.UseCase.Operation(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	h.respond(c, out, err)
}
func stockLimit(c *gin.Context) int {
	n, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || n < 1 || n > 100 {
		return 20
	}
	return n
}
func (h StockSelfServiceHandler) movements(c *gin.Context) {
	out, err := h.UseCase.Movements(c.Request.Context(), middleware.GetUserID(c), c.Query("item_id"), c.Query("cursor"), stockLimit(c))
	h.respond(c, out, err)
}
func (h StockSelfServiceHandler) work(c *gin.Context) {
	out, err := h.UseCase.Work(c.Request.Context(), middleware.GetUserID(c), c.Query("cursor"), stockLimit(c))
	h.respond(c, out, err)
}
func (h StockSelfServiceHandler) decide(c *gin.Context) {
	if !stockID(c, c.Param("id")) {
		return
	}
	var in usecase.StockDecision
	if !stockJSON(c, &in) {
		return
	}
	out, err := h.UseCase.Decide(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), c.GetHeader("Idempotency-Key"), in)
	h.respond(c, out, err)
}
