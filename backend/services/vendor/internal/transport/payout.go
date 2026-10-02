package transport

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

type PayoutHandler struct {
	UseCase *usecase.PayoutUseCase
	Log     zerolog.Logger
}

func (h PayoutHandler) Register(r *gin.Engine, auth gin.HandlerFunc, payoutKey string, internal *serviceauth.Verifier) {
	owner := r.Group("/api/vendor/:vendorId/payout-accounts", auth, middleware.RequireRole("vendor"))
	owner.Use(payoutNoStore())
	owner.POST("", h.Submit)
	owner.GET("", func(c *gin.Context) { h.List(c, false) })
	admin := r.Group("/api/vendor/admin/shops/:vendorId/payout-accounts", auth, middleware.RequireRole("admin"))
	admin.Use(payoutNoStore())
	admin.GET("", func(c *gin.Context) { h.List(c, true) })
	admin.POST("/:id/decision", h.Decide)
	admin.POST("/:id/details", func(c *gin.Context) { h.Details(c, false) })
	r.POST("/internal/payout-destinations/:vendorId/:id", serviceauth.Require(payoutKey, "X-Vendor-Payout-Key"), func(c *gin.Context) { h.Details(c, true) })
	// Masked reference only; the full account needs the payout scope key.
	r.GET("/internal/payout-destinations/:vendorId/default", internal.Allow("payment"), h.DefaultDestination)
}
func payoutNoStore() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	}
}
func (h PayoutHandler) Submit(c *gin.Context) {
	var in struct {
		Bank   string `json:"bank_bin"`
		Number string `json:"account_number"`
		Name   string `json:"account_name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpresponse.HandleError(c, h.Log, apperror.Validation("Invalid payout account"))
		return
	}
	a, err := h.UseCase.Submit(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), in.Bank, in.Number, in.Name)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, a)
}
func (h PayoutHandler) List(c *gin.Context, admin bool) {
	a, err := h.UseCase.List(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), admin, parseIntDefault(c.Query("limit"), 20, 1, 100), parseIntDefault(c.Query("offset"), 0, 0, 1000000))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, a)
}
func (h PayoutHandler) DefaultDestination(c *gin.Context) {
	d, err := h.UseCase.DefaultDestination(c.Request.Context(), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, d)
}
func (h PayoutHandler) Decide(c *gin.Context) {
	var in struct {
		Version int64  `json:"version"`
		Verify  bool   `json:"verify"`
		Reason  string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpresponse.HandleError(c, h.Log, apperror.Validation("Invalid payout decision"))
		return
	}
	a, err := h.UseCase.Decide(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.Param("id"), in.Version, in.Verify, in.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, a)
}
func (h PayoutHandler) Details(c *gin.Context, payment bool) {
	var in struct {
		Version int64  `json:"version"`
		Purpose string `json:"purpose"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Version < 1 {
		httpresponse.HandleError(c, h.Log, apperror.Validation("Invalid payout account version"))
		return
	}
	a, err := h.UseCase.Details(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.Param("id"), in.Version, in.Purpose, payment)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Account-Version", strconv.FormatInt(a.Version, 10))
	httpresponse.OK(c, http.StatusOK, a)
}
