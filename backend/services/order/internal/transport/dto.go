package transport

import (
	"time"

	"shopee/backend/services/order/internal/domain"
)

type orderItemResponse struct {
	ProductID      string  `json:"product_id"`
	ProductName    string  `json:"product_name"`
	VariantID      *string `json:"variant_id,omitempty"`
	VariantSKU     *string `json:"variant_sku,omitempty"`
	VariantLabel   *string `json:"variant_label,omitempty"`
	PriceAmount    int64   `json:"price_amount"`
	Quantity       int64   `json:"quantity"`
	SubtotalAmount int64   `json:"subtotal_amount"`
}

func toOrderItemResponse(i *domain.OrderItem) orderItemResponse {
	return orderItemResponse{
		ProductID: i.ProductID, ProductName: i.ProductName,
		VariantID: i.VariantID, VariantSKU: i.VariantSKU, VariantLabel: i.VariantLabel,
		PriceAmount: i.PriceAmount, Quantity: i.Quantity, SubtotalAmount: i.SubtotalAmount,
	}
}

func toOrderItemResponseList(items []*domain.OrderItem) []orderItemResponse {
	out := make([]orderItemResponse, 0, len(items))
	for _, i := range items {
		out = append(out, toOrderItemResponse(i))
	}
	return out
}

type orderResponse struct {
	ID                 string                `json:"id"`
	Status             string                `json:"status"`
	TotalAmount        int64                 `json:"total_amount"`
	Currency           string                `json:"currency"`
	CancellationReason *string               `json:"cancellation_reason,omitempty"`
	RecipientName      string                `json:"recipient_name"`
	Phone              string                `json:"phone"`
	Province           string                `json:"province"`
	District           string                `json:"district"`
	Ward               string                `json:"ward"`
	StreetAddress      string                `json:"street_address"`
	Items              []orderItemResponse   `json:"items,omitempty"`
	VendorOrders       []vendorOrderResponse `json:"vendor_orders,omitempty"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
}

func toOrderResponse(o *domain.Order) orderResponse {
	return orderResponse{
		ID: o.ID, Status: string(o.Status), TotalAmount: o.TotalAmount, Currency: o.Currency,
		CancellationReason: o.CancellationReason,
		RecipientName:      o.RecipientName, Phone: o.Phone, Province: o.Province,
		District: o.District, Ward: o.Ward, StreetAddress: o.StreetAddress,
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
}

// toOrderResponseWithDetails serves the buyer's order-detail view: their
// items, plus each vendor sub-order's own status so a multi-vendor order's
// per-package fulfillment progress is visible even while the buyer-facing
// overall status (the weakest link across vendors) hasn't moved yet.
func toOrderResponseWithDetails(o *domain.Order, items []*domain.OrderItem, vendorOrders []*domain.VendorOrder) orderResponse {
	resp := toOrderResponse(o)
	resp.Items = toOrderItemResponseList(items)
	// The buyer's own view already lists every item at the top level
	// (resp.Items); each vendor sub-order embeds its own items only for the
	// vendor's own list/export view (toVendorOrderResponseList), not here.
	resp.VendorOrders = toVendorOrderResponseList(vendorOrders, nil)
	return resp
}

func toOrderResponseList(orders []*domain.Order) []orderResponse {
	out := make([]orderResponse, 0, len(orders))
	for _, o := range orders {
		out = append(out, toOrderResponse(o))
	}
	return out
}

type vendorOrderResponse struct {
	ID                string              `json:"id"`
	OrderID           string              `json:"order_id"`
	VendorID          string              `json:"vendor_id"`
	Status            string              `json:"status"`
	SubtotalAmount    int64               `json:"subtotal_amount"`
	ShippingFeeAmount int64               `json:"shipping_fee_amount"`
	Currency          string              `json:"currency"`
	CommissionRateBps *int                `json:"commission_rate_bps,omitempty"`
	CommissionAmount  *int64              `json:"commission_amount,omitempty"`
	NetAmount         *int64              `json:"net_amount,omitempty"`
	Items             []orderItemResponse `json:"items,omitempty"`
	CreatedAt         time.Time           `json:"created_at"`
}

func toVendorOrderResponse(vo *domain.VendorOrder, items []*domain.OrderItem) vendorOrderResponse {
	return vendorOrderResponse{
		ID: vo.ID, OrderID: vo.OrderID, VendorID: vo.VendorID, Status: string(vo.Status),
		SubtotalAmount: vo.SubtotalAmount, ShippingFeeAmount: vo.ShippingFeeAmount, Currency: vo.Currency, CreatedAt: vo.CreatedAt,
		CommissionRateBps: vo.CommissionRateBps, CommissionAmount: vo.CommissionAmount, NetAmount: vo.NetAmount,
		Items: toOrderItemResponseList(items),
	}
}

func toVendorOrderResponseList(vendorOrders []*domain.VendorOrder, itemsByVendorOrder map[string][]*domain.OrderItem) []vendorOrderResponse {
	out := make([]vendorOrderResponse, 0, len(vendorOrders))
	for _, vo := range vendorOrders {
		out = append(out, toVendorOrderResponse(vo, itemsByVendorOrder[vo.ID]))
	}
	return out
}

type checkoutRequest struct {
	AddressID string `json:"address_id" binding:"required"`
}

type addressRequest struct {
	RecipientName string `json:"recipient_name" binding:"required"`
	Phone         string `json:"phone" binding:"required"`
	Province      string `json:"province" binding:"required"`
	District      string `json:"district" binding:"required"`
	Ward          string `json:"ward" binding:"required"`
	StreetAddress string `json:"street_address" binding:"required"`
}

type buyerAddressResponse struct {
	ID            string `json:"id"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Province      string `json:"province"`
	District      string `json:"district"`
	Ward          string `json:"ward"`
	StreetAddress string `json:"street_address"`
	IsDefault     bool   `json:"is_default"`
}

