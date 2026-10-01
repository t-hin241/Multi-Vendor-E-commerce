package transport

import (
	"time"

	"shopee/backend/services/notification/internal/domain"
)

// notifyRequest is a producing service's event. event_id identifies the
// event at its source (an outbox row id); a repeat with the same id is a
// duplicate. Without it, type + reference_id identify the event.
type notifyRequest struct {
	EventID       string `json:"event_id" binding:"max=100"`
	Source        string `json:"source" binding:"max=30"`
	UserID        string `json:"user_id" binding:"required"`
	Type          string `json:"type" binding:"required,max=60"`
	ReferenceID   string `json:"reference_id" binding:"required,max=100"`
	CorrelationID string `json:"correlation_id" binding:"max=64"`
}

type retryRequest struct {
	Reason string `json:"reason" binding:"required,max=500"`
}

// notificationResponse never carries the recipient address, only a mask.
type notificationResponse struct {
	ID              string     `json:"id"`
	EventID         string     `json:"event_id"`
	Source          string     `json:"source"`
	UserID          string     `json:"user_id"`
	Type            string     `json:"type"`
	TemplateVersion string     `json:"template_version"`
	ReferenceID     string     `json:"reference_id"`
	Status          string     `json:"status"`
	Recipient       *string    `json:"recipient,omitempty"`
	FailReason      *string    `json:"fail_reason,omitempty"`
	Attempts        int        `json:"attempts"`
	MaxAttempts     int        `json:"max_attempts"`
	NextAttemptAt   time.Time  `json:"next_attempt_at"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func toNotificationResponse(n *domain.Notification) notificationResponse {
	return notificationResponse{
		ID: n.ID, EventID: n.EventID, Source: n.Source, UserID: n.UserID, Type: string(n.Type), TemplateVersion: n.TemplateVersion,
		ReferenceID: n.ReferenceID, Status: string(n.Status), Recipient: n.RecipientMasked, FailReason: n.FailReason,
		Attempts: n.Attempts, MaxAttempts: n.MaxAttempts, NextAttemptAt: n.NextAttemptAt, SentAt: n.SentAt,
		CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
	}
}

func toNotificationResponseList(notifications []*domain.Notification) []notificationResponse {
	out := make([]notificationResponse, 0, len(notifications))
	for _, n := range notifications {
		out = append(out, toNotificationResponse(n))
	}
	return out
}

type attemptResponse struct {
	Attempt    int       `json:"attempt"`
	Outcome    string    `json:"outcome"`
	Error      *string   `json:"error,omitempty"`
	DurationMS int       `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

func toAttemptResponseList(items []*domain.Attempt) []attemptResponse {
	out := make([]attemptResponse, 0, len(items))
	for _, a := range items {
		out = append(out, attemptResponse{Attempt: a.Attempt, Outcome: a.Outcome, Error: a.Error, DurationMS: a.DurationMS, CreatedAt: a.CreatedAt})
	}
	return out
}
