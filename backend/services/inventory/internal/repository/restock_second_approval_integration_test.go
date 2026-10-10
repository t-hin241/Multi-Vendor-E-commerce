package repository_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/inventory/internal/domain"
	"shopee/backend/services/inventory/internal/repository"
	"shopee/backend/services/inventory/internal/usecase"
)

type anyAdmin struct{}

func (anyAdmin) RequireRole(_ context.Context, _, role string) error {
	if role != "admin" {
		return apperror.Forbidden("Not admin")
	}
	return nil
}

// PW-027: a large restock moves stock only on a second, different admin's
// approval, once even when both press at the same time; the database
// refuses a request approved twice by one admin.
func TestLargeRestockNeedsTwoAdmins(t *testing.T) {
	pool := inventoryDB(t)
	ctx := t.Context()
	items := repository.NewInventoryItemRepository(pool)
	requests := repository.NewRestockRequestRepository(pool)
	item := stock(t, pool, 5, false)
	req := &domain.RestockRequest{InventoryItemID: item.ID, ProductID: item.ProductID, VendorID: item.VendorID, RequestedQuantity: 500, RequestedBy: uuid.NewString()}
	if err := requests.Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	uc := usecase.NewInventoryUseCase(items, repository.NewReservationRepository(pool), requests, nil, nil,
		usecase.Operations{Transactions: repository.Transactions{Pool: pool}, Identity: anyAdmin{}, Audit: repository.AdminAudit{Pool: pool}, SecondApprovalQuantity: 100})
	first, second := uuid.NewString(), uuid.NewString()
	if _, err := uc.ApproveRestockRequest(ctx, first, req.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := requests.FindByID(ctx, req.ID)
	if err != nil || stored.Status != domain.RestockPending || stored.FirstApprovedBy == nil || *stored.FirstApprovedBy != first {
		t.Fatalf("first approval recorded, still pending: %+v %v", stored, err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, admin := range []string{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = uc.ApproveRestockRequest(ctx, admin, req.ID)
		}()
	}
	wg.Wait()
	// The first admin is refused (self approval) or, once the second
	// admin's approval committed, answered like a replay; never applied twice.
	if results[1] != nil {
		t.Fatalf("the second admin applies it: %v", results)
	}
	current, err := items.FindByProductID(ctx, item.ProductID)
	if err != nil || current.AvailableQuantity != 505 {
		t.Fatalf("stock applied once: %+v %v", current, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE restock_requests SET decided_by = first_approved_by WHERE id = $1`, req.ID); err == nil {
		t.Fatal("one admin cannot be both approvers")
	}
}
