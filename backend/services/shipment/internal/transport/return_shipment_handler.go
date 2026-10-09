package transport

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/usecase"
)

// ReturnShipmentHandler serves AF-05 return parcels: Order's commands
// (service key) and the operators' list.
type ReturnShipmentHandler struct {
	Returns *usecase.ReturnShipmentUseCase
	Log     zerolog.Logger
}

// Register adds the internal routes (Order only) and the admin list to an
// admin group already guarded by AdminRoutes.
func (h ReturnShipmentHandler) Register(r *gin.Engine, admin *gin.RouterGroup, internal *serviceauth.Verifier) {
	group := r.Group("/internal/shipments/return-shipments", internal.Allow("order"))
	group.POST("", h.Authorize)
	group.POST("/:id/dispatches", h.Dispatch)
	group.POST("/:id/receipts", h.Receive)
	group.POST("/:id/exceptions", h.Exception)
	admin.GET("/return-shipments", h.AdminList)
}

type returnShipmentResponse struct {
	ID                   string     `json:"id"`
	ReturnID             string     `json:"return_id"`
	OrderID              string     `json:"order_id"`
	VendorID             string     `json:"vendor_id"`
	AuthorizationVersion int        `json:"authorization_version"`
	Status               string     `json:"status"`
	Province             string     `json:"province"`
	District             string     `json:"district"`
	CarrierName          *string    `json:"carrier_name,omitempty"`
	TrackingNumber       *string    `json:"tracking_number,omitempty"`
	DispatchedAt         *time.Time `json:"dispatched_at,omitempty"`
	ReceivedAt           *time.Time `json:"received_at,omitempty"`
	ExceptionReason      *string    `json:"exception_reason,omitempty"`
	Version              int64      `json:"version"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func toReturnShipment(s *domain.ReturnShipment) returnShipmentResponse {
	return returnShipmentResponse{ID: s.ID, ReturnID: s.ReturnID, OrderID: s.OrderID, VendorID: s.VendorID, AuthorizationVersion: s.AuthorizationVersion,
		Status: string(s.Status), Province: s.Province, District: s.District, CarrierName: s.CarrierName, TrackingNumber: s.TrackingNumber,
		DispatchedAt: s.DispatchedAt, ReceivedAt: s.ReceivedAt, ExceptionReason: s.ExceptionReason, Version: s.Version,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}

func (h ReturnShipmentHandler) respond(c *gin.Context, status int, s *domain.ReturnShipment, err error) {
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, status, toReturnShipment(s))
}

type returnAuthorizationRequest struct {
	OperationID          string             `json:"operation_id" binding:"required,max=100"`
	ReturnID             string             `json:"return_id" binding:"required,uuid"`
	OrderID              string             `json:"order_id" binding:"required,uuid"`
	VendorID             string             `json:"vendor_id" binding:"required,uuid"`
	BuyerID              string             `json:"buyer_id" binding:"required,uuid"`
	AuthorizationVersion int                `json:"authorization_version" binding:"required,min=1"`
	Destination          destinationRequest `json:"destination_snapshot" binding:"required"`
	ReceivingHours       string             `json:"receiving_hours" binding:"required,max=200"`
}

// Authorize: POST /internal/shipments/return-shipments → 201 with the
// parcel (the same parcel for a repeat).
func (h ReturnShipmentHandler) Authorize(c *gin.Context) {
	var req returnAuthorizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"operation_id, return_id, order_id, vendor_id, buyer_id, authorization_version, destination_snapshot and receiving_hours are required")
		return
	}
	d := req.Destination
	s, err := h.Returns.Authorize(c.Request.Context(), usecase.ReturnAuthorization{OperationID: req.OperationID, ReturnID: req.ReturnID,
		OrderID: req.OrderID, VendorID: req.VendorID, BuyerID: req.BuyerID, AuthorizationVersion: req.AuthorizationVersion,
		Destination: usecase.Destination{RecipientName: d.RecipientName, Phone: d.Phone, Province: d.Province, District: d.District,
			Ward: d.Ward, StreetAddress: d.StreetAddress}, ReceivingHours: req.ReceivingHours})
	h.respond(c, http.StatusCreated, s, err)
}

type returnDispatchRequest struct {
	OperationID    string    `json:"operation_id" binding:"required,max=100"`
	CarrierName    string    `json:"carrier_name" binding:"required,max=60"`
	TrackingNumber string    `json:"tracking_number" binding:"required,max=64"`
	DispatchedAt   time.Time `json:"dispatched_at" binding:"required"`
}

func (h ReturnShipmentHandler) Dispatch(c *gin.Context) {
	if !validShipmentID(c) {
		return
	}
	var req returnDispatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "operation_id, carrier_name, tracking_number and dispatched_at are required")
		return
	}
	s, err := h.Returns.Dispatch(c.Request.Context(), c.Param("id"), usecase.ReturnDispatch{OperationID: req.OperationID,
		CarrierName: req.CarrierName, TrackingNumber: req.TrackingNumber, DispatchedAt: req.DispatchedAt})
	h.respond(c, http.StatusOK, s, err)
}

func (h ReturnShipmentHandler) Receive(c *gin.Context) {
	if !validShipmentID(c) {
		return
	}
	s, err := h.Returns.Receive(c.Request.Context(), c.Param("id"))
	h.respond(c, http.StatusOK, s, err)
}

func (h ReturnShipmentHandler) Exception(c *gin.Context) {
	if !validShipmentID(c) {
		return
	}
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reason is required")
		return
	}
	s, err := h.Returns.Exception(c.Request.Context(), c.Param("id"), req.Reason)
	h.respond(c, http.StatusOK, s, err)
}

func (h ReturnShipmentHandler) AdminList(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 || offset > 10_000 {
		offset = 0
	}
	items, err := h.Returns.AdminList(c.Request.Context(), middleware.GetUserID(c), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	out := make([]returnShipmentResponse, 0, len(items))
	for _, s := range items {
		out = append(out, toReturnShipment(s))
	}
	httpresponse.OK(c, http.StatusOK, out)
}
