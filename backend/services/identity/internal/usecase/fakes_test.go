package usecase_test

import (
	"context"
	"sync"
	"time"

	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
)

type fakeUserRepository struct {
	mu     sync.Mutex
	byID   map[string]*domain.User
	nextID int
}

func newFakeUserRepository() *fakeUserRepository {
	return &fakeUserRepository{byID: make(map[string]*domain.User)}
}

func (f *fakeUserRepository) Create(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, existing := range f.byID {
		if existing.Email == u.Email {
			return repository.ErrEmailTaken
		}
	}

	f.nextID++
	u.ID = itoa(f.nextID)
	u.IsActive = true
	u.CreatedAt = time.Now()
	u.UpdatedAt = time.Now()
	stored := *u
	f.byID[u.ID] = &stored
	return nil
}

func (f *fakeUserRepository) FindByEmail(_ context.Context, email string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, u := range f.byID {
		if u.Email == email {
			copyU := *u
			return &copyU, nil
		}
	}
	return nil, repository.ErrUserNotFound
}

func (f *fakeUserRepository) FindByID(_ context.Context, id string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	u, ok := f.byID[id]
	if !ok {
		return nil, repository.ErrUserNotFound
	}
	copyU := *u
	return &copyU, nil
}

func (f *fakeUserRepository) UpdatePasswordHash(_ context.Context, userID, passwordHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	u, ok := f.byID[userID]
	if !ok {
		return repository.ErrUserNotFound
	}
	u.PasswordHash = passwordHash
	return nil
}

type storedRefreshToken struct {
	repository.RefreshToken
}

type fakeRefreshTokenRepository struct {
	mu     sync.Mutex
	byHash map[string]*storedRefreshToken
	nextID int
}

func newFakeRefreshTokenRepository() *fakeRefreshTokenRepository {
	return &fakeRefreshTokenRepository{byHash: make(map[string]*storedRefreshToken)}
}

func (f *fakeRefreshTokenRepository) Create(_ context.Context, userID, tokenHash string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nextID++
	f.byHash[tokenHash] = &storedRefreshToken{repository.RefreshToken{
		ID: itoa(f.nextID), UserID: userID, TokenHash: tokenHash, ExpiresAt: expiresAt,
	}}
	return nil
}

func (f *fakeRefreshTokenRepository) FindActiveByHash(_ context.Context, tokenHash string) (*repository.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rt, ok := f.byHash[tokenHash]
	if !ok || rt.RevokedAt != nil || rt.ExpiresAt.Before(time.Now()) {
		return nil, repository.ErrRefreshTokenNotFound
	}
	copyRT := rt.RefreshToken
	return &copyRT, nil
}

func (f *fakeRefreshTokenRepository) Revoke(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, rt := range f.byHash {
		if rt.ID == id {
			now := time.Now()
			rt.RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeRefreshTokenRepository) RevokeAllForUser(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now()
	for _, rt := range f.byHash {
		if rt.UserID == userID {
			rt.RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeRefreshTokenRepository) RevokeByHash(_ context.Context, tokenHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if rt, ok := f.byHash[tokenHash]; ok {
		now := time.Now()
		rt.RevokedAt = &now
	}
	return nil
}

type fakePasswordResetRepository struct {
	mu     sync.Mutex
	byHash map[string]*repository.PasswordResetToken
	nextID int
}

func newFakePasswordResetRepository() *fakePasswordResetRepository {
	return &fakePasswordResetRepository{byHash: make(map[string]*repository.PasswordResetToken)}
}

func (f *fakePasswordResetRepository) Create(_ context.Context, userID, tokenHash string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nextID++
	f.byHash[tokenHash] = &repository.PasswordResetToken{
		ID: itoa(f.nextID), UserID: userID, TokenHash: tokenHash, ExpiresAt: expiresAt,
	}
	return nil
}

func (f *fakePasswordResetRepository) FindUsableByHash(_ context.Context, tokenHash string) (*repository.PasswordResetToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	t, ok := f.byHash[tokenHash]
	if !ok || t.UsedAt != nil || t.ExpiresAt.Before(time.Now()) {
		return nil, repository.ErrPasswordResetTokenNotFound
	}
	copyT := *t
	return &copyT, nil
}

func (f *fakePasswordResetRepository) MarkUsed(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, t := range f.byHash {
		if t.ID == id {
			now := time.Now()
			t.UsedAt = &now
		}
	}
	return nil
}

func itoa(n int) string {
	digits := "0123456789"
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{digits[n%10]}, buf...)
		n /= 10
	}
	return "user-" + string(buf)
}
