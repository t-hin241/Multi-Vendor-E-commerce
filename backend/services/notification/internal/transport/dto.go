package transport

import (
	"time"

	"shopee/backend/services/notification/internal/domain"
)

type notifyRequest struct {
	UserID      string `json:"user_id" binding:"required"`
	Type        string `json:"type" binding:"required"`
	ReferenceID string `json:"reference_id" binding:"required"`
}

type notificationResponse struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Type        string    `json:"type"`
	ReferenceID string    `json:"reference_id"`
	Status      string    `json:"status"`
	FailReason  *string   `json:"fail_reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func toNotificationResponseList(notifications []*domain.Notification) []notificationResponse {
	out := make([]notificationResponse, 0, len(notifications))
	for _, n := range notifications {
		out = append(out, notificationResponse{
			ID: n.ID, UserID: n.UserID, Type: string(n.Type), ReferenceID: n.ReferenceID,
			Status: string(n.Status), FailReason: n.FailReason, CreatedAt: n.CreatedAt,
		})
	}
	return out
}
