// Package sender defines Notification's boundary with an outbound email
// provider. The use case depends only on this interface, never on a
// concrete SMTP/SES/SendGrid SDK, so wiring in a real provider later is
// adding a new implementation, not touching business logic.
package sender

import "context"

type Email struct {
	ToEmail   string
	ToName    string
	Type      string
	Reference string
}

// Sender delivers one email. Implementations must never log the message
// body or recipient beyond what's needed to debug delivery — no secret or
// provider API key ever flows through here.
type Sender interface {
	Send(ctx context.Context, email Email) error
}
