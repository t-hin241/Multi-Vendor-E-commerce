package usecase_test

import (
	"context"
	"errors"
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/review/internal/domain"
	"shopee/backend/services/review/internal/usecase"
)

type denyRole struct{}

func (denyRole) RequireRole(context.Context, string, string) error {
	return apperror.Forbidden("Active account with required role needed")
}

// Moderation is refused by the use case itself when the caller cannot be
// verified as an admin, before anything is read or written.
func TestModerationNeedsVerifiedAdmin(t *testing.T) {
	unverified := usecase.NewReviewUseCase(nil, nil, nil, nil, nil)
	if err := unverified.ResolveReport(t.Context(), "admin-1", "report-1", domain.DecisionKeep, "", nil); err == nil {
		t.Fatal("moderation must fail closed without admin verification")
	}
	denied := usecase.NewReviewUseCase(nil, nil, nil, nil, nil).WithAdminVerification(denyRole{})
	var app *apperror.Error
	if err := denied.ResolveReport(t.Context(), "buyer-1", "report-1", domain.DecisionKeep, "", nil); !errors.As(err, &app) || app.Code != apperror.CodeForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
	if _, err := denied.CreateReason(t.Context(), "buyer-1", "spam", "Spam", nil); !errors.As(err, &app) || app.Code != apperror.CodeForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
}
