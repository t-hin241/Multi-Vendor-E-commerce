package domain

import (
	"slices"
	"testing"
	"time"
)

func TestPayoutApprovalPolicyAssess(t *testing.T) {
	p := PayoutApprovalPolicy{Currency: "VND", BatchLimit: 1000, ItemLimit: 600}
	items := func(amounts ...int64) []*PayoutItem {
		out := []*PayoutItem{}
		for _, a := range amounts {
			out = append(out, &PayoutItem{Amount: a})
		}
		return out
	}
	cases := []struct {
		name     string
		currency string
		items    []*PayoutItem
		fresh    int
		required int
		reasons  []string
	}{
		{"under the limits", "VND", items(400, 500), 0, 1, []string{}},
		{"at the limits", "VND", items(600, 400), 0, 1, []string{}},
		{"batch over", "VND", items(500, 501), 0, 2, []string{PayoutRiskBatchOverLimit}},
		{"item over", "VND", items(601), 0, 2, []string{PayoutRiskItemOverLimit}},
		{"new destination", "VND", items(10), 1, 2, []string{PayoutRiskNewDestination}},
		{"other currency", "USD", items(1), 0, 2, []string{PayoutRiskForeignCurrency}},
	}
	for _, c := range cases {
		required, reasons := p.Assess(c.currency, c.items, c.fresh)
		if required != c.required || !slices.Equal(reasons, c.reasons) {
			t.Errorf("%s: %d %v", c.name, required, reasons)
		}
	}
	if required, _ := (PayoutApprovalPolicy{Currency: "VND"}).Assess("VND", items(1<<40), 0); required != 1 {
		t.Error("a zero limit is no limit")
	}
}

func TestApprovePayoutBatchKeepsApproversDistinct(t *testing.T) {
	now := time.Now()
	b := &PayoutBatch{CreatedBy: "maker", Approval: PayoutBatchApproval{State: PayoutAwaitingApproval, RequiredApprovals: 1}}
	if _, err := ApprovePayoutBatch(b, "maker", PayoutAwaitingApproval, now); err != ErrPayoutSelfApproval {
		t.Fatalf("self approval: %v", err)
	}
	if final, err := ApprovePayoutBatch(b, "checker", PayoutAwaitingApproval, now); err != nil || !final || b.Approval.State != PayoutApproved {
		t.Fatalf("one approval suffices: %v %v", final, err)
	}
	if _, err := ApprovePayoutBatch(b, "other", PayoutAwaitingApproval, now); err != ErrPayoutApprovalClosed {
		t.Fatalf("approved is closed: %v", err)
	}
	if !b.Approval.AllowsTransfers() {
		t.Fatal("an approved batch allows transfers")
	}
	if err := ClosePayoutBatch(b, "checker", "x", false, now); err != ErrPayoutApprovalClosed {
		t.Fatalf("an approved batch cannot be rejected: %v", err)
	}
}
