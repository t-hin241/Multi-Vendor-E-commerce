package usecase_test

import (
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

func newTestVendorUseCase() (*usecase.VendorUseCase, *fakeAuditLogRepository) {
	audit := newFakeAuditLogRepository()
	return usecase.NewVendorUseCase(newFakeVendorRepository(), audit, newFakeNotificationGateway(), zerolog.Nop()), audit
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

func TestApply_RejectsSecondApplicationFromSameUser(t *testing.T) {
	uc, _ := newTestVendorUseCase()
	ctx := t.Context()

	if _, err := uc.Apply(ctx, "user-1", "Alice's Shop", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := uc.Apply(ctx, "user-1", "Alice's Second Shop", "")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict, got %v", appErr.Code)
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

func TestGetByUserID_NotFound(t *testing.T) {
	uc, _ := newTestVendorUseCase()

	_, err := uc.GetByUserID(t.Context(), "nobody")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeNotFound {
		t.Errorf("expected not found, got %v", appErr.Code)
	}
}
