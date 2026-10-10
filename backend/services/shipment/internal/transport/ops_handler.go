package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/usecase"
)

// OpsHandler serves admin fulfillment operations: the attention lists and
// audited actions an operator takes after talking to the carrier.
type OpsHandler struct {
	shipments *usecase.ShipmentUseCase
	log       zerolog.Logger
}

func NewOpsHandler(shipments *usecase.ShipmentUseCase, log zerolog.Logger) *OpsHandler {
	return &OpsHandler{shipments: shipments, log: log}
}

func admin(c *gin.Context) usecase.Actor {
	return usecase.Actor{ID: middleware.GetUserID(c), Role: domain.ActorAdmin}
}

func validShipmentID(c *gin.Context) bool {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid shipment id")
		return false
	}
	return true
}

func (h *OpsHandler) respond(c *gin.Context, s *domain.Shipment, err error) {
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toShipmentResponse(s))
}

func (h *OpsHandler) Operations(c *gin.Context) {
	ops, err := h.shipments.AdminOperations(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	lists := gin.H{}
	for kind, list := range ops.Lists {
		lists[kind] = toShipmentResponseList(list)
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"counts": ops.Counts, "outbox": ops.Outbox, "lists": lists})
}

func (h *OpsHandler) Get(c *gin.Context) {
	if !validShipmentID(c) {
		return
	}
	s, err := h.shipments.AdminGet(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	h.respond(c, s, err)
}

func (h *OpsHandler) MarkDelivered(c *gin.Context) {
	if !validShipmentID(c) {
		return
	}
	var req noteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "note must be at most 500 characters")
		return
	}
	s, err := h.shipments.MarkDelivered(c.Request.Context(), admin(c), c.Param("id"), req.Note)
	h.respond(c, s, err)
}

func (h *OpsHandler) bindReason(c *gin.Context) (string, bool) {
	if !validShipmentID(c) {
		return "", false
	}
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reason is required")
		return "", false
	}
	return req.Reason, true
}

func (h *OpsHandler) FailedAttempt(c *gin.Context) {
	reason, ok := h.bindReason(c)
	if !ok {
		return
	}
	s, err := h.shipments.RecordFailedAttempt(c.Request.Context(), admin(c), c.Param("id"), reason)
	h.respond(c, s, err)
}

func (h *OpsHandler) MarkReturned(c *gin.Context) {
	reason, ok := h.bindReason(c)
	if !ok {
		return
	}
	s, err := h.shipments.MarkReturned(c.Request.Context(), admin(c), c.Param("id"), reason)
	h.respond(c, s, err)
}

// FailureReport: an admin records a returned or lost package (AF-04);
// lost needs the carrier's confirmation.
func (h *OpsHandler) FailureReport(c *gin.Context) {
	if !validShipmentID(c) {
		return
	}
	var req failureReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "kind (returned or lost), reason and expected_version are required")
		return
	}
	s, err := h.shipments.ReportFailure(c.Request.Context(), admin(c), c.Param("id"),
		usecase.FailureReport{Kind: req.Kind, Reason: req.Reason, ExpectedVersion: req.ExpectedVersion, EvidenceIDs: req.EvidenceIDs})
	h.respond(c, s, err)
}

func (h *OpsHandler) UpdateTracking(c *gin.Context) {
	if !validShipmentID(c) {
		return
	}
	var req updateTrackingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "tracking_number and reason are required")
		return
	}
	s, err := h.shipments.UpdateTracking(c.Request.Context(), admin(c), c.Param("id"), req.TrackingNumber, req.Reason)
	h.respond(c, s, err)
}

func (h *OpsHandler) ResolveInterception(c *gin.Context) {
	if !validShipmentID(c) {
		return
	}
	var req interceptionDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "accepted and note are required")
		return
	}
	s, err := h.shipments.ResolveInterception(c.Request.Context(), admin(c), c.Param("id"), req.Accepted, req.Note)
	h.respond(c, s, err)
}

type retryOrderEventRequest struct {
	ShipmentID string `json:"shipment_id" binding:"required,uuid"`
	Reason     string `json:"reason" binding:"required,max=500"`
}

func (h *OpsHandler) RetryOrderEvent(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid event id")
		return
	}
	var req retryOrderEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "shipment_id and reason are required")
		return
	}
	if err := h.shipments.RetryOrderEvent(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.ShipmentID, req.Reason); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"requeued": true})
}
