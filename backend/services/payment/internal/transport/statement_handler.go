package transport

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/usecase"
)

type StatementHandler struct {
	UC  *usecase.StatementUseCase
	Log zerolog.Logger
}

func (h *StatementHandler) admin(c *gin.Context) bool {
	return strings.HasPrefix(c.FullPath(), "/api/payments/admin/")
}
func (h *StatementHandler) Capability(c *gin.Context) {
	enabled, err := h.UC.Capability(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), h.admin(c))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"statements_enabled": enabled})
}
func statementFilter(from, to, currency string) (domain.StatementFilter, error) {
	f := domain.StatementFilter{Currency: currency}
	var err error
	f.From, err = time.Parse(time.RFC3339Nano, from)
	if err != nil {
		return f, domain.StatementInvalid("from must be an ISO timestamp with timezone")
	}
	f.To, err = time.Parse(time.RFC3339Nano, to)
	if err != nil {
		return f, domain.StatementInvalid("to must be an ISO timestamp with timezone")
	}
	f.From, f.To = f.From.UTC(), f.To.UTC()
	return f, f.Validate()
}
func (h *StatementHandler) Read(c *gin.Context) {
	f, err := statementFilter(c.Query("from"), c.Query("to"), c.Query("currency"))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil {
		httpresponse.HandleError(c, h.Log, domain.StatementInvalid("limit must be 1-100"))
		return
	}
	s, err := h.UC.Read(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), h.admin(c), f, c.Query("cursor"), limit)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, s)
}
func (h *StatementHandler) CreateExport(c *gin.Context) {
	var in struct {
		From     string `json:"from"`
		To       string `json:"to"`
		Currency string `json:"currency"`
		Format   string `json:"format"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpresponse.HandleError(c, h.Log, domain.StatementInvalid("from, to, currency and format are required"))
		return
	}
	f, err := statementFilter(in.From, in.To, in.Currency)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	e, err := h.UC.CreateExport(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.GetHeader("Idempotency-Key"), in.Format, f)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusAccepted, e)
}
func (h *StatementHandler) Export(c *gin.Context) {
	e, err := h.UC.Export(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, e)
}
func (h *StatementHandler) Download(c *gin.Context) {
	u, expires, err := h.UC.Download(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"url": u, "expires_at": expires})
}
func (h *StatementHandler) Payout(c *gin.Context) {
	p, err := h.UC.Payout(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.Param("id"), h.admin(c))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, p)
}
