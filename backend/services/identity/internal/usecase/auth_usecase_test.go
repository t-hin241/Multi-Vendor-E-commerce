package usecase_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/authjwt"
	"shopee/backend/services/identity/internal/usecase"
)

func newTestAuthUseCase() *usecase.AuthUseCase {
	uc, _ := newTestAuthUseCaseWithLog()
	return uc
}

type authFixture struct {
	log    *bytes.Buffer
	resets *fakePasswordResetRepository
}

func newTestAuthUseCaseWithLog() (*usecase.AuthUseCase, *authFixture) {
	buf := &bytes.Buffer{}
	tokenCipher, err := usecase.NewTokenCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		panic(err)
	}
	resets := newFakePasswordResetRepository()
	resets.cipher = tokenCipher
	uc := usecase.NewAuthUseCase(newFakeUserRepository(), newFakeRefreshTokenRepository(), resets, authjwt.NewManager("test-secret"), zerolog.New(buf), &fakeTransactions{}, tokenCipher)
	return uc, &authFixture{buf, resets}
}
func (f *authFixture) Reset() { f.log.Reset() }
func extractResetToken(t *testing.T, f *authFixture) string {
	t.Helper()
	token := f.resets.capturedToken
	if token == "" {
		t.Fatal("reset material not queued")
	}
	if strings.Contains(f.log.String(), token) || strings.Contains(f.log.String(), "reset_token") {
		t.Fatal("reset secret leaked to logs")
	}
	return token
}

func mustAppError(t *testing.T, err error) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperror.Error, got %T: %v", err, err)
	}
	return appErr
}

func TestRegister_Success(t *testing.T) {
	uc := newTestAuthUseCase()

	result, err := uc.Register(t.Context(), "alice@example.com", "password123", "Alice", "buyer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatal("expected both tokens to be issued")
	}
	if result.User.Role != "buyer" {
		t.Errorf("expected role buyer, got %q", result.User.Role)
	}
}

func TestRegister_DuplicateEmailIsConflict(t *testing.T) {
	uc := newTestAuthUseCase()
	ctx := t.Context()

	if _, err := uc.Register(ctx, "alice@example.com", "password123", "Alice", "buyer"); err != nil {
		t.Fatalf("unexpected error on first register: %v", err)
	}

	_, err := uc.Register(ctx, "ALICE@example.com", "password123", "Alice2", "buyer")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict, got %v", appErr.Code)
	}
}

func TestRegister_RejectsAdminRole(t *testing.T) {
	uc := newTestAuthUseCase()

	_, err := uc.Register(t.Context(), "alice@example.com", "password123", "Alice", "admin")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error, got %v", appErr.Code)
	}
}

func TestLogin_WrongPasswordIsUnauthorized(t *testing.T) {
	uc := newTestAuthUseCase()
	ctx := t.Context()

	if _, err := uc.Register(ctx, "alice@example.com", "password123", "Alice", "buyer"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := uc.Login(ctx, "alice@example.com", "wrong-password")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeUnauthorized {
		t.Errorf("expected unauthorized, got %v", appErr.Code)
	}
}

func TestLogin_UnknownEmailGivesSameMessageAsWrongPassword(t *testing.T) {
	uc := newTestAuthUseCase()
	ctx := t.Context()

	if _, err := uc.Register(ctx, "alice@example.com", "password123", "Alice", "buyer"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, wrongPasswordErr := uc.Login(ctx, "alice@example.com", "wrong-password")
	_, unknownEmailErr := uc.Login(ctx, "nobody@example.com", "whatever123")

	wp := mustAppError(t, wrongPasswordErr)
	ue := mustAppError(t, unknownEmailErr)

	if wp.Message != ue.Message {
		t.Errorf("expected identical error messages to avoid leaking email existence, got %q vs %q", wp.Message, ue.Message)
	}
}

func TestLogin_Success(t *testing.T) {
	uc := newTestAuthUseCase()
	ctx := t.Context()

	if _, err := uc.Register(ctx, "alice@example.com", "password123", "Alice", "buyer"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := uc.Login(ctx, "alice@example.com", "password123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AccessToken == "" {
		t.Fatal("expected an access token")
	}
}

func TestRefreshToken_RotatesAndInvalidatesOldToken(t *testing.T) {
	uc := newTestAuthUseCase()
	ctx := t.Context()

	registered, err := uc.Register(ctx, "alice@example.com", "password123", "Alice", "buyer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	refreshed, err := uc.RefreshToken(ctx, registered.RefreshToken)
	if err != nil {
		t.Fatalf("unexpected error refreshing: %v", err)
	}
	if refreshed.RefreshToken == registered.RefreshToken {
		t.Fatal("expected a new refresh token to be issued")
	}

	// Reject the refresh token consumed by rotation.
	_, err = uc.RefreshToken(ctx, registered.RefreshToken)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeUnauthorized {
		t.Errorf("expected the rotated-out token to be rejected, got %v", appErr.Code)
	}
}

func TestResetPassword_RevokesAllSessionsAndAllowsNewLogin(t *testing.T) {
	uc, logBuf := newTestAuthUseCaseWithLog()
	ctx := t.Context()

	registered, err := uc.Register(ctx, "alice@example.com", "password123", "Alice", "buyer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	logBuf.Reset()

	if err := uc.RequestPasswordReset(ctx, "alice@example.com"); err != nil {
		t.Fatalf("unexpected error requesting reset: %v", err)
	}
	resetToken := extractResetToken(t, logBuf)

	if err := uc.ResetPassword(ctx, resetToken, "brand-new-password"); err != nil {
		t.Fatalf("unexpected error resetting password: %v", err)
	}

	// Reject refresh tokens issued before the password reset.
	if _, err := uc.RefreshToken(ctx, registered.RefreshToken); err == nil {
		t.Error("expected the pre-reset refresh token to be revoked")
	}

	// Accept the new password and reject the previous password.
	if _, err := uc.Login(ctx, "alice@example.com", "brand-new-password"); err != nil {
		t.Errorf("expected login with the new password to succeed: %v", err)
	}
	if _, err := uc.Login(ctx, "alice@example.com", "password123"); err == nil {
		t.Error("expected login with the old password to fail")
	}

	// Reject reuse of the reset token.
	if err := uc.ResetPassword(ctx, resetToken, "another-password123"); err == nil {
		t.Error("expected a reused reset token to be rejected")
	}
}

func TestResetPassword_RejectsInvalidToken(t *testing.T) {
	uc := newTestAuthUseCase()

	err := uc.ResetPassword(t.Context(), "not-a-real-token", "newpassword123")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeUnauthorized {
		t.Errorf("expected unauthorized for an invalid reset token, got %v", appErr.Code)
	}
}

func TestResetPassword_RejectsShortPassword(t *testing.T) {
	uc := newTestAuthUseCase()

	err := uc.ResetPassword(t.Context(), "irrelevant-token", "short")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error, got %v", appErr.Code)
	}
}
