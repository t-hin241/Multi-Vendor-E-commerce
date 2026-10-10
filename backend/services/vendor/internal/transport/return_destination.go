package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// ReturnDestinationHandler serves AF-05 return destinations: the owner
// designates one, an admin verifies it, Order reads the verified one.
type ReturnDestinationHandler struct {
	UseCase    *usecase.ReturnDestinationUseCase
	Log        zerolog.Logger
	AdminGuard gin.HandlerFunc
}

func (h ReturnDestinationHandler) Register(r *gin.Engine, auth gin.HandlerFunc, internal *serviceauth.Verifier) {
	owner := r.Group("/api/vendor/:vendorId/return-destination", auth, middleware.RequireRole("vendor"))
	owner.GET("", h.Get)
	owner.PUT("", h.Set)
	admin := r.Group("/api/vendor/admin/shops/:vendorId/return-destination", auth, middleware.RequireRole("admin"), h.AdminGuard)
	admin.GET("", h.AdminGet)
	admin.POST("/decision", h.Decide)
	r.GET("/internal/return-destinations/:vendorId", internal.Allow("order"), h.Verified)
}

type returnDestinationResponse struct {
	VendorID        string     `json:"vendor_id"`
	AddressID       string     `json:"address_id"`
	RecipientName   string     `json:"recipient_name"`
	Phone           string     `json:"phone"`
	Province        string     `json:"province"`
	District        string     `json:"district"`
	Ward            string     `json:"ward"`
	StreetAddress   string     `json:"street_address"`
	ReceivingHours  string     `json:"receiving_hours"`
	Version         int64      `json:"version"`
	Verified        bool       `json:"verified"`
	VerifiedAt      *time.Time `json:"verified_at,omitempty"`
	RejectionReason *string    `json:"rejection_reason,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
	// PW-042: verified by the carrier's address check rather than a person,
	// and the carrier's answer for this version.
	VerifiedByCarrier bool                  `json:"verified_by_carrier"`
	CarrierCheck      *carrierCheckResponse `json:"carrier_check,omitempty"`
}

type carrierCheckResponse struct {
	Result    string    `json:"result"`
	Reason    *string   `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

func toReturnDestination(d *domain.ReturnDestination) returnDestinationResponse {
	out := returnDestinationResponse{VendorID: d.VendorID, AddressID: d.AddressID, ReceivingHours: d.ReceivingHours, Version: d.Version,
		Verified: d.Verified(), RejectionReason: d.RejectionReason, UpdatedAt: d.UpdatedAt}
	if d.Verified() {
		out.VerifiedAt = d.VerifiedAt
		out.VerifiedByCarrier = d.VerifiedByCarrier()
	}
	if c := d.CurrentCarrierCheck(); c != nil {
		out.CarrierCheck = &carrierCheckResponse{Result: c.Result, Reason: c.Reason, CheckedAt: c.CheckedAt}
	}
	if a := d.Address; a != nil {
		out.RecipientName, out.Phone, out.Province, out.District, out.Ward, out.StreetAddress =
			a.RecipientName, a.Phone, a.Province, a.District, a.Ward, a.StreetAddress
	}
	return out
}

func (h ReturnDestinationHandler) respond(c *gin.Context, d *domain.ReturnDestination, err error) {
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toReturnDestination(d))
}

func (h ReturnDestinationHandler) Get(c *gin.Context) {
	d, err := h.UseCase.Get(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"))
	h.respond(c, d, err)
}

func (h ReturnDestinationHandler) Set(c *gin.Context) {
	var in struct {
		AddressID      string `json:"address_id" binding:"required,uuid"`
		ReceivingHours string `json:"receiving_hours" binding:"required,max=200"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpresponse.HandleError(c, h.Log, apperror.Validation("address_id and receiving_hours are required"))
		return
	}
	d, err := h.UseCase.Set(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), in.AddressID, in.ReceivingHours)
	h.respond(c, d, err)
}

func (h ReturnDestinationHandler) AdminGet(c *gin.Context) {
	d, err := h.UseCase.AdminGet(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"))
	h.respond(c, d, err)
}

func (h ReturnDestinationHandler) Decide(c *gin.Context) {
	var in struct {
		Version int64  `json:"version" binding:"required,min=1"`
		Verify  *bool  `json:"verify" binding:"required"`
		Reason  string `json:"reason" binding:"required,max=200"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpresponse.HandleError(c, h.Log, apperror.Validation("version, verify and reason are required"))
		return
	}
	d, err := h.UseCase.Decide(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), in.Version, *in.Verify, in.Reason)
	h.respond(c, d, err)
}

// Verified: Order's read at return approval (404 when none is verified).
func (h ReturnDestinationHandler) Verified(c *gin.Context) {
	d, err := h.UseCase.Verified(c.Request.Context(), c.Param("vendorId"))
	h.respond(c, d, err)
}
