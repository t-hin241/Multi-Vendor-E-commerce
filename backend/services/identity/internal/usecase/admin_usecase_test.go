package usecase_test

import (
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/usecase"
)

func newAdminFixture() (*usecase.AdminUseCase, *fakeUserRepository, *fakeRefreshTokenRepository) {
	users := newFakeUserRepository()
	refreshTokens := newFakeRefreshTokenRepository()
	return usecase.NewAdminUseCase(users, refreshTokens), users, refreshTokens
}

func mustCreateUser(t *testing.T, users *fakeUserRepository, email string, role domain.Role) *domain.User {
	t.Helper()
	u := &domain.User{Email: email, PasswordHash: "x", FullName: "Test User", Role: role}
	if err := users.Create(t.Context(), u); err != nil {
		t.Fatalf("unexpected error seeding user: %v", err)
	}
	return u
}

func TestListUsers_FiltersByRole(t *testing.T) {
	admin, users, _ := newAdminFixture()
	mustCreateUser(t, users, "buyer@test.local", domain.RoleBuyer)
	mustCreateUser(t, users, "vendor@test.local", domain.RoleVendor)

	got, err := admin.ListUsers(t.Context(), "vendor", "", 20, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Role != domain.RoleVendor {
		t.Fatalf("expected exactly one vendor, got %+v", got)
	}
}

func TestListUsers_RejectsInvalidRole(t *testing.T) {
	admin, _, _ := newAdminFixture()

	_, err := admin.ListUsers(t.Context(), "superuser", "", 20, 0)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for an unknown role filter, got %v", appErr.Code)
	}
}

func TestSetActive_404sOnUnknownUser(t *testing.T) {
	admin, _, _ := newAdminFixture()

	_, err := admin.SetActive(t.Context(), "no-such-user", false)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeNotFound {
		t.Errorf("expected not found, got %v", appErr.Code)
	}
}

func TestSetActive_RejectsTargetingAnAdmin(t *testing.T) {
	admin, users, _ := newAdminFixture()
	target := mustCreateUser(t, users, "otheradmin@test.local", domain.RoleAdmin)

	_, err := admin.SetActive(t.Context(), target.ID, false)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden when targeting an admin account, got %v", appErr.Code)
	}
}

func TestSetActive_DeactivatingRevokesExistingSessions(t *testing.T) {
	admin, users, refreshTokens := newAdminFixture()
	target := mustCreateUser(t, users, "buyer@test.local", domain.RoleBuyer)
	if err := refreshTokens.Create(t.Context(), target.ID, "session-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("unexpected error seeding a session: %v", err)
	}

	updated, err := admin.SetActive(t.Context(), target.ID, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.IsActive {
		t.Errorf("expected the returned user to reflect is_active=false")
	}

	if _, err := refreshTokens.FindActiveByHash(t.Context(), "session-hash"); err == nil {
		t.Errorf("expected the user's existing session to be revoked on deactivation")
	}
}

func TestSetActive_ReactivatingDoesNotTouchSessions(t *testing.T) {
	admin, users, refreshTokens := newAdminFixture()
	target := mustCreateUser(t, users, "buyer@test.local", domain.RoleBuyer)
	if err := refreshTokens.Create(t.Context(), target.ID, "session-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("unexpected error seeding a session: %v", err)
	}

	if _, err := admin.SetActive(t.Context(), target.ID, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := refreshTokens.FindActiveByHash(t.Context(), "session-hash"); err != nil {
		t.Errorf("expected the session to remain active when reactivating, got %v", err)
	}
}
