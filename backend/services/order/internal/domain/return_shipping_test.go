package domain

import (
	"strings"
	"testing"
	"time"
)

// AF-05: a receipt accounts for exactly the approved units; a dispatch is
// neither in the future nor before the return existed; the deadline is
// the buyer's (paused), its miss and a disputed inspection the
// marketplace's.
func TestReturnShippingRules(t *testing.T) {
	if err := ValidateReturnReceipt(1, 1, 0, 2); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][4]int64{{3, 0, 0, 2}, {1, 0, 0, 2}, {-1, 3, 0, 2}} {
		if ValidateReturnReceipt(c[0], c[1], c[2], c[3]) == nil {
			t.Errorf("%v accepted", c)
		}
	}
	if (&ReturnReceipt{Sellable: 2}).Disputed() || !(&ReturnReceipt{Sellable: 1, Missing: 1}).Disputed() {
		t.Fatal("damaged or missing goods are disputed")
	}
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	created := now.Add(-48 * time.Hour)
	if _, _, err := ValidateDispatch(" GHN ", "VN0001", now.Add(-time.Hour), now, created); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ValidateDispatch("GHN", "VN0001", now.Add(time.Hour), now, created); err == nil {
		t.Fatal("a dispatch in the future")
	}
	if _, _, err := ValidateDispatch("GHN", "VN0001", created.Add(-2*time.Hour), now, created); err == nil {
		t.Fatal("a dispatch before the return existed")
	}
	if _, _, err := ValidateDispatch("GHN", "x", now, now, created); err == nil {
		t.Fatal("an invalid tracking number")
	}
	if code := ReturnCode("0b9f0c5e-1111-4111-8111-111111111111"); !strings.HasPrefix(code, "RT-0B9F0C5E11") {
		t.Fatalf("return code: %s", code)
	}

	waiting, overdue := ShippingAwaitingDispatch, ShippingAwaitingDispatch
	r := ReturnRequest{ID: "r", Status: ReturnApproved, ShippingStatus: &waiting, CreatedAt: now, UpdatedAt: now}
	if s := r.SLAStage(); s.Stage != "return_dispatch" || s.WaitingOn != "buyer" || !s.Pause {
		t.Fatalf("waiting on the buyer pauses: %+v", s)
	}
	r.ShippingStatus, r.DispatchOverdueAt = &overdue, &now
	if s := r.SLAStage(); s.Stage != "return_dispatch_review" || s.WaitingOn != "admin" {
		t.Fatalf("a missed deadline is the marketplace's: %+v", s)
	}
	received := ShippingReceived
	r = ReturnRequest{ID: "r", Status: ReturnReceived, ShippingStatus: &received, InspectionDisputed: true, CreatedAt: now, UpdatedAt: now}
	if s := r.SLAStage(); s.Stage != "return_inspection_review" {
		t.Fatalf("a disputed inspection waits for an admin: %+v", s)
	}
	r.InspectionDisputed = false
	if s := r.SLAStage(); s.Stage != "" {
		t.Fatalf("a clean receipt has no stage: %+v", s)
	}
}
