package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

type checkoutFixture struct {
	uc              *usecase.OrderUseCase
	orders          *fakeOrderRepository
	vendorOrders    *fakeVendorOrderRepository
	buyerAddresses  *fakeBuyerAddressRepository
	commissionRules *fakeCommissionRuleRepository
	consumptions    *fakeCartConsumptionRepository
	checkoutOps     *fakeCheckoutOpRepository
	payments        *fakePaymentRecordRepository
	effects         *fakeEffectRepository
	refunds         *fakeRefundRepository
	returns         *fakeReturnRepository
	support         *fakeSupportRepository
	policies        *fakePolicyVersions
	store           *fakeAttachmentStore
	cart            *fakeCartGateway
	catalog         *fakeCatalogGateway
	vendors         *fakeVendorGateway
	inventory       *fakeInventoryGateway
	shipments       *fakeShipmentGateway
	notifications   *fakeNotificationGateway
	payment         *fakePaymentGateway
	identity        fakeIdentityGateway
	audit           *fakeAudit
	addressID       string
	now             time.Time
}

func newCheckoutFixture() *checkoutFixture {
	f := &checkoutFixture{
		vendorOrders: newFakeVendorOrderRepository(), buyerAddresses: newFakeBuyerAddressRepository(),
		commissionRules: newFakeCommissionRuleRepository(1000), consumptions: newFakeCartConsumptionRepository(),
		checkoutOps: newFakeCheckoutOpRepository(), payments: newFakePaymentRecordRepository(), effects: &fakeEffectRepository{},
		refunds: newFakeRefundRepository(), returns: newFakeReturnRepository(), support: newFakeSupportRepository(),
		store: newFakeAttachmentStore(), policies: newFakePolicyVersions(), cart: newFakeCartGateway(),
		catalog: newFakeCatalogGateway(), vendors: newFakeVendorGateway(), inventory: newFakeInventoryGateway(),
		shipments: newFakeShipmentGateway(20000), notifications: &fakeNotificationGateway{}, payment: &fakePaymentGateway{},
		identity: fakeIdentityGateway{denied: map[string]bool{}}, audit: &fakeAudit{}, now: time.Now(),
	}
	f.orders = newFakeOrderRepository(f.vendorOrders)
	f.orders.consumptions, f.orders.checkoutOps, f.orders.policies = f.consumptions, f.checkoutOps, f.policies
	f.returns.vendorsOf = func(itemID string) (string, string) {
		for _, items := range f.vendorOrders.items {
			for _, item := range items {
				if item.ID == itemID {
					vo, _ := f.vendorOrders.FindByID(context.Background(), item.VendorOrderID)
					return vo.ID, vo.VendorID
				}
			}
		}
		return "", ""
	}
	f.uc = usecase.NewOrderUseCase(usecase.Deps{
		Orders: f.orders, VendorOrders: f.vendorOrders, BuyerAddresses: f.buyerAddresses, CommissionRules: f.commissionRules,
		CartConsumption: f.consumptions, CheckoutOps: f.checkoutOps, Payments: f.payments, Effects: f.effects,
		Refunds: f.refunds, Returns: f.returns, Support: f.support, Attachments: f.store, Policies: f.policies, Cart: f.cart, Catalog: f.catalog, Vendors: f.vendors, Inventory: f.inventory,
		Shipments: f.shipments, Notifications: f.notifications, Payment: f.payment, Identity: f.identity, Audit: f.audit, Tx: inlineTx{},
		ReturnPolicy: domain.ReturnPolicy{Version: "window-7d", WindowDays: 7}, Log: zerolog.Nop(),
		SupportConfig: usecase.SupportConfig{Enabled: true},
		Now:           func() time.Time { return f.now },
	})
	address := &domain.BuyerAddress{BuyerID: "buyer-1", RecipientName: "Nguyen A", Phone: "0900000000", Province: "HN", District: "D1", Ward: "W1", StreetAddress: "123 St"}
	_ = f.buyerAddresses.Create(context.Background(), address)
	f.addressID = address.ID
	return f
}

// product registers a visible product of vendor priced at price VND.
func (f *checkoutFixture) product(id, vendor string, price int64) *adapter.ProductInfo {
	weight := int64(500)
	p := &adapter.ProductInfo{ID: id, VendorID: vendor, Name: "Product " + id, PriceAmount: price, Currency: "VND", IsVisible: true, Version: 1, PackageWeightGrams: &weight}
	f.catalog.products[id] = p
	return p
}

func (f *checkoutFixture) cartOf(lines ...adapter.CartLine) {
	f.cart.byBuyer["buyer-1"] = lines
}

func (f *checkoutFixture) checkout(t *testing.T, key string) *domain.Order {
	t.Helper()
	order, _, err := f.uc.Checkout(t.Context(), "buyer-1", usecase.CheckoutInput{AddressID: f.addressID, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	return order
}

func (f *checkoutFixture) pay(t *testing.T, order *domain.Order, paymentID string) {
	t.Helper()
	current := f.orders.get(order.ID)
	if _, err := f.uc.MarkPaid(t.Context(), order.ID, &domain.PaymentCapture{PaymentID: paymentID, Amount: current.TotalAmount, Currency: current.Currency}); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
}

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

func expectCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	if got := mustAppError(t, err).Code; got != code {
		t.Fatalf("expected %s, got %s (%v)", code, got, err)
	}
}
