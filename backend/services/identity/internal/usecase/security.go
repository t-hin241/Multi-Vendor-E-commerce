package usecase

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/authjwt"
)

type Transactions interface {
	Run(context.Context, func(context.Context) error) error
}
type TokenCipher struct{ aead cipher.AEAD }

func NewTokenCipher(key []byte) (*TokenCipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &TokenCipher{aead}, nil
}
func (c *TokenCipher) Encrypt(token, userID string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(token), []byte(userID)), nil
}
func (c *TokenCipher) Decrypt(data []byte, userID string) (string, error) {
	n := c.aead.NonceSize()
	if len(data) < n {
		return "", errors.New("invalid encrypted token")
	}
	plain, err := c.aead.Open(nil, data[:n], data[n:], []byte(userID))
	return string(plain), err
}
func (uc *AuthUseCase) ValidateSession(ctx context.Context, claims *authjwt.Claims) error {
	if claims.SessionID == "" {
		return authjwt.ErrInvalidToken
	}
	valid, err := uc.refreshTokens.SessionActive(ctx, claims.UserID, claims.Role, claims.SessionID)
	if err != nil {
		return authjwt.ErrVerificationUnavailable
	}
	if !valid {
		return authjwt.ErrInvalidToken
	}
	return nil
}
func (uc *AuthUseCase) transaction(ctx context.Context, fn func(context.Context) error) error {
	err := uc.tx.Run(ctx, fn)
	var app *apperror.Error
	if err != nil && !errors.As(err, &app) {
		return apperror.Internal(err)
	}
	return err
}
