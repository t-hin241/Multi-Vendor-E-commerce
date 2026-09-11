package domain

import (
	"sort"
	"strconv"

	"shopee/backend/pkg/apperror"
)

// Plan is an order fully computed from checkout lines but not yet
// persisted: no IDs, no timestamps. The repository assigns those on
// insert. Splitting lines into one VendorOrder per vendor is the checkout
// rule that keeps a multi-vendor cart from becoming a single order that
// spans bounded contexts.
type Plan struct {
	Order        Order
	VendorOrders []VendorOrder
	Items        []OrderItem // VendorOrderID left as the vendor's index into VendorOrders; the repository resolves it to a real id after insert.
}

// BuildCheckoutPlan groups checkout lines by vendor and computes every
// total. It fails if lines mix currencies, since Order doesn't do currency
// conversion.
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
		if _, seen := byVendor[line.VendorID]; !seen {
			vendorOrder = append(vendorOrder, line.VendorID)
		}
		byVendor[line.VendorID] = append(byVendor[line.VendorID], line)
	}
	sort.Strings(vendorOrder) // deterministic regardless of cart iteration order

	plan := &Plan{Order: Order{BuyerID: buyerID, Status: StatusPendingPayment, Currency: currency}}

	for vendorIdx, vendorID := range vendorOrder {
		vendorLines := byVendor[vendorID]
		var vendorSubtotal int64

		for _, line := range vendorLines {
			subtotal := line.Subtotal()
			vendorSubtotal += subtotal
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
		plan.Order.TotalAmount += vendorSubtotal
	}

	return plan, nil
}
