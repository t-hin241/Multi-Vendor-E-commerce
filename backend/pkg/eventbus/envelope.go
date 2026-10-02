// Package eventbus carries domain events between services on NATS
// JetStream (PLT-03). A producer writes the event to its own outbox in the
// transaction that changes its state and a relay publishes it (Bus.Publish)
// with the event id as the JetStream message id; only the broker's ACK
// marks it published. A consumer (Bus.Consume) records the event in its
// own inbox in the same transaction as the state change it causes and
// ACKs the message after commit, so a redelivered or republished event is
// applied once. Failures are retried a bounded number of times with
// backoff, then parked for an operator to replay or discard, with audit.
// Ordering is not assumed: consumers compare aggregate versions or re-read
// authoritative state.
package eventbus

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Envelope is the common shape of every event (contract in
// pkg/events/CONTRACTS.md).
type Envelope struct {
	EventID          string          `json:"event_id"`
	Type             string          `json:"type"`
	SchemaVersion    int             `json:"schema_version"`
	Producer         string          `json:"producer"`
	AggregateID      string          `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version,omitempty"`
	OccurredAt       time.Time       `json:"occurred_at"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
	CausationID      string          `json:"causation_id,omitempty"`
	Payload          json.RawMessage `json:"payload"`
}

const (
	// StreamName is the one durable stream holding every domain event.
	StreamName = "SHOPEE_EVENTS"
	// SubjectPrefix + type is an event's subject.
	SubjectPrefix = "shopee.events."
	// MaxPayloadBytes bounds an event: events carry references and the few
	// fields a consumer needs, never whole records.
	MaxPayloadBytes = 64 << 10
)

var (
	typePattern = regexp.MustCompile(`^[a-z]+\.[a-z_]+$`)
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

// Subject is the subject an event type is published on.
func Subject(eventType string) string { return SubjectPrefix + eventType }

// New builds an envelope. eventID must be stable for the same fact (the
// outbox row id), so a republish is recognized as the same event.
func New(eventID, eventType string, schemaVersion int, aggregateID string, aggregateVersion int64, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("eventbus: encode %s payload: %w", eventType, err)
	}
	env := Envelope{EventID: eventID, Type: eventType, SchemaVersion: schemaVersion, Producer: strings.SplitN(eventType, ".", 2)[0],
		AggregateID: aggregateID, AggregateVersion: aggregateVersion, OccurredAt: time.Now().UTC(), Payload: raw}
	return env, env.Validate()
}

// Validate checks the envelope fields every consumer relies on.
func (e Envelope) Validate() error {
	switch {
	case !idPattern.MatchString(e.EventID):
		return fmt.Errorf("eventbus: invalid event id")
	case !typePattern.MatchString(e.Type):
		return fmt.Errorf("eventbus: invalid event type %q", e.Type)
	case e.SchemaVersion < 1:
		return fmt.Errorf("eventbus: invalid schema version")
	case e.AggregateID == "" || len(e.AggregateID) > 128:
		return fmt.Errorf("eventbus: invalid aggregate id")
	case len(e.Payload) == 0 || len(e.Payload) > MaxPayloadBytes || !json.Valid(e.Payload):
		return fmt.Errorf("eventbus: invalid payload")
	case len(e.CorrelationID) > 64 || len(e.CausationID) > 128:
		return fmt.Errorf("eventbus: invalid correlation")
	}
	return nil
}

// Decode reads the payload into v.
func (e Envelope) Decode(v any) error {
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return Permanent(fmt.Errorf("eventbus: decode %s v%d: %w", e.Type, e.SchemaVersion, err))
	}
	return nil
}

// WithCorrelation sets the correlation id (the request that caused the
// fact) when it is a valid request id; otherwise the event id is used.
func (e Envelope) WithCorrelation(id string) Envelope {
	if idPattern.MatchString(id) && len(id) <= 64 {
		e.CorrelationID = id
	} else if len(e.EventID) <= 64 {
		e.CorrelationID = e.EventID
	}
	return e
}

// NewID returns a fresh event id for producers without a natural one.
func NewID() string { return uuid.NewString() }
