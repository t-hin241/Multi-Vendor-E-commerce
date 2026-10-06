package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/services/identity/internal/usecase"
)

// countingTransactions counts transactions and runs before() as each one
// starts (a change made concurrently, after the password was checked).
type countingTransactions struct {
	runs   int
	before func()
}

func (c *countingTransactions) Run(ctx context.Context, fn func(context.Context) error) error {
	c.runs++
	if c.before != nil {
		c.before()
	}
	return fn(ctx)
}

func newAuthWith(t *testing.T, users *fakeUserRepository, tx *countingTransactions) *usecase.AuthUseCase {
	t.Helper()
	tokenCipher, err := usecase.NewTokenCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	resets := newFakePasswordResetRepository()
	resets.cipher = tokenCipher
	return usecase.NewAuthUseCase(users, newFakeRefreshTokenRepository(), resets, authjwttest.Manager(),
		zerolog.New(&bytes.Buffer{}), tx, tokenCipher)
}

// bcrypt (about 100 ms of CPU) must not hold a transaction: a rejected
// login or registration never opens one.
func TestPasswordHashingRunsOutsideTransactions(t *testing.T) {
	users := newFakeUserRepository()
	tx := &countingTransactions{}
	uc := newAuthWith(t, users, tx)
	ctx := context.Background()

	if _, err := uc.Register(ctx, "buyer@example.test", "correct-horse-1", "Buyer", "buyer"); err != nil {
		t.Fatal(err)
	}
	if tx.runs != 1 {
		t.Fatalf("register used %d transactions, want 1", tx.runs)
	}
	tx.runs = 0
	if _, err := uc.Login(ctx, "buyer@example.test", "wrong-password"); !isUnauthorized(err) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := uc.Register(ctx, "not-an-email", "correct-horse-1", "Buyer", "buyer"); err == nil {
		t.Fatal("invalid registration accepted")
	}
	if tx.runs != 0 {
		t.Fatalf("rejected credentials opened %d transactions", tx.runs)
	}
}

// An account deactivated, or given a new password, while its password was
// being checked gets no session.
func TestLoginRechecksTheAccountInsideTheTransaction(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(*fakeUserRepository, string){
		"deactivated":      func(u *fakeUserRepository, id string) { u.byID[id].IsActive = false },
		"password changed": func(u *fakeUserRepository, id string) { u.byID[id].PasswordHash = "$2a$10$another" },
	} {
		t.Run(name, func(t *testing.T) {
			users := newFakeUserRepository()
			tx := &countingTransactions{}
			uc := newAuthWith(t, users, tx)
			if _, err := uc.Register(ctx, "buyer@example.test", "correct-horse-1", "Buyer", "buyer"); err != nil {
				t.Fatal(err)
			}
			registered, err := users.FindByEmail(ctx, "buyer@example.test")
			if err != nil {
				t.Fatal(err)
			}
			tx.before = func() { change(users, registered.ID) }
			if _, err := uc.Login(ctx, "buyer@example.test", "correct-horse-1"); !isUnauthorized(err) {
				t.Fatalf("login after the account changed: %v", err)
			}
		})
	}
}

func isUnauthorized(err error) bool {
	var appErr *apperror.Error
	return errors.As(err, &appErr) && appErr.Status == 401
}
