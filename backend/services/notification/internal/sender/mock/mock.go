// Package mock is a local stand-in for a real email provider (SES,
// SendGrid, Postmark, ...), used until this deployment has real provider
// credentials. It "sends" an email by logging that it would have, never the
// address or body in full — just enough to confirm delivery was attempted.
package mock

import (
	"context"

	"github.com/rs/zerolog"

	"shopee/backend/services/notification/internal/sender"
)

type Sender struct {
	log zerolog.Logger
}

func New(log zerolog.Logger) *Sender {
	return &Sender{log: log}
}

func (s *Sender) Send(_ context.Context, email sender.Email) error {
	s.log.Info().
		Str("type", email.Type).
		Str("reference", email.Reference).
		Str("to_name", email.ToName).
		Msg("mock_email_sent")
	return nil
}
