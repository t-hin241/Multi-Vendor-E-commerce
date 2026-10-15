package transport

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// AvailabilityHandler serves AF-18 shop vacations to shop members. Who may
// change the schedule (shop.availability.write) is decided in the use case.
type AvailabilityHandler struct {
	UseCase *usecase.AvailabilityUseCase
	Log     zerolog.Logger
}

func (h AvailabilityHandler) Register(r *gin.Engine, auth gin.HandlerFunc) {
	g := r.Group("/api/vendor/:vendorId/availability", auth, middleware.RequireRole("vendor", "buyer"))
	g.GET("", h.Get)
	g.PUT("", h.Put)
	g.DELETE("", h.Delete)
}

type intervalResponse struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at,omitempty"`
	Message   string     `json:"message"`
	PausedAt  *time.Time `json:"paused_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type availabilityResponse struct {
	VendorID            string            `json:"vendor_id"`
	ShopStatus          string            `json:"shop_status"`
	AcceptingOrders     bool              `json:"accepting_orders"`
	AvailabilityVersion int64             `json:"availability_version"`
	SaleVersion         int64             `json:"sale_version"`
	EnforcementPending  bool              `json:"enforcement_pending"`
	NextOpenAt          *time.Time        `json:"next_open_at,omitempty"`
	Current             *intervalResponse `json:"current,omitempty"`
	Enabled             bool              `json:"enabled"`
}

func (h AvailabilityHandler) respond(c *gin.Context, status int, a *domain.Availability, err error) {
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	out := availabilityResponse{VendorID: a.VendorID, ShopStatus: string(a.Status), AcceptingOrders: a.AcceptingOrders,
		AvailabilityVersion: a.AvailabilityVersion, SaleVersion: a.SaleVersion, EnforcementPending: a.EnforcementPending(),
		NextOpenAt: a.NextOpenAt(), Enabled: h.UseCase.Enabled}
	if i := a.Current; i != nil {
		out.Current = &intervalResponse{ID: i.ID, Status: string(i.Status), StartsAt: i.StartsAt, EndsAt: i.EndsAt, Message: i.Message,
			PausedAt: i.PausedAt, UpdatedAt: i.UpdatedAt}
	}
	httpresponse.OK(c, status, out)
}

func (h AvailabilityHandler) Get(c *gin.Context) {
	a, err := h.UseCase.Get(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"))
	h.respond(c, http.StatusOK, a, err)
}

// putAvailabilityRequest: times are RFC3339 with an explicit offset (Z or
// ±hh:mm); a local time without a zone is refused rather than guessed.
type putAvailabilityRequest struct {
	StartsAt        *string `json:"starts_at"`
	EndsAt          *string `json:"ends_at"`
	Message         string  `json:"message"`
	ExpectedVersion *int64  `json:"expected_version"`
}

func parseInstant(field string, raw *string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, domain.ErrInvalidVacation(field + " must be an RFC3339 time with a time zone offset")
	}
	return &t, nil
}

func (h AvailabilityHandler) Put(c *gin.Context) {
	var req putAvailabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedVersion == nil {
		httpresponse.HandleError(c, h.Log, apperror.Validation("expected_version is required"))
		return
	}
	starts, err := parseInstant("starts_at", req.StartsAt)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	ends, err := parseInstant("ends_at", req.EndsAt)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	a, err := h.UseCase.Schedule(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"),
		domain.VacationInput{StartsAt: starts, EndsAt: ends, Message: req.Message, ExpectedVersion: *req.ExpectedVersion})
	// 202: the pause reaches Catalog and Order asynchronously
	// (enforcement_pending until the status outbox delivered it).
	h.respond(c, http.StatusAccepted, a, err)
}

func (h AvailabilityHandler) Delete(c *gin.Context) {
	expected, err := strconv.ParseInt(c.Query("expected_version"), 10, 64)
	if err != nil || expected < 0 {
		httpresponse.HandleError(c, h.Log, apperror.Validation("expected_version is required"))
		return
	}
	a, err := h.UseCase.Resume(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), expected)
	h.respond(c, http.StatusAccepted, a, err)
}
