package usecase

import (
	"context"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// OrderDetail is one order with everything its viewer may see.
type OrderDetail struct {
	Order        *domain.Order
	Items        []*domain.OrderItem
	VendorOrders []*domain.VendorOrder
	Refunds      []*domain.Refund
	Returns      []*domain.ReturnRequest
	Payments     []*domain.OrderPayment // admin only
	Effects      []*domain.Effect       // admin only
}

// ListFilter narrows an order listing.
type ListFilter struct {
	Status  string
	BuyerID string
	From    *time.Time
	To      *time.Time
}

func validOrderStatus(status string) error {
	if status == "" {
		return nil
	}
	for _, s := range []domain.Status{domain.StatusPendingPayment, domain.StatusPaid, domain.StatusProcessing, domain.StatusShipped,
		domain.StatusCompleted, domain.StatusCancelled, domain.StatusRefunded} {
		if string(s) == status {
			return nil
		}
	}
	return apperror.Validation("Invalid order status filter")
}

// ListMine is the buyer's own order list with its total count.
func (uc *OrderUseCase) ListMine(ctx context.Context, buyerID, status string, limit, offset int) ([]*domain.Order, int64, error) {
	if err := validOrderStatus(status); err != nil {
		return nil, 0, err
	}
	orders, total, err := uc.Orders.List(ctx, repository.OrderFilter{BuyerID: buyerID, Status: status}, limit, offset)
	return orders, total, asError(err)
}

// AdminList lists every buyer's orders with filters and a total count.
func (uc *OrderUseCase) AdminList(ctx context.Context, f ListFilter, limit, offset int) ([]*domain.Order, int64, error) {
	if err := validOrderStatus(f.Status); err != nil {
		return nil, 0, err
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return nil, 0, apperror.Validation("from must be before to")
	}
	orders, total, err := uc.Orders.List(ctx, repository.OrderFilter{BuyerID: f.BuyerID, Status: f.Status, From: f.From, To: f.To}, limit, offset)
	return orders, total, asError(err)
}

// GetOwnedByBuyer is the buyer's order detail.
func (uc *OrderUseCase) GetOwnedByBuyer(ctx context.Context, buyerID, orderID string) (*OrderDetail, error) {
	order, err := uc.findOwnedByBuyer(ctx, buyerID, orderID)
	if err != nil {
		return nil, err
	}
	return uc.detail(ctx, order, false)
}

// AdminGetOrder is the admin's full view of one order, including captures
// and side-effect state.
func (uc *OrderUseCase) AdminGetOrder(ctx context.Context, orderID string) (*OrderDetail, error) {
	order, err := uc.findOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	return uc.detail(ctx, order, true)
}

func (uc *OrderUseCase) detail(ctx context.Context, order *domain.Order, admin bool) (*OrderDetail, error) {
	d := &OrderDetail{Order: order}
	var err error
	if d.Items, err = uc.Orders.ListItemsByOrder(ctx, order.ID); err != nil {
		return nil, appError(err)
	}
	if d.VendorOrders, err = uc.VendorOrders.ListByOrderID(ctx, order.ID); err != nil {
		return nil, appError(err)
	}
	if d.Refunds, err = uc.Refunds.ListByOrder(ctx, order.ID); err != nil {
		return nil, appError(err)
	}
	if d.Returns, err = uc.Returns.ListByOrder(ctx, order.ID); err != nil {
		return nil, appError(err)
	}
	if admin {
		if d.Payments, err = uc.Payments.ListByOrder(ctx, order.ID); err != nil {
			return nil, appError(err)
		}
		if d.Effects, err = uc.Effects.ListByOrder(ctx, order.ID); err != nil {
			return nil, appError(err)
		}
	}
	return d, nil
}

// ListVendorMine is the vendor's own order list with items.
func (uc *OrderUseCase) ListVendorMine(ctx context.Context, userID, vendorID, status string, limit, offset int) ([]*domain.VendorOrder, map[string][]*domain.OrderItem, error) {
	if err := validOrderStatus(status); err != nil {
		return nil, nil, err
	}
	vendorID, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vendorID, shopaccess.OrdersRead)
	if err != nil {
		return nil, nil, err
	}
	vendorOrders, err := uc.VendorOrders.ListByVendor(ctx, vendorID, status, limit, offset)
	if err != nil {
		return nil, nil, appError(err)
	}
	ids := make([]string, 0, len(vendorOrders))
	for _, vo := range vendorOrders {
		ids = append(ids, vo.ID)
	}
	items, err := uc.VendorOrders.ListItemsByVendorOrderIDs(ctx, ids)
	if err != nil {
		return nil, nil, appError(err)
	}
	return vendorOrders, items, nil
}

// GetVendorSummary aggregates only confirmed sales and confirmed refunds.
func (uc *OrderUseCase) GetVendorSummary(ctx context.Context, userID, vendorID string) (*domain.VendorSummary, []*domain.TopProduct, error) {
	vendorID, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vendorID, shopaccess.AnalyticsRead)
	if err != nil {
		return nil, nil, err
	}
	summary, err := uc.VendorOrders.SummaryByVendor(ctx, vendorID)
	if err != nil {
		return nil, nil, appError(err)
	}
	topProducts, err := uc.VendorOrders.TopProductsByVendor(ctx, vendorID, 5)
	if err != nil {
		return nil, nil, appError(err)
	}
	return summary, topProducts, nil
}

// VendorOrderForInternal serves Shipment: the vendor order, its parent and
// package weight, and whether fulfillment is open (verified capture,
// committed stock, finished checkout).
type VendorOrderForInternal struct {
	VendorOrder        *domain.VendorOrder
	Order              *domain.Order
	PackageWeightGrams int64
	Fulfillable        bool
}

func (uc *OrderUseCase) GetVendorOrderForInternal(ctx context.Context, vendorOrderID string) (*VendorOrderForInternal, error) {
	vo, err := uc.VendorOrders.FindByID(ctx, vendorOrderID)
	if err != nil {
		return nil, notFoundOrInternal(err, repository.ErrVendorOrderNotFound, "Order not found")
	}
	order, err := uc.findOrder(ctx, vo.OrderID)
	if err != nil {
		return nil, err
	}
	out := &VendorOrderForInternal{VendorOrder: vo, Order: order, Fulfillable: vo.Fulfillable() && order.CheckoutState == domain.CheckoutReady}
	if vo.Shipping != nil {
		out.PackageWeightGrams = vo.Shipping.PackageWeightGrams
	} else if out.PackageWeightGrams, err = uc.legacyPackageWeight(ctx, vo.ID); err != nil {
		uc.Log.Error().Err(err).Str("vendor_order_id", vo.ID).Msg("failed to recompute package weight")
	}
	return out, nil
}

// QuantitySoldByProductIDs serves Catalog's storefront listing.
func (uc *OrderUseCase) QuantitySoldByProductIDs(ctx context.Context, productIDs []string) (map[string]int64, error) {
	quantities, err := uc.VendorOrders.QuantitySoldByProductIDs(ctx, productIDs)
	return quantities, asError(err)
}

func (uc *OrderUseCase) ListReviewEligibility(ctx context.Context, buyerID, productID string) ([]*domain.ReviewEligibility, error) {
	items, err := uc.Orders.ListReviewEligibility(ctx, buyerID, productID)
	return items, asError(err)
}

// OrderStatusForInventory serves Inventory's reconciliation.
func (uc *OrderUseCase) OrderStatusForInventory(ctx context.Context, orderID string) (*domain.Order, error) {
	return uc.findOrder(ctx, orderID)
}
