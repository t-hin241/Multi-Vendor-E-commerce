// Package natsclient builds the NATS connection services use to publish and
// consume domain events (OrderCreated, PaymentSucceeded, ...).
package natsclient

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// Connect dials natsURL with reconnect settings suited to a long-lived
// service process, and fails fast if the broker cannot be reached at all
// within the timeout.
func Connect(natsURL string) (*nats.Conn, error) {
	conn, err := nats.Connect(
		natsURL,
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats: connect: %w", err)
	}

	return conn, nil
}
