package adapter

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
)

func TestSendShipmentEventClassifiesOrdersAnswer(t *testing.T) {
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(serviceauth.Header) != "fake-key-not-a-real-secret" || r.URL.Path != "/internal/shipment-events" {
			t.Error("unexpected request")
		}
		w.WriteHeader(status)
	}))
	defer server.Close()
	c := NewHTTPOrderClient(server.URL, "fake-key-not-a-real-secret")
	e := ShipmentEvent{EventID: "e", ShipmentID: "s", VendorOrderID: "v", Type: "delivered", OccurredAt: time.Now()}
	if err := c.SendShipmentEvent(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	var app *apperror.Error
	status = 409
	if err := c.SendShipmentEvent(t.Context(), e); !errors.As(err, &app) || app.Code != apperror.CodeConflict {
		t.Fatalf("Order's refusal must be parked for review, got %v", err)
	}
	status = 503
	if err := c.SendShipmentEvent(t.Context(), e); !errors.As(err, &app) || app.Code != apperror.CodeInternal {
		t.Fatalf("an outage must be retried, got %v", err)
	}
}
