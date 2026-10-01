// Package mock is a local stand-in for the email provider. It "sends" by
// logging the type and reference only — never the address or the body.
// Configuration refuses it in production.
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
	s.log.Info().Str("type", email.Type).Str("reference", email.Reference).Msg("mock_email_sent")
	return nil
}
