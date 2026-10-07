package repository_test

import (
	"os"
	"testing"
	"time"

	"shopee/backend/pkg/casesla"
	"shopee/backend/services/order/internal/repository"
	"shopee/backend/services/order/internal/usecase"

	"github.com/google/uuid"
)

func TestCaseSLABackfillKeepsOriginalDateAndRequiresActivation(t *testing.T) {
	pool := orderDB(t, "000017")
	buyer := uuid.NewString()
	order, vo, vendor := paidSupportOrder(t, pool, buyer)
	id := uuid.NewString()
	opened := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
	_, err := pool.Exec(t.Context(), `INSERT INTO support_cases(id,order_id,vendor_order_id,vendor_id,buyer_id,category,status,policy_version,financial_hold,created_at,updated_at)
	 VALUES($1,$2,$3,$4,$5,'other','open','test-policy',false,$6,$6)`, id, order, vo, vendor, buyer, opened)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../../migrations/000018_case_sla.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = applyMigration(t.Context(), pool, string(sql)); err != nil {
		t.Fatal(err)
	}
	s := repository.NewCaseSLAStore(pool)
	p, err := s.List(t.Context(), casesla.Filter{Status: "legacy", Limit: 20}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || !p.Items[0].DueAt.Equal(opened.Add(24*time.Hour)) || len(p.Items[0].Notices(time.Now())) != 0 {
		t.Fatal("backfill reset deadline or can spam", p.Items)
	}
}

func TestSupportDeadlineAssignmentAndExtensionAreSharedWithDetail(t *testing.T) {
	pool := orderDB(t)
	buyer := uuid.NewString()
	order, vo, _ := paidSupportOrder(t, pool, buyer)
	uc := supportUseCase(pool)
	c, _, err := uc.CreateSupportCase(t.Context(), buyer, usecase.CreateSupportCaseInput{OrderID: order, VendorOrderID: vo, Category: "other", Message: "Please help"})
	if err != nil {
		t.Fatal(err)
	}
	s := repository.NewCaseSLAStore(pool)
	p, err := s.List(t.Context(), casesla.Filter{Limit: 20}, time.Now())
	if err != nil || len(p.Items) != 1 {
		t.Fatal(err)
	}
	item := p.Items[0]
	admin := uuid.NewString()
	item, err = s.Mutate(t.Context(), item.ID, admin, "assignments", "assign-sla-test", casesla.Mutation{ExpectedVersion: item.Version, Reason: "On call", AssigneeID: admin}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.Mutate(t.Context(), item.ID, admin, "extensions", "extend-sla-test", casesla.Mutation{ExpectedVersion: item.Version, Reason: "Waiting for investigation", NewDueAt: item.DueAt.Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	detail, err := repository.NewSupportCaseRepository(pool).FindByID(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.AssigneeID == nil || *detail.AssigneeID != admin || detail.ActionDueAt == nil || !detail.ActionDueAt.Equal(item.DueAt) || !detail.DueAt.Equal(item.DueAt) {
		t.Fatal("support and work item disagree")
	}
	version := item.DeadlineVersion
	// An ordinary save/message must not renew the deadline or its receipts.
	if err = repository.NewSupportCaseRepository(pool).Save(t.Context(), detail); err != nil {
		t.Fatal(err)
	}
	p, err = s.List(t.Context(), casesla.Filter{Limit: 20}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if p.Items[0].DeadlineVersion != version || !p.Items[0].DueAt.Equal(item.DueAt) {
		t.Fatal("ordinary save reset SLA")
	}
}
