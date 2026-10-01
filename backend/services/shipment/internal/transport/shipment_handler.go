package transport

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/usecase"
)

type ShipmentHandler struct {
	shipments *usecase.ShipmentUseCase
	log       zerolog.Logger
}

func NewShipmentHandler(shipments *usecase.ShipmentUseCase, log zerolog.Logger) *ShipmentHandler {
	return &ShipmentHandler{shipments: shipments, log: log}
}

// Create is the vendor-triggered fallback for when the automatic
// checkout-time creation call failed — see ShipmentUseCase.CreateOrGet.
func (h *ShipmentHandler) Create(c *gin.Context) {
	var req createShipmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	shipment, err := h.shipments.CreateOrGet(c.Request.Context(), middleware.GetUserID(c), req.VendorOrderID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toShipmentResponse(shipment))
}

func vendor(c *gin.Context) usecase.Actor {
	return usecase.Actor{ID: middleware.GetUserID(c), Role: domain.ActorVendor}
}

func (h *ShipmentHandler) respond(c *gin.Context, s *domain.Shipment, err error) {
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toShipmentResponse(s.ForVendor()))
}

func (h *ShipmentHandler) MarkReady(c *gin.Context) {
	s, err := h.shipments.MarkReady(c.Request.Context(), vendor(c), c.Param("id"))
	h.respond(c, s, err)
}

func (h *ShipmentHandler) MarkShipped(c *gin.Context) {
	var req markShippedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "tracking_number is required")
		return
	}
	s, err := h.shipments.MarkShipped(c.Request.Context(), vendor(c), c.Param("id"), req.TrackingNumber)
	h.respond(c, s, err)
}

func (h *ShipmentHandler) UpdateTracking(c *gin.Context) {
	var req updateTrackingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "tracking_number and reason are required")
		return
	}
	s, err := h.shipments.UpdateTracking(c.Request.Context(), vendor(c), c.Param("id"), req.TrackingNumber, req.Reason)
	h.respond(c, s, err)
}

func (h *ShipmentHandler) FailedAttempt(c *gin.Context) {
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reason is required")
		return
	}
	s, err := h.shipments.RecordFailedAttempt(c.Request.Context(), vendor(c), c.Param("id"), req.Reason)
	h.respond(c, s, err)
}

func (h *ShipmentHandler) MarkDelivered(c *gin.Context) {
	var req noteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "note must be at most 500 characters")
		return
	}
	s, err := h.shipments.MarkDelivered(c.Request.Context(), vendor(c), c.Param("id"), req.Note)
	h.respond(c, s, err)
}

func (h *ShipmentHandler) MarkReturned(c *gin.Context) {
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reason is required")
		return
	}
	s, err := h.shipments.MarkReturned(c.Request.Context(), vendor(c), c.Param("id"), req.Reason)
	h.respond(c, s, err)
}

func (h *ShipmentHandler) ResolveInterception(c *gin.Context) {
	var req interceptionDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "accepted and note are required")
		return
	}
	s, err := h.shipments.ResolveInterception(c.Request.Context(), vendor(c), c.Param("id"), req.Accepted, req.Note)
	h.respond(c, s, err)
}

func (h *ShipmentHandler) Advance(c *gin.Context) {
	var req advanceShipmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	shipment, err := h.shipments.Advance(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), domain.Status(req.Status), req.TrackingNumber)
	h.respond(c, shipment, err)
}

// ListMine serves both roles on the same route (Gin allows only one
// handler per method+path, so the vendor and buyer views can't live in
// separate route groups at the identical "/mine" path) — it dispatches on
// the caller's own role rather than trusting anything the client sent.
func (h *ShipmentHandler) ListMine(c *gin.Context) {
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset := parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)

	var shipments []*domain.Shipment
	var err error
	if middleware.GetRole(c) == "buyer" {
		shipments, err = h.shipments.ListForBuyer(c.Request.Context(), middleware.GetUserID(c), limit, offset)
	} else {
		shipments, err = h.shipments.ListMine(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), limit, offset)
	}
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toShipmentResponseList(shipments))
}

// SimulateCarrierDecision lets a vendor stand in for the carrier's own
// callback in local/dev environments — see
// ShipmentUseCase.SimulateCarrierDecision.
func (h *ShipmentHandler) SimulateCarrierDecision(c *gin.Context) {
	var req simulateCarrierDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	shipment, err := h.shipments.SimulateCarrierDecision(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Accepted, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toShipmentResponse(shipment))
}

func (h *ShipmentHandler) GetByVendorOrderID(c *gin.Context) {
	shipment, err := h.shipments.GetByVendorOrderID(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorOrderID"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toShipmentResponse(shipment))
}

// ListEvents is available to either the shipment's own vendor or buyer —
// ownership is checked inside the usecase against whichever role the
// caller has.
func (h *ShipmentHandler) ListEvents(c *gin.Context) {
	events, err := h.shipments.ListEventsForShipment(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toTrackingEventResponseList(events))
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
