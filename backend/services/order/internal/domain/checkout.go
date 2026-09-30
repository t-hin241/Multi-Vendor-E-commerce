package domain

import (
	"sort"
	"strconv"
	"time"

	"shopee/backend/pkg/apperror"
)

// Plan is an order fully computed from checkout lines but not yet
// persisted: no IDs, no timestamps. The repository assigns those on
// insert. Splitting lines into one VendorOrder per vendor is the checkout
// rule that keeps a multi-vendor cart from becoming a single order that
// spans bounded contexts.
type Plan struct {
	VendorVersions  map[string]int64
	ProductVersions map[string]int64
	Order           Order
	VendorOrders    []VendorOrder
	Items           []OrderItem // VendorOrderID left as the vendor's index into VendorOrders; the repository resolves it to a real id after insert.
	// CartConsumption, when set, is inserted (status held) in the same
	// transaction as the order; OrderID is filled in by the repository.
	CartConsumption *CartConsumption
	// CheckoutOperationID, when set, is linked to the order in the same
	// transaction, so a crash right after the insert is recoverable.
	CheckoutOperationID string
}

// ShippingQuote is Shipment's fee for one vendor's package, snapshotted onto
// the vendor order. A later fee-rule change never alters it.
type ShippingQuote struct {
	VendorID           string
	FeeAmount          int64
	Currency           string
	CarrierID          string
	ZoneID             string
	FeeRuleID          string
	FeeRuleVersion     int
	PackageWeightGrams int64
	QuotedAt           time.Time
}

// BuildCheckoutPlan groups checkout lines by vendor and computes every
// total. It fails if lines mix currencies, since Order doesn't do currency
// conversion, or if any amount would overflow.
func BuildCheckoutPlan(buyerID string, lines []CheckoutLine) (*Plan, error) {
	if len(lines) == 0 {
		return nil, apperror.Validation("Your cart is empty")
	}

	currency := lines[0].Currency
	byVendor := map[string][]CheckoutLine{}
	var vendorOrder []string // preserves first-seen vendor order for deterministic output

	for _, line := range lines {
		if line.Currency != currency {
			return nil, apperror.Validation("All items in an order must use the same currency")
		}
		if line.PriceAmount <= 0 || line.Quantity <= 0 {
			return nil, apperror.Validation("Every item needs a positive price and quantity")
		}
		if _, seen := byVendor[line.VendorID]; !seen {
			vendorOrder = append(vendorOrder, line.VendorID)
		}
		byVendor[line.VendorID] = append(byVendor[line.VendorID], line)
	}
	sort.Strings(vendorOrder) // deterministic regardless of cart iteration order

	plan := &Plan{Order: Order{BuyerID: buyerID, Status: StatusPendingPayment, Currency: currency, CheckoutState: CheckoutPreparing}}

	for vendorIdx, vendorID := range vendorOrder {
		vendorLines := byVendor[vendorID]
		var vendorSubtotal int64

		for _, line := range vendorLines {
			subtotal, ok := MulAmount(line.PriceAmount, line.Quantity)
			if !ok {
				return nil, apperror.Validation("Order amount is too large")
			}
			if vendorSubtotal, ok = AddAmount(vendorSubtotal, subtotal); !ok {
				return nil, apperror.Validation("Order amount is too large")
			}
			plan.Items = append(plan.Items, OrderItem{
				VendorOrderID:  strconv.Itoa(vendorIdx),
				ProductID:      line.ProductID,
				ProductName:    line.ProductName,
				VariantID:      line.VariantID,
				VariantSKU:     line.VariantSKU,
				VariantLabel:   line.VariantLabel,
				PriceAmount:    line.PriceAmount,
				Quantity:       line.Quantity,
				SubtotalAmount: subtotal,
			})
		}

		plan.VendorOrders = append(plan.VendorOrders, VendorOrder{
			VendorID:       vendorID,
			Status:         StatusPendingPayment,
			SubtotalAmount: vendorSubtotal,
			Currency:       currency,
		})
		var ok bool
		if plan.Order.SubtotalAmount, ok = AddAmount(plan.Order.SubtotalAmount, vendorSubtotal); !ok {
			return nil, apperror.Validation("Order amount is too large")
		}
	}
	plan.Order.TotalAmount = plan.Order.SubtotalAmount

	return plan, nil
}

// VendorIDs lists the plan's vendors in their deterministic order.
func (p *Plan) VendorIDs() []string {
	ids := make([]string, 0, len(p.VendorOrders))
	for _, vo := range p.VendorOrders {
		ids = append(ids, vo.VendorID)
	}
	return ids
}

// ApplyShipping snapshots one quote per vendor order and adds the fees to
// the order total. Every vendor must have a quote in the order's currency:
// a missing quote is never treated as free shipping.
func (p *Plan) ApplyShipping(quotes map[string]ShippingQuote) error {
	var shipping int64
	for i := range p.VendorOrders {
		vo := &p.VendorOrders[i]
		quote, ok := quotes[vo.VendorID]
		if !ok {
			return apperror.Internal(errMissingQuote)
		}
		if quote.FeeAmount < 0 || quote.Currency != p.Order.Currency {
			return ShippingUnavailable("Shipping cannot be quoted in this order's currency")
		}
		q := quote
		vo.Shipping = &q
		vo.ShippingFeeAmount = quote.FeeAmount
		if shipping, ok = AddAmount(shipping, quote.FeeAmount); !ok {
			return apperror.Validation("Order amount is too large")
		}
	}
	total, ok := AddAmount(p.Order.SubtotalAmount, shipping)
	if !ok {
		return apperror.Validation("Order amount is too large")
	}
	p.Order.ShippingAmount, p.Order.TotalAmount = shipping, total
	return nil
}

// ApplyCommission snapshots the commission of every vendor order under the
// rule current at checkout.
func (p *Plan) ApplyCommission(rule *CommissionRule) error {
	for i := range p.VendorOrders {
		snapshot, err := SnapshotCommission(rule, p.VendorOrders[i].SubtotalAmount)
		if err != nil {
			return err
		}
		p.VendorOrders[i].Commission = snapshot
	}
	return nil
}
