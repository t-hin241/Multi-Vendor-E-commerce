package smtp

import (
	"context"
	"fmt"
	"strings"

	"shopee/backend/services/notification/internal/domain"
)

// InvitationSender delivers a shop staff invitation link. The message
// only exists in memory here: it is never stored or logged by Notification.
type InvitationSender struct {
	Host, Port, Username, Password, From string
	AllowPlaintext                       bool
}

func (s InvitationSender) SendInvitation(ctx context.Context, message domain.StaffInvitation) error {
	if strings.ContainsAny(message.URL, "\r\n") {
		return fmt.Errorf("invalid invitation URL")
	}
	shop := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, message.ShopName)
	relay := Relay{Host: s.Host, Port: s.Port, Username: s.Username, Password: s.Password, From: s.From, AllowPlaintext: s.AllowPlaintext}
	return relay.deliver(ctx, message.Email, "You are invited to help run a shop",
		"The shop \""+shop+"\" invited you to help run it.\n\n"+
			"Sign in with this email address, then open this one-time link before "+message.ExpiresAt.UTC().Format("2006-01-02 15:04 UTC")+":\n"+
			message.URL+"\n\nIf you did not expect this, ignore this message; nothing changes until you accept.\n")
}
