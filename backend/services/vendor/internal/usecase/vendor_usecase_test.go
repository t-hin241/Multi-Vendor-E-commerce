package usecase_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

func newTestVendorUseCase() (*usecase.VendorUseCase, *fakeAuditLogRepository) {
	audit := newFakeAuditLogRepository()
	return usecase.NewVendorUseCase(newFakeVendorRepository(), audit, newFakeNotificationGateway(), newFakeObjectStore(), zerolog.Nop()), audit
}

// newTestVendorUseCaseWithStore is for tests that need to assert against the
// object store itself (upload/delete side effects), not just the usecase.
func newTestVendorUseCaseWithStore() (*usecase.VendorUseCase, *fakeObjectStore) {
	store := newFakeObjectStore()
	return usecase.NewVendorUseCase(newFakeVendorRepository(), newFakeAuditLogRepository(), newFakeNotificationGateway(), store, zerolog.Nop()), store
}

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

func TestApply_CreatesPendingApplication(t *testing.T) {
	uc, _ := newTestVendorUseCase()

	v, err := uc.Apply(t.Context(), "user-1", "Alice's Shop", "Handmade goods")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Status != "pending" {
		t.Errorf("expected status pending, got %q", v.Status)
	}
}

// TestApply_AllowsMultipleShopsFromTheSameUser guards the 1:N vendor<->user
// relationship: a second (or third) application from the same user creates
// an independent shop, not a conflict.
func TestApply_AllowsMultipleShopsFromTheSameUser(t *testing.T) {
	uc, _ := newTestVendorUseCase()
	ctx := t.Context()

	first, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	second, err := uc.Apply(ctx, "user-1", "Alice's Second Shop", "")
	if err != nil {
		t.Fatalf("unexpected error applying for a second shop: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("expected two distinct shops")
	}

	shops, err := uc.ListByUserID(ctx, "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(shops) != 2 {
		t.Errorf("expected the user to own 2 shops, got %d", len(shops))
	}
}

// TestApprove_IsIndependentPerShop guards the other half of 1:N: approving
// one of a user's shops must not affect their other shop's status.
func TestApprove_IsIndependentPerShop(t *testing.T) {
	uc, _ := newTestVendorUseCase()
	ctx := t.Context()

	shopA, _ := uc.Apply(ctx, "user-1", "Shop A", "")
	shopB, _ := uc.Apply(ctx, "user-1", "Shop B", "")

	if _, err := uc.Approve(ctx, shopA.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := uc.GetOwned(ctx, "user-1", shopB.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("expected shop B to remain pending after shop A was approved, got %q", got.Status)
	}
}

func TestApprove_OnlyPendingCanBeApproved(t *testing.T) {
	uc, audit := newTestVendorUseCase()
	ctx := t.Context()

	v, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	approved, err := uc.Approve(ctx, v.ID, "admin-1")
	if err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}
	if approved.Status != "approved" {
		t.Errorf("expected status approved, got %q", approved.Status)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "approved" {
		t.Errorf("expected one 'approved' audit entry, got %+v", audit.entries)
	}

	// Approving again must fail: an already-decided application can't be
	// silently re-decided.
	_, err = uc.Approve(ctx, v.ID, "admin-1")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict re-approving, got %v", appErr.Code)
	}
}

func TestReject_RequiresReasonAndOnlyPending(t *testing.T) {
	uc, audit := newTestVendorUseCase()
	ctx := t.Context()

	v, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := uc.Reject(ctx, v.ID, "admin-1", ""); err == nil {
		t.Error("expected an error rejecting without a reason")
	}

	rejected, err := uc.Reject(ctx, v.ID, "admin-1", "Incomplete business info")
	if err != nil {
		t.Fatalf("unexpected error rejecting: %v", err)
	}
	if rejected.Status != "rejected" {
		t.Errorf("expected status rejected, got %q", rejected.Status)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != "rejected" {
		t.Errorf("expected one 'rejected' audit entry, got %+v", audit.entries)
	}

	if _, err := uc.Approve(ctx, v.ID, "admin-1"); err == nil {
		t.Error("expected approving an already-rejected application to fail")
	}
}

// TestListAuditLog_ReturnsEntriesInOrder drives a real reject-then-recheck
// sequence through the usecase itself (not by poking the fake directly), so
// this also proves Create and List agree on shape, not just that List can
// read back whatever a test seeded by hand.
func TestListAuditLog_ReturnsEntriesInOrder(t *testing.T) {
	uc, _ := newTestVendorUseCase()
	ctx := t.Context()

	v, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := uc.Reject(ctx, v.ID, "admin-1", "Incomplete business info"); err != nil {
		t.Fatalf("unexpected error rejecting: %v", err)
	}

	entries, err := uc.ListAuditLog(ctx, v.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e.ActorUserID != "admin-1" || e.Action != "rejected" {
		t.Errorf("expected actor=admin-1 action=rejected, got actor=%q action=%q", e.ActorUserID, e.Action)
	}
	if e.Reason == nil || *e.Reason != "Incomplete business info" {
		t.Errorf("expected the rejection reason to carry through, got %v", e.Reason)
	}
}

func TestGetOwned_NotFoundForAnUnknownShop(t *testing.T) {
	uc, _ := newTestVendorUseCase()

	_, err := uc.GetOwned(t.Context(), "user-1", "shop-does-not-exist")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeNotFound {
		t.Errorf("expected not found, got %v", appErr.Code)
	}
}

// TestGetOwned_RejectsAnotherUsersShop is the ownership check every other
// vendor-scoped operation is built on.
func TestGetOwned_RejectsAnotherUsersShop(t *testing.T) {
	uc, _ := newTestVendorUseCase()
	ctx := t.Context()

	v, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = uc.GetOwned(ctx, "user-2", v.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}

func TestUploadLogo_ReplacesAndDeletesTheOldObject(t *testing.T) {
	uc, store := newTestVendorUseCaseWithStore()
	ctx := t.Context()

	v, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	first, err := uc.UploadLogo(ctx, "user-1", v.ID, "image/png", []byte("first-logo"))
	if err != nil {
		t.Fatalf("unexpected error uploading first logo: %v", err)
	}
	if first.LogoURL == nil {
		t.Fatal("expected a logo URL to be set")
	}
	firstKey := *first.LogoObjectKey

	second, err := uc.UploadLogo(ctx, "user-1", v.ID, "image/png", []byte("second-logo"))
	if err != nil {
		t.Fatalf("unexpected error uploading second logo: %v", err)
	}
	if *second.LogoObjectKey == firstKey {
		t.Error("expected the second upload to use a new object key")
	}

	store.mu.Lock()
	deleted := slices.Contains(store.deleted, firstKey)
	_, stillThere := store.uploads[firstKey]
	store.mu.Unlock()
	if !deleted || stillThere {
		t.Errorf("expected the first logo's object key to be deleted, deleted=%v stillThere=%v", deleted, stillThere)
	}
}

func TestUploadLogo_RejectsNonOwner(t *testing.T) {
	uc, _ := newTestVendorUseCase()
	ctx := t.Context()

	v, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = uc.UploadLogo(ctx, "user-2", v.ID, "image/png", []byte("logo"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden, got %v", appErr.Code)
	}
}

func TestUploadBanner_RejectsBadContentType(t *testing.T) {
	uc, _ := newTestVendorUseCase()
	ctx := t.Context()

	v, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = uc.UploadBanner(ctx, "user-1", v.ID, "application/pdf", []byte("not-an-image"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a non-image content type, got %v", appErr.Code)
	}
}

func TestGetPublicProfile_HidesAPendingShop(t *testing.T) {
	uc, _ := newTestVendorUseCase()
	ctx := t.Context()

	v, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = uc.GetPublicProfile(ctx, v.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeNotFound {
		t.Errorf("expected not found for a pending shop, got %v", appErr.Code)
	}
}

func TestGetPublicProfile_ShowsAnApprovedShop(t *testing.T) {
	uc, _ := newTestVendorUseCase()
	ctx := t.Context()

	v, err := uc.Apply(ctx, "user-1", "Alice's Shop", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := uc.Approve(ctx, v.ID, "admin-1"); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}

	found, err := uc.GetPublicProfile(ctx, v.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.ID != v.ID {
		t.Errorf("expected to find the approved shop, got a different one")
	}
}
