package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/usecase"
)

// SettlementHoldHandler serves the hold contract Order calls (00 §6.1) and
// the finance view of holds.
type SettlementHoldHandler struct {
	holds *usecase.SettlementHoldUseCase
	log   zerolog.Logger
}

func NewSettlementHoldHandler(holds *usecase.SettlementHoldUseCase, log zerolog.Logger) *SettlementHoldHandler {
	return &SettlementHoldHandler{holds: holds, log: log}
}

type holdResponse struct {
	HoldID               string     `json:"hold_id"`
	VendorID             *string    `json:"vendor_id,omitempty"`
	VendorOrderID        *string    `json:"vendor_order_id,omitempty"`
	SourceType           *string    `json:"source_type,omitempty"`
	SourceID             *string    `json:"source_id,omitempty"`
	SourceVersion        *int64     `json:"source_version,omitempty"`
	ReasonCode           *string    `json:"reason_code,omitempty"`
	Status               string     `json:"status"`
	PayoutClaimed        bool       `json:"payout_claimed"`
	AcquiredAt           *time.Time `json:"acquired_at,omitempty"`
	ReleaseOperationID   *string    `json:"release_operation_id,omitempty"`
	ReleaseSourceVersion *int64     `json:"release_source_version,omitempty"`
	ResolutionRef        *string    `json:"resolution_ref,omitempty"`
	ReleaseReason        *string    `json:"release_reason,omitempty"`
	ReleasedAt           *time.Time `json:"released_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
}

func toHold(h *domain.SettlementHold) holdResponse {
	return holdResponse{HoldID: h.ID, VendorID: h.VendorID, VendorOrderID: h.VendorOrderID, SourceType: h.SourceType, SourceID: h.SourceID,
		SourceVersion: h.SourceVersion, ReasonCode: h.ReasonCode, Status: string(h.Status), PayoutClaimed: h.PayoutClaimed, AcquiredAt: h.AcquiredAt,
		ReleaseOperationID: h.ReleaseOperationID, ReleaseSourceVersion: h.ReleaseSourceVersion, ResolutionRef: h.ResolutionRef,
		ReleaseReason: h.ReleaseReason, ReleasedAt: h.ReleasedAt, CreatedAt: h.CreatedAt}
}

type acquireHoldRequest struct {
	HoldID        string `json:"hold_id" binding:"required"`
	VendorID      string `json:"vendor_id" binding:"required"`
	VendorOrderID string `json:"vendor_order_id" binding:"required"`
	SourceType    string `json:"source_type" binding:"required,max=40"`
	SourceID      string `json:"source_id" binding:"required"`
	SourceVersion int64  `json:"source_version"`
	ReasonCode    string `json:"reason_code" binding:"required,max=60"`
}

// Acquire: 201 new, 200 replay; 409 payout_already_claimed when a payout
// claimed the vendor order first (the hold is still recorded).
func (h *SettlementHoldHandler) Acquire(c *gin.Context) {
	var req acquireHoldRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "hold_id, vendor_id, vendor_order_id, source_type, source_id and reason_code are required")
		return
	}
	hold, created, err := h.holds.Acquire(c.Request.Context(), domain.HoldRequest{HoldID: req.HoldID, VendorID: req.VendorID,
		VendorOrderID: req.VendorOrderID, SourceType: req.SourceType, SourceID: req.SourceID, SourceVersion: req.SourceVersion, ReasonCode: req.ReasonCode})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	if hold.PayoutClaimed && hold.Status == domain.HoldActive {
		httpresponse.HandleError(c, h.log, domain.ErrPayoutAlreadyClaimed)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpresponse.OK(c, status, toHold(hold))
}

func (h *SettlementHoldHandler) Get(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("holdID")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid hold id")
		return
	}
	hold, err := h.holds.Get(c.Request.Context(), c.Param("holdID"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toHold(hold))
}

type releaseHoldRequest struct {
	OperationID   string `json:"operation_id" binding:"required,max=100"`
	SourceVersion int64  `json:"source_version"`
	ResolutionRef string `json:"resolution_ref" binding:"max=100"`
	Reason        string `json:"reason" binding:"required,max=500"`
}

func (h *SettlementHoldHandler) Release(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("holdID")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid hold id")
		return
	}
	var req releaseHoldRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "operation_id and reason are required")
		return
	}
	hold, err := h.holds.Release(c.Request.Context(), c.Param("holdID"), domain.HoldRelease{OperationID: req.OperationID,
		SourceVersion: req.SourceVersion, ResolutionRef: req.ResolutionRef, Reason: req.Reason})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toHold(hold))
}

// AdminList: active holds by default; ?vendor_order_id= or ?status=all.
func (h *SettlementHoldHandler) AdminList(c *gin.Context) {
	var vendorOrderID *string
	if raw := c.Query("vendor_order_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid vendor order id")
			return
		}
		vendorOrderID = &raw
	}
	holds, err := h.holds.List(c.Request.Context(), vendorOrderID, c.Query("status") != "all",
		boundedInt(c.Query("limit"), 50, 1, 100), boundedInt(c.Query("offset"), 0, 0, 10_000))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]holdResponse, 0, len(holds))
	for _, hold := range holds {
		out = append(out, toHold(hold))
	}
	httpresponse.OK(c, http.StatusOK, out)
}
