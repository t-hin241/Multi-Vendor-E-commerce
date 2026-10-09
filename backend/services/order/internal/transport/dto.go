package transport

import (
	"encoding/json"
	"sort"
	"time"

	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

type orderItemResponse struct {
	ID             string  `json:"id"`
	VendorOrderID  string  `json:"vendor_order_id"`
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
		ID: i.ID, VendorOrderID: i.VendorOrderID, ProductID: i.ProductID, ProductName: i.ProductName,
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
	ID                 string                 `json:"id"`
	Status             string                 `json:"status"`
	CheckoutState      string                 `json:"checkout_state"`
	Version            int64                  `json:"version"`
	SubtotalAmount     int64                  `json:"subtotal_amount"`
	ShippingAmount     int64                  `json:"shipping_amount"`
	TotalAmount        int64                  `json:"total_amount"`
	RefundedAmount     int64                  `json:"refunded_amount"`
	Currency           string                 `json:"currency"`
	CancellationReason *string                `json:"cancellation_reason,omitempty"`
	RecipientName      string                 `json:"recipient_name"`
	Phone              string                 `json:"phone"`
	Province           string                 `json:"province"`
	District           string                 `json:"district"`
	Ward               string                 `json:"ward"`
	StreetAddress      string                 `json:"street_address"`
	PaidAt             *time.Time             `json:"paid_at,omitempty"`
	Items              []orderItemResponse    `json:"items,omitempty"`
	VendorOrders       []vendorOrderResponse  `json:"vendor_orders,omitempty"`
	Refunds            []refundResponse       `json:"refunds,omitempty"`
	Returns            []returnResponse       `json:"returns,omitempty"`
	Payments           []orderPaymentResponse `json:"payments,omitempty"`
	Effects            []effectResponse       `json:"effects,omitempty"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

func toOrderResponse(o *domain.Order) orderResponse {
	return orderResponse{
		ID: o.ID, Status: string(o.Status), CheckoutState: string(o.CheckoutState), Version: o.Version,
		SubtotalAmount: o.SubtotalAmount, ShippingAmount: o.ShippingAmount, TotalAmount: o.TotalAmount, RefundedAmount: o.RefundedAmount,
		Currency: o.Currency, CancellationReason: o.CancellationReason,
		RecipientName: o.RecipientName, Phone: o.Phone, Province: o.Province,
		District: o.District, Ward: o.Ward, StreetAddress: o.StreetAddress,
		PaidAt: o.PaidAt, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
}

// toOrderDetailResponse serves the order-detail views. Commission and the
// internal capture/effect records are only included for admin.
func toOrderDetailResponse(d *usecase.OrderDetail, admin bool) orderResponse {
	resp := toOrderResponse(d.Order)
	resp.Items = toOrderItemResponseList(d.Items)
	resp.VendorOrders = make([]vendorOrderResponse, 0, len(d.VendorOrders))
	for _, vo := range d.VendorOrders {
		v := toVendorOrderResponse(vo, nil)
		if !admin {
			v.Commission, v.CommissionRateBps, v.CommissionAmount, v.NetAmount = nil, nil, nil, nil
		}
		resp.VendorOrders = append(resp.VendorOrders, v)
	}
	resp.Refunds = make([]refundResponse, 0, len(d.Refunds))
	for _, f := range d.Refunds {
		resp.Refunds = append(resp.Refunds, toRefundResponse(f))
	}
	resp.Returns = toReturnResponses(d.Returns)
	if admin {
		resp.Payments = make([]orderPaymentResponse, 0, len(d.Payments))
		for _, p := range d.Payments {
			resp.Payments = append(resp.Payments, toOrderPaymentResponse(p))
		}
		resp.Effects = make([]effectResponse, 0, len(d.Effects))
		for _, e := range d.Effects {
			resp.Effects = append(resp.Effects, toEffectResponse(e))
		}
	}
	return resp
}

func toOrderResponseList(orders []*domain.Order) []orderResponse {
	out := make([]orderResponse, 0, len(orders))
	for _, o := range orders {
		out = append(out, toOrderResponse(o))
	}
	return out
}

type shippingSnapshotResponse struct {
	FeeAmount          int64      `json:"fee_amount"`
	FeeRuleID          string     `json:"fee_rule_id"`
	FeeRuleVersion     int        `json:"fee_rule_version"`
	PackageWeightGrams int64      `json:"package_weight_grams"`
	QuotedAt           *time.Time `json:"quoted_at,omitempty"`
}

type commissionSnapshotResponse struct {
	RuleID      *string `json:"rule_id,omitempty"`
	RuleVersion *int64  `json:"rule_version,omitempty"`
	RateBps     int     `json:"rate_bps"`
	BaseAmount  int64   `json:"base_amount"`
	Amount      int64   `json:"amount"`
	NetAmount   int64   `json:"net_amount"`
	Rounding    string  `json:"rounding,omitempty"`
	Source      string  `json:"source,omitempty"`
}

type vendorOrderResponse struct {
	ID                string                      `json:"id"`
	OrderID           string                      `json:"order_id"`
	VendorID          string                      `json:"vendor_id"`
	Status            string                      `json:"status"`
	SubtotalAmount    int64                       `json:"subtotal_amount"`
	ShippingFeeAmount int64                       `json:"shipping_fee_amount"`
	RefundedAmount    int64                       `json:"refunded_amount"`
	Currency          string                      `json:"currency"`
	Shipping          *shippingSnapshotResponse   `json:"shipping,omitempty"`
	Commission        *commissionSnapshotResponse `json:"commission,omitempty"`
	CommissionRateBps *int                        `json:"commission_rate_bps,omitempty"`
	CommissionAmount  *int64                      `json:"commission_amount,omitempty"`
	NetAmount         *int64                      `json:"net_amount,omitempty"`
	Fulfillable       bool                        `json:"fulfillable"`
	CompletedAt       *time.Time                  `json:"completed_at,omitempty"`
	Items             []orderItemResponse         `json:"items,omitempty"`
	CreatedAt         time.Time                   `json:"created_at"`
}

func toVendorOrderResponse(vo *domain.VendorOrder, items []*domain.OrderItem) vendorOrderResponse {
	resp := vendorOrderResponse{
		ID: vo.ID, OrderID: vo.OrderID, VendorID: vo.VendorID, Status: string(vo.Status),
		SubtotalAmount: vo.SubtotalAmount, ShippingFeeAmount: vo.ShippingFeeAmount, RefundedAmount: vo.RefundedAmount,
		Currency: vo.Currency, CreatedAt: vo.CreatedAt, CompletedAt: vo.CompletedAt, Fulfillable: vo.Fulfillable(),
		CommissionRateBps: vo.CommissionRateBps, CommissionAmount: vo.CommissionAmount, NetAmount: vo.NetAmount,
		Items: toOrderItemResponseList(items),
	}
	if vo.Shipping != nil {
		s := &shippingSnapshotResponse{FeeAmount: vo.Shipping.FeeAmount, FeeRuleID: vo.Shipping.FeeRuleID,
			FeeRuleVersion: vo.Shipping.FeeRuleVersion, PackageWeightGrams: vo.Shipping.PackageWeightGrams}
		if !vo.Shipping.QuotedAt.IsZero() {
			s.QuotedAt = &vo.Shipping.QuotedAt
		}
		resp.Shipping = s
	}
	if c := vo.Commission; c != nil {
		resp.Commission = &commissionSnapshotResponse{RuleID: c.RuleID, RuleVersion: c.RuleVersion, RateBps: c.RateBps, BaseAmount: c.BaseAmount,
			Amount: c.Amount, NetAmount: c.NetAmount, Rounding: c.Rounding, Source: c.Source}
	}
	return resp
}

func toVendorOrderResponseList(vendorOrders []*domain.VendorOrder, itemsByVendorOrder map[string][]*domain.OrderItem) []vendorOrderResponse {
	out := make([]vendorOrderResponse, 0, len(vendorOrders))
	for _, vo := range vendorOrders {
		out = append(out, toVendorOrderResponse(vo, itemsByVendorOrder[vo.ID]))
	}
	return out
}

// checkoutRequest: CartVersion and ExpectedTotalAmount are what the buyer
// reviewed; a different cart or total is refused instead of bought unseen.
// The Idempotency-Key header identifies the attempt.
type checkoutRequest struct {
	AddressID           string `json:"address_id" binding:"required,uuid"`
	CartVersion         *int64 `json:"cart_version" binding:"omitempty,min=1"`
	ExpectedTotalAmount *int64 `json:"expected_total_amount" binding:"omitempty,min=1"`
	// AcceptedPolicyVersions: kind → version the buyer was shown (AF-02).
	AcceptedPolicyVersions map[string]int64 `json:"accepted_policy_versions" binding:"omitempty,max=10"`
}

type previewRequest struct {
	AddressID string `json:"address_id" binding:"required,uuid"`
}

type previewVendorResponse struct {
	VendorID          string `json:"vendor_id"`
	SubtotalAmount    int64  `json:"subtotal_amount"`
	ShippingFeeAmount *int64 `json:"shipping_fee_amount"`
	ShippingError     string `json:"shipping_error,omitempty"`
	ItemCount         int64  `json:"item_count"`
}

type previewResponse struct {
	CartVersion    int64                   `json:"cart_version"`
	Currency       string                  `json:"currency"`
	SubtotalAmount int64                   `json:"subtotal_amount"`
	ShippingAmount *int64                  `json:"shipping_amount"`
	TotalAmount    *int64                  `json:"total_amount"`
	Ready          bool                    `json:"ready"`
	Vendors        []previewVendorResponse `json:"vendors"`
	// PolicyVersions (kind → version) and Policies are what a checkout now
	// is placed under; send policy_versions back as accepted_policy_versions.
	PolicyVersions map[string]int64        `json:"policy_versions,omitempty"`
	Policies       *policySnapshotResponse `json:"policies,omitempty"`
}

func toPreviewResponse(p *usecase.CheckoutPreview) previewResponse {
	resp := previewResponse{CartVersion: p.CartVersion, Currency: p.Currency, SubtotalAmount: p.SubtotalAmount,
		ShippingAmount: p.ShippingAmount, TotalAmount: p.TotalAmount, Ready: p.Ready, Vendors: make([]previewVendorResponse, 0, len(p.Vendors))}
	for _, v := range p.Vendors {
		resp.Vendors = append(resp.Vendors, previewVendorResponse{VendorID: v.VendorID, SubtotalAmount: v.SubtotalAmount,
			ShippingFeeAmount: v.ShippingFeeAmount, ShippingError: v.ShippingError, ItemCount: v.ItemCount})
	}
	if p.Policies != nil {
		resp.PolicyVersions = p.Policies.VersionsByKind()
		resp.Policies = toPolicySnapshotResponse(p.Policies)
	}
	return resp
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

// adminTransitionRequest: only cancellation remains. A paid order is
// refunded through POST /api/orders/admin/:id/refunds, never by status.
type adminTransitionRequest struct {
	Status string `json:"status" binding:"required"`
	Reason string `json:"reason" binding:"required,max=500"`
}

type markPaymentFailedRequest struct {
	Reason string `json:"reason" binding:"required,max=500"`
}

// markPaidRequest is Payment's capture report. All fields are required
// from current Payment builds; an empty body is accepted from older builds
// during cutover (see deploy/order-runbook.md).
type markPaidRequest struct {
	PaymentID string `json:"payment_id" binding:"omitempty,uuid"`
	Amount    int64  `json:"amount" binding:"omitempty,min=1"`
	Currency  string `json:"currency" binding:"omitempty,len=3"`
}

type refundEventRequest struct {
	RefundID        string `json:"order_refund_id" binding:"required,uuid"`
	PaymentRefundID string `json:"payment_refund_id" binding:"required"`
	Status          string `json:"status" binding:"required,oneof=succeeded failed"`
	Amount          int64  `json:"amount" binding:"required,min=1"`
	Currency        string `json:"currency" binding:"required,len=3"`
	FailureReason   string `json:"failure_reason" binding:"max=500"`
}

type internalOrderResponse struct {
	InventoryStatus      string     `json:"inventory_status,omitempty"`
	ReservationExpiresAt *time.Time `json:"reservation_expires_at,omitempty"`
	ID                   string     `json:"id"`
	BuyerID              string     `json:"buyer_id"`
	Status               string     `json:"status"`
	CheckoutState        string     `json:"checkout_state"`
	TotalAmount          int64      `json:"total_amount"`
	Currency             string     `json:"currency"`
}

func toInternalOrderResponse(o *domain.Order) internalOrderResponse {
	return internalOrderResponse{ID: o.ID, BuyerID: o.BuyerID, Status: string(o.Status), CheckoutState: string(o.CheckoutState),
		TotalAmount: o.TotalAmount, Currency: o.Currency}
}

type setCommissionRuleRequest struct {
	RateBps int    `json:"rate_bps" binding:"min=0,max=10000"`
	Reason  string `json:"reason" binding:"required,max=500"`
}

// adminReasonRequest is the body of a retry/replay: the reason is required
// and goes to the audit.
type adminReasonRequest struct {
	Reason string `json:"reason" binding:"required,max=500"`
}

type commissionRuleResponse struct {
	ID        string    `json:"id"`
	Version   int64     `json:"version"`
	RateBps   int       `json:"rate_bps"`
	CreatedBy *string   `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func toCommissionRuleResponse(r *domain.CommissionRule) commissionRuleResponse {
	return commissionRuleResponse{ID: r.ID, Version: r.Version, RateBps: r.RateBps, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}
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
	TotalRefunded   int64                `json:"total_refunded"`
	TopProducts     []topProductResponse `json:"top_products"`
}

func toVendorSummaryResponse(s *domain.VendorSummary, topProducts []*domain.TopProduct) vendorSummaryResponse {
	resp := vendorSummaryResponse{
		TotalOrders: s.TotalOrders, TotalRevenue: s.TotalRevenue, TotalCommission: s.TotalCommission, TotalNet: s.TotalNet,
		TotalRefunded: s.TotalRefunded, TopProducts: make([]topProductResponse, 0, len(topProducts)),
	}
	for _, p := range topProducts {
		resp.TopProducts = append(resp.TopProducts, topProductResponse{
			ProductID: p.ProductID, ProductName: p.ProductName, QuantitySold: p.QuantitySold, RevenueAmount: p.RevenueAmount,
		})
	}
	return resp
}

type refundResponse struct {
	ID              string     `json:"id"`
	OrderID         string     `json:"order_id"`
	VendorOrderID   *string    `json:"vendor_order_id,omitempty"`
	ReturnRequestID *string    `json:"return_request_id,omitempty"`
	PaymentID       *string    `json:"payment_id,omitempty"`
	ReasonCode      string     `json:"reason_code"`
	Amount          int64      `json:"amount"`
	Currency        string     `json:"currency"`
	Reason          string     `json:"reason"`
	Status          string     `json:"status"`
	FailureReason   *string    `json:"failure_reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}

func toRefundResponse(f *domain.Refund) refundResponse {
	return refundResponse{ID: f.ID, OrderID: f.OrderID, VendorOrderID: f.VendorOrderID, ReturnRequestID: f.ReturnRequestID, PaymentID: f.PaymentID,
		ReasonCode: f.ReasonCode, Amount: f.Amount, Currency: f.Currency, Reason: f.Reason, Status: string(f.Status),
		FailureReason: f.FailureReason, CreatedAt: f.CreatedAt, ResolvedAt: f.ResolvedAt}
}

type createRefundRequest struct {
	VendorOrderID string `json:"vendor_order_id" binding:"omitempty,uuid"`
	PaymentID     string `json:"payment_id" binding:"omitempty,uuid"`
	ReasonCode    string `json:"reason_code" binding:"required,oneof=dispute late_payment duplicate_payment"`
	Amount        int64  `json:"amount" binding:"required,min=1"`
	Reason        string `json:"reason" binding:"required,max=500"`
	// IdempotencyKey may also come as the Idempotency-Key header.
	IdempotencyKey string `json:"idempotency_key" binding:"omitempty,max=100"`
}

type orderPaymentResponse struct {
	PaymentID       string    `json:"payment_id"`
	OrderID         string    `json:"order_id"`
	Amount          int64     `json:"amount"`
	Currency        string    `json:"currency"`
	Outcome         string    `json:"outcome"`
	RejectionReason *string   `json:"rejection_reason,omitempty"`
	ReceivedAt      time.Time `json:"received_at"`
}

func toOrderPaymentResponse(p *domain.OrderPayment) orderPaymentResponse {
	return orderPaymentResponse{PaymentID: p.PaymentID, OrderID: p.OrderID, Amount: p.Amount, Currency: p.Currency,
		Outcome: p.Outcome, RejectionReason: p.RejectionReason, ReceivedAt: p.ReceivedAt}
}

type effectResponse struct {
	ID            string          `json:"id"`
	OrderID       string          `json:"order_id"`
	Kind          string          `json:"kind"`
	Target        string          `json:"target,omitempty"`
	Status        string          `json:"status"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt time.Time       `json:"next_attempt_at"`
	LastError     *string         `json:"last_error,omitempty"`
	Payload       json.RawMessage `json:"-"`
	CreatedAt     time.Time       `json:"created_at"`
}

func toEffectResponse(e *domain.Effect) effectResponse {
	return effectResponse{ID: e.ID, OrderID: e.OrderID, Kind: string(e.Kind), Target: e.Target, Status: e.Status, Attempts: e.Attempts,
		NextAttemptAt: e.NextAttemptAt, LastError: e.LastError, CreatedAt: e.CreatedAt}
}

type returnResponse struct {
	ActionDueAt      *time.Time `json:"action_due_at"`
	WaitingOn        string     `json:"waiting_on"`
	ID               string     `json:"id"`
	OrderID          string     `json:"order_id"`
	OrderItemID      string     `json:"order_item_id"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	Quantity         int64      `json:"quantity"`
	RefundAmount     int64      `json:"refund_amount"`
	PolicyVersion    string     `json:"policy_version"`
	ReturnWindowDays *int       `json:"return_window_days,omitempty"`
	Evidence         *string    `json:"evidence,omitempty"`
	VendorNote       *string    `json:"vendor_note,omitempty"`
	DecisionNote     *string    `json:"decision_note,omitempty"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
	ReceivedAt       *time.Time `json:"received_at,omitempty"`
	InspectionNote   *string    `json:"inspection_note,omitempty"`
	Restock          *bool      `json:"restock,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	// AF-05 return shipping (the address itself is in the buyer's
	// shipping instructions only).
	Version              int64                `json:"version"`
	AuthorizationVersion int                  `json:"authorization_version"`
	ReturnCode           string               `json:"return_code,omitempty"`
	ShippingStatus       *string              `json:"shipping_status,omitempty"`
	FeePayer             *string              `json:"fee_payer,omitempty"`
	FeeCap               *int64               `json:"fee_cap,omitempty"`
	DispatchDeadline     *time.Time           `json:"dispatch_deadline,omitempty"`
	DispatchOverdue      bool                 `json:"dispatch_overdue"`
	DispatchCarrier      *string              `json:"dispatch_carrier,omitempty"`
	DispatchTracking     *string              `json:"dispatch_tracking,omitempty"`
	DispatchedAt         *time.Time           `json:"dispatched_at,omitempty"`
	DestinationProvince  *string              `json:"destination_province,omitempty"`
	RestockQuantity      *int64               `json:"restock_quantity,omitempty"`
	InspectionDisputed   bool                 `json:"inspection_disputed"`
	Receipt              *receiptResponseBody `json:"receipt,omitempty"`
}

func toReturnResponse(r *domain.ReturnRequest) returnResponse {
	out := returnResponse{ActionDueAt: r.ActionDueAt, WaitingOn: r.WaitingOn, ID: r.ID, OrderID: r.OrderID, OrderItemID: r.OrderItemID, Reason: r.Reason, Status: string(r.Status),
		Quantity: r.Quantity, RefundAmount: r.RefundAmount, PolicyVersion: r.PolicyVersion, ReturnWindowDays: r.ReturnWindowDays,
		Evidence: r.Evidence, VendorNote: r.VendorNote, DecisionNote: r.DecisionNote, DecidedAt: r.DecidedAt,
		ReceivedAt: r.ReceivedAt, InspectionNote: r.InspectionNote, Restock: r.Restock, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Version: r.Version, AuthorizationVersion: r.AuthorizationVersion, ShippingStatus: r.ShippingStatus, FeePayer: r.FeePayer, FeeCap: r.FeeCap,
		DispatchDeadline: r.DispatchDeadline, DispatchOverdue: r.DispatchOverdueAt != nil, DispatchCarrier: r.DispatchCarrier,
		DispatchTracking: r.DispatchTracking, DispatchedAt: r.DispatchedAt, RestockQuantity: r.RestockQuantity, InspectionDisputed: r.InspectionDisputed}
	if r.Authorized() {
		out.ReturnCode = domain.ReturnCode(r.ID)
		out.DestinationProvince = &r.Destination.Province
	}
	return out
}

func toReturnResponses(items []*domain.ReturnRequest) []returnResponse {
	out := make([]returnResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toReturnResponse(item))
	}
	return out
}

type returnEventResponse struct {
	Action     string    `json:"action"`
	ActorRole  string    `json:"actor_role"`
	FromStatus *string   `json:"from_status,omitempty"`
	ToStatus   string    `json:"to_status"`
	Note       *string   `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func toReturnEventResponses(events []*domain.ReturnEvent) []returnEventResponse {
	out := make([]returnEventResponse, 0, len(events))
	for _, e := range events {
		out = append(out, returnEventResponse{Action: e.Action, ActorRole: e.ActorRole, FromStatus: e.FromStatus, ToStatus: e.ToStatus, Note: e.Note, CreatedAt: e.CreatedAt})
	}
	return out
}

type policyRefResponse struct {
	Kind        string `json:"kind"`
	PolicyID    string `json:"policy_id"`
	Version     int64  `json:"version"`
	ContentHash string `json:"content_hash"`
}

type policySnapshotResponse struct {
	Source               string              `json:"source"`
	Policies             []policyRefResponse `json:"policies"`
	ReturnsWindowDays    int                 `json:"returns_window_days"`
	ReturnShippingRefund string              `json:"return_shipping_refund"`
	ReturnPolicyVersion  string              `json:"return_policy_version"`
	TakenAt              time.Time           `json:"taken_at"`
}

func toPolicyRef(p domain.PolicyRef) policyRefResponse {
	return policyRefResponse{Kind: p.Kind, PolicyID: p.PolicyID, Version: p.Version, ContentHash: p.ContentHash}
}

func toPolicySnapshotResponse(s *domain.OrderPolicySnapshot) *policySnapshotResponse {
	if s == nil {
		return nil
	}
	out := &policySnapshotResponse{Source: s.Source, Policies: make([]policyRefResponse, 0, len(s.Policies)), ReturnsWindowDays: s.ReturnsWindowDays,
		ReturnShippingRefund: s.ReturnShippingRefund, ReturnPolicyVersion: s.ReturnPolicyVersion, TakenAt: s.TakenAt}
	for _, p := range s.Policies {
		out.Policies = append(out.Policies, toPolicyRef(p))
	}
	return out
}

type vendorPolicySnapshotResponse struct {
	VendorOrderID        string             `json:"vendor_order_id"`
	ShopPolicy           *policyRefResponse `json:"shop_policy,omitempty"`
	ReturnsWindowDays    int                `json:"returns_window_days"`
	ReturnShippingRefund string             `json:"return_shipping_refund"`
	ReturnPolicyVersion  string             `json:"return_policy_version"`
}

type orderPolicyViewResponse struct {
	OrderID      string                         `json:"order_id"`
	Legacy       bool                           `json:"legacy"`
	Order        *policySnapshotResponse        `json:"order,omitempty"`
	VendorOrders []vendorPolicySnapshotResponse `json:"vendor_orders"`
}

func toOrderPolicyViewResponse(v *usecase.OrderPolicyView) orderPolicyViewResponse {
	out := orderPolicyViewResponse{OrderID: v.OrderID, Legacy: v.Legacy, Order: toPolicySnapshotResponse(v.Order),
		VendorOrders: []vendorPolicySnapshotResponse{}}
	for id, s := range v.VendorOrders {
		if s == nil {
			out.VendorOrders = append(out.VendorOrders, vendorPolicySnapshotResponse{VendorOrderID: id})
			continue
		}
		r := vendorPolicySnapshotResponse{VendorOrderID: id, ReturnsWindowDays: s.ReturnsWindowDays, ReturnShippingRefund: s.ReturnShippingRefund,
			ReturnPolicyVersion: s.ReturnPolicyVersion}
		if s.ShopPolicy != nil {
			ref := toPolicyRef(*s.ShopPolicy)
			r.ShopPolicy = &ref
		}
		out.VendorOrders = append(out.VendorOrders, r)
	}
	sort.Slice(out.VendorOrders, func(i, j int) bool { return out.VendorOrders[i].VendorOrderID < out.VendorOrders[j].VendorOrderID })
	return out
}