func toBuyerAddressResponse(a *domain.BuyerAddress) buyerAddressResponse {
	return buyerAddressResponse{
		ID: a.ID, RecipientName: a.RecipientName, Phone: a.Phone,
		Province: a.Province, District: a.District, Ward: a.Ward, StreetAddress: a.StreetAddress,
		IsDefault: a.IsDefault,
	}
}

func toBuyerAddressResponseList(addresses []*domain.BuyerAddress) []buyerAddressResponse {
	out := make([]buyerAddressResponse, 0, len(addresses))
	for _, a := range addresses {
		out = append(out, toBuyerAddressResponse(a))
	}
	return out
}

type updateVendorOrderStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=processing shipped completed"`
}

type adminTransitionRequest struct {
	Status string `json:"status" binding:"required,oneof=cancelled refunded"`
	Reason string `json:"reason" binding:"required"`
}

type markPaymentFailedRequest struct {
	Reason string `json:"reason" binding:"required"`
}

type internalOrderResponse struct {
	ID          string `json:"id"`
	BuyerID     string `json:"buyer_id"`
	Status      string `json:"status"`
	TotalAmount int64  `json:"total_amount"`
	Currency    string `json:"currency"`
}

func toInternalOrderResponse(o *domain.Order) internalOrderResponse {
	return internalOrderResponse{ID: o.ID, BuyerID: o.BuyerID, Status: string(o.Status), TotalAmount: o.TotalAmount, Currency: o.Currency}
}

type setCommissionRuleRequest struct {
	RateBps int `json:"rate_bps" binding:"required,min=0,max=10000"`
}

type commissionRuleResponse struct {
	ID        string    `json:"id"`
	RateBps   int       `json:"rate_bps"`
	CreatedBy *string   `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func toCommissionRuleResponse(r *domain.CommissionRule) commissionRuleResponse {
	return commissionRuleResponse{ID: r.ID, RateBps: r.RateBps, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}
}

func toCommissionRuleResponseList(rules []*domain.CommissionRule) []commissionRuleResponse {
	out := make([]commissionRuleResponse, 0, len(rules))
	for _, r := range rules {
		out = append(out, toCommissionRuleResponse(r))
	}
	return out
}

type topProductResponse struct {
	ProductID     string `json:"product_id"`
	ProductName   string `json:"product_name"`
	QuantitySold  int64  `json:"quantity_sold"`
	RevenueAmount int64  `json:"revenue_amount"`
}

type vendorSummaryResponse struct {
	TotalOrders     int64                `json:"total_orders"`
	TotalRevenue    int64                `json:"total_revenue"`
	TotalCommission int64                `json:"total_commission"`
	TotalNet        int64                `json:"total_net"`
	TopProducts     []topProductResponse `json:"top_products"`
}

func toVendorSummaryResponse(s *domain.VendorSummary, topProducts []*domain.TopProduct) vendorSummaryResponse {
	resp := vendorSummaryResponse{
		TotalOrders: s.TotalOrders, TotalRevenue: s.TotalRevenue,
		TotalCommission: s.TotalCommission, TotalNet: s.TotalNet,
		TopProducts: make([]topProductResponse, 0, len(topProducts)),
	}
	for _, p := range topProducts {
		resp.TopProducts = append(resp.TopProducts, topProductResponse{
			ProductID: p.ProductID, ProductName: p.ProductName, QuantitySold: p.QuantitySold, RevenueAmount: p.RevenueAmount,
		})
	}
	return resp
}
