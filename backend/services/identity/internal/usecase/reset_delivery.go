package usecase

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"shopee/backend/services/identity/internal/repository"
)

type ResetDeliveryStore interface {
	ClaimDelivery(context.Context) (string, error)
	ReadDelivery(context.Context, string) (*repository.ResetDelivery, error)
	FinishDelivery(context.Context, string, bool) error
}
type ResetNotifier interface {
	NotifyReset(context.Context, string) error
}
type ResetDeliveryUseCase struct {
	Store    ResetDeliveryStore
	Cipher   *TokenCipher
	Notifier ResetNotifier
	ResetURL string
	// VerifyURL is the page an email verification link opens (PW-022).
	VerifyURL string
	Log       zerolog.Logger
}
type ResetMessage struct {
	Email string `json:"email"`
	URL   string `json:"url"`
	// Kind: password_reset, or email_verification (PW-022).
	Kind string `json:"kind"`
}

func (u *ResetDeliveryUseCase) Message(ctx context.Context, id string) (*ResetMessage, error) {
	d, err := u.Store.ReadDelivery(ctx, id)
	if err != nil {
		return nil, err
	}
	token, err := u.Cipher.Decrypt(d.EncryptedToken, d.UserID)
	if err != nil {
		return nil, err
	}
	base := u.ResetURL
	if d.Kind == "email_verification" {
		base = u.VerifyURL
	}
	link, err := url.Parse(base)
	if err != nil || base == "" {
		return nil, errors.New("no link configured for " + d.Kind)
	}
	// Put the token in the URL fragment.
	link.Fragment = "token=" + url.QueryEscape(token)
	return &ResetMessage{Email: d.Email, URL: link.String(), Kind: d.Kind}, nil
}
func (u *ResetDeliveryUseCase) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.DeliverNext(ctx)
		}
	}
}
func (u *ResetDeliveryUseCase) DeliverNext(ctx context.Context) {
	jobCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id, err := u.Store.ClaimDelivery(jobCtx)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		u.Log.Error().Msg("reset_delivery_claim_failed")
		return
	}
	err = u.Notifier.NotifyReset(jobCtx, id)
	if err != nil {
		u.Log.Warn().Str("delivery_id", id).Msg("reset_delivery_retry")
	}
	if finishErr := u.Store.FinishDelivery(jobCtx, id, err == nil); finishErr != nil {
		u.Log.Error().Str("delivery_id", id).Msg("reset_delivery_persist_failed")
	}
}
