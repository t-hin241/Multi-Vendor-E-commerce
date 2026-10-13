package domain_test

import (
	"shopee/backend/services/notification/internal/domain"
	"testing"
	"time"
)

func TestStockAlertSourceRecipientsAndTemplate(t *testing.T) {
	req := domain.VendorActionRequest{Source: "inventory", EventID: "fake-episode-1", VendorID: "11111111-1111-4111-8111-111111111111", ActionKind: "low_stock", ReferenceID: "11111111-1111-4111-8111-111111111112"}
	a, err := domain.NewVendorAction(req, time.Now())
	if err != nil || a.Purpose != domain.PurposeInventory {
		t.Fatalf("action %+v %v", a, err)
	}
	req.Source = "order"
	if _, err = domain.NewVendorAction(req, time.Now()); err == nil {
		t.Fatal("wrong producer allowed")
	}
	req.Source = "inventory"
	req.ActionKind = "new_order"
	if _, err = domain.NewVendorAction(req, time.Now()); err == nil {
		t.Fatal("inventory may not report orders")
	}
	recipients := domain.SelectRecipients(domain.PurposeInventory, []domain.Candidate{{UserID: "owner", Role: "owner"}, {UserID: "staff", Role: "staff"}, {UserID: "other", Role: "staff"}}, map[string][]string{"staff": {domain.PurposeInventory}})
	if len(recipients) != 2 {
		t.Fatalf("recipients %v", recipients)
	}
	n, err := domain.NewNotification(domain.Request{EventID: a.EventID, Source: a.Source, UserID: "11111111-1111-4111-8111-111111111113", Type: a.Notice(), ReferenceID: a.ReferenceID}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	inbox, ok, err := domain.NewInboxItem(n)
	if err != nil || !ok || inbox.Link != "/vendor/inventory" {
		t.Fatalf("inbox %+v %v", inbox, err)
	}
}
