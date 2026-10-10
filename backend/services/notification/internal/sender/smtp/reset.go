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
	if message.Kind == "email_verification" {
		// PW-022: the account proves it owns this address.
		return relay.deliver(ctx, message.Email, "Confirm your email address",
			"Open this one-time link to confirm this is your email address:\n"+message.URL+
				"\n\nThe link expires in 48 hours. If you did not create an account, ignore this message.\n")
	}
	return relay.deliver(ctx, message.Email, "Reset your password",
		"Use this one-time link to reset your password:\n"+message.URL+"\n\nIf you did not request this, ignore this message.\n")
}
