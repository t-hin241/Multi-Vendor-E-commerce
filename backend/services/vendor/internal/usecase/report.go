package usecase

import (
	"context"
	"time"

	"shopee/backend/pkg/vendorreport"
)

type Dashboard struct {
	Vendors          *VendorUseCase
	Orders, Payments vendorreport.Reader
}
type DashboardReport struct {
	Orders              *vendorreport.Report `json:"orders"`
	Payments            *vendorreport.Report `json:"payments"`
	OrdersUnavailable   bool                 `json:"orders_unavailable"`
	PaymentsUnavailable bool                 `json:"payments_unavailable"`
	AsOf                time.Time            `json:"as_of"`
}

func (d Dashboard) Get(ctx context.Context, user, id string, r vendorreport.Range) (*DashboardReport, error) {
	if _, err := d.Vendors.GetOwned(ctx, user, id); err != nil {
		return nil, err
	}
	out := &DashboardReport{AsOf: time.Now().UTC()}
	var err error
	out.Orders, err = d.Orders.Report(ctx, id, r)
	out.OrdersUnavailable = err != nil
	out.Payments, err = d.Payments.Report(ctx, id, r)
	out.PaymentsUnavailable = err != nil
	return out, nil
}
