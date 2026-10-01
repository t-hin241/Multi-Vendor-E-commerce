package smtp

import (
	"context"
	"fmt"
	"strings"

	"shopee/backend/services/notification/internal/usecase"
)

// ResetSender delivers a password reset link. The message only exists in
// memory here: it is never stored or logged by Notification.
type ResetSender struct {
	Host, Port, Username, Password, From string
	AllowPlaintext                       bool
}

func (s ResetSender) SendReset(ctx context.Context, message *usecase.ResetMessage) error {
	if strings.ContainsAny(message.URL, "\r\n") {
		return fmt.Errorf("invalid reset URL")
	}
	relay := Relay{Host: s.Host, Port: s.Port, Username: s.Username, Password: s.Password, From: s.From, AllowPlaintext: s.AllowPlaintext}
	return relay.deliver(ctx, message.Email, "Reset your password",
		"Use this one-time link to reset your password:\n"+message.URL+"\n\nIf you did not request this, ignore this message.\n")
}
