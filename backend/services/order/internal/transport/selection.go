package transport

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

var selectionRefusals = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "checkout_selection_refusals_total", Help: "Checkout selection refusals by bounded business code.",
}, []string{"code"})

func recordSelectionRefusal(err error) {
	var app *apperror.Error
	if !errors.As(err, &app) {
		return
	}
	switch app.Code {
	case "invalid_selection", "selection_stale", "line_claimed", "checkout_total_changed", "selective_checkout_disabled":
		selectionRefusals.WithLabelValues(string(app.Code)).Inc()
	}
}

func (h *OrderHandler) CheckoutCapabilities(c *gin.Context) {
	enabled, err := h.orders.SelectiveCheckoutCapability(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, gin.H{"selective_checkout_enabled": enabled})
}
