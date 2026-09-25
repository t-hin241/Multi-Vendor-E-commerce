package transport

import (
	"time"

	"shopee/backend/services/payment/internal/domain"
)

type createIntentRequest struct {
	OrderID string `json:"order_id" binding:"required"`
}

type simulateRequest struct {
	Outcome       string `json:"outcome" binding:"required,oneof=succeeded failed"`
	FailureReason string `json:"failure_reason"`
}

type paymentIntentResponse struct {
	ID               string    `json:"id"`
	OrderID          string    `json:"order_id"`
	Amount           int64     `json:"amount"`
	Currency         string    `json:"currency"`
	Status           string    `json:"status"`
	Provider         string    `json:"provider"`
	ProviderIntentID string    `json:"provider_intent_id"`
	FailureReason    *string   `json:"failure_reason,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func toPaymentIntentResponse(i *domain.PaymentIntent) paymentIntentResponse {
	return paymentIntentResponse{
		ID: i.ID, OrderID: i.OrderID, Amount: i.Amount, Currency: i.Currency,
		Status: string(i.Status), Provider: i.Provider, ProviderIntentID: i.ProviderIntentID,
		FailureReason: i.FailureReason, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
	}
}
