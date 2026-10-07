package usecase

import (
	"context"
	"time"

	"shopee/backend/pkg/shopaccess"
	"shopee/backend/pkg/vendorreport"
)

type Dashboard struct {
	Vendors          *VendorUseCase
	Orders, Payments vendorreport.Reader
	// Access checks shop permissions (AF-17): the report needs
	// analytics.read, its payment part also finance.read.
	Access *StaffUseCase
}
type DashboardReport struct {
	Orders              *vendorreport.Report `json:"orders"`
	Payments            *vendorreport.Report `json:"payments"`
	OrdersUnavailable   bool                 `json:"orders_unavailable"`
	PaymentsUnavailable bool                 `json:"payments_unavailable"`
	PaymentsRestricted  bool                 `json:"payments_restricted,omitempty"`
	AsOf                time.Time            `json:"as_of"`
}

func (d Dashboard) Get(ctx context.Context, user, id string, r vendorreport.Range) (*DashboardReport, error) {
	finance := true
	if d.Access == nil {
		if _, err := d.Vendors.GetOwned(ctx, user, id); err != nil {
			return nil, err
		}
	} else {
		m, err := d.Access.Require(ctx, user, id, shopaccess.AnalyticsRead)
		if err != nil {
			return nil, err
		}
		finance = m.Has(shopaccess.FinanceRead)
	}
	out := &DashboardReport{AsOf: time.Now().UTC()}
	var err error
	out.Orders, err = d.Orders.Report(ctx, id, r)
	out.OrdersUnavailable = err != nil
	if !finance {
		out.PaymentsRestricted = true
		return out, nil
	}
	out.Payments, err = d.Payments.Report(ctx, id, r)
	out.PaymentsUnavailable = err != nil
	return out, nil
}
