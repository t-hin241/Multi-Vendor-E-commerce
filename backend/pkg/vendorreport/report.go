// Package vendorreport defines Order and Payment's vendor reporting contracts.
package vendorreport

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
)

type Range struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Currency string    `json:"currency"`
}

func Parse(from, to, currency string) (Range, error) {
	r := Range{Currency: currency}
	if r.Currency == "" {
		r.Currency = "VND"
	}
	if len(r.Currency) != 3 {
		return r, apperror.Validation("Currency must be an ISO uppercase code")
	}
	for _, c := range r.Currency {
		if c < 'A' || c > 'Z' {
			return r, apperror.Validation("Invalid currency")
		}
	}
	var err error
	r.From, err = time.Parse(time.RFC3339, from)
	if err != nil {
		return r, apperror.Validation("from must be RFC3339")
	}
	r.To, err = time.Parse(time.RFC3339, to)
	if err != nil {
		return r, apperror.Validation("to must be RFC3339")
	}
	if !r.To.After(r.From) || r.To.Sub(r.From) > 366*24*time.Hour {
		return r, apperror.Validation("Range must be positive and at most 366 days")
	}
	return r, nil
}

type Amount struct {
	Value     *int64 `json:"value"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

func Known(v int64) Amount         { return Amount{Value: &v, Available: true} }
func Unknown(reason string) Amount { return Amount{Reason: reason} }

type Report struct {
	Range               Range     `json:"range"`
	AsOf                time.Time `json:"as_of"`
	GrossOrdered        Amount    `json:"gross_ordered"`
	Captured            Amount    `json:"captured"`
	Refunded            Amount    `json:"refunded"`
	Eligible            Amount    `json:"eligible"`
	PaidOut             Amount    `json:"paid_out"`
	TotalOrders         int64     `json:"total_orders"`
	AwaitingFulfillment int64     `json:"awaiting_fulfillment"`
	Stale               bool      `json:"stale"`
}
type Reader interface {
	Report(context.Context, string, Range) (*Report, error)
}

// Service bounds report queries before they reach a service-owned repository.
type Service struct{ Repository Reader }

func (s Service) Report(ctx context.Context, id string, r Range) (*Report, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperror.Validation("Invalid shop ID")
	}
	if _, err := Parse(r.From.Format(time.RFC3339), r.To.Format(time.RFC3339), r.Currency); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := s.Repository.Report(ctx, id, r)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return out, nil
}
func Handler(reader Reader, log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		r, err := Parse(c.Query("from"), c.Query("to"), c.Query("currency"))
		if err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		out, err := reader.Report(c.Request.Context(), c.Param("vendorId"), r)
		if err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		httpresponse.OK(c, 200, out)
	}
}
