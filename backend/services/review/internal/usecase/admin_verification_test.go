package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/review/internal/domain"
	"shopee/backend/services/review/internal/usecase"
)

type denyRole struct{}

func (denyRole) RequireRole(context.Context, string, string) error {
	return apperror.Forbidden("Active account with required role needed")
}

const (
	someAdmin  = "00000000-0000-0000-0000-00000000000a"
	someReport = "00000000-0000-0000-0000-00000000000b"
	someReview = "00000000-0000-0000-0000-00000000000c"
	someReason = "00000000-0000-0000-0000-00000000000d"
)

// Moderation is refused by the use case itself when the caller cannot be
// verified as an admin, before anything is read or written (no repository
// is configured here: touching it would panic).
func TestModerationNeedsVerifiedAdmin(t *testing.T) {
	unverified := usecase.NewReviewUseCase(usecase.Deps{Log: zerolog.Nop()})
	if err := unverified.ResolveReport(t.Context(), someAdmin, someReport, domain.DecisionKeep, "", nil); err == nil {
		t.Fatal("moderation must fail closed without admin verification")
	}
	denied := usecase.NewReviewUseCase(usecase.Deps{Roles: denyRole{}, Log: zerolog.Nop()})
	note := "reason"
	var app *apperror.Error
	for name, call := range map[string]func() error{
		"resolve": func() error {
			return denied.ResolveReport(t.Context(), someAdmin, someReport, domain.DecisionKeep, "", nil)
		},
		"hide":    func() error { return denied.Hide(t.Context(), someAdmin, someReview, someReason, &note) },
		"restore": func() error { return denied.Restore(t.Context(), someAdmin, someReview, &note) },
		"reason": func() error {
			_, err := denied.CreateReason(t.Context(), someAdmin, "spam", "Spam", nil)
			return err
		},
		"operations": func() error {
			_, err := denied.Operations(t.Context(), someAdmin)
			return err
		},
	} {
		if err := call(); !errors.As(err, &app) || app.Code != apperror.CodeForbidden {
			t.Errorf("%s: expected forbidden, got %v", name, err)
		}
	}
}
