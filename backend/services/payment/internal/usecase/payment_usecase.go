// Package usecase orchestrates Payment's workflows: starting a payment
// intent for an order, and processing a provider's webhook delivery (or the
// mock "simulate" path standing in for one) idempotently and only ever
// trusting an amount/currency it verified itself.
package usecase

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/provider"
	"shopee/backend/services/payment/internal/repository"
)

type PaymentUseCase struct {
	intents         PaymentIntentRepositoryPort
	events          PaymentEventRepositoryPort
	orders          OrderGateway
	paymentProvider provider.Provider
	verifier        provider.Verifier
	simulator       Simulator
	providerName    string
	log             zerolog.Logger
}

func NewPaymentUseCase(
	intents PaymentIntentRepositoryPort,
	events PaymentEventRepositoryPort,
	orders OrderGateway,
	paymentProvider provider.Provider,
	verifier provider.Verifier,
	simulator Simulator,
	providerName string,
	log zerolog.Logger,
) *PaymentUseCase {
	return &PaymentUseCase{
		intents: intents, events: events, orders: orders,
		paymentProvider: paymentProvider, verifier: verifier, simulator: simulator,
		providerName: providerName, log: log,
	}
}

// CreateIntent starts payment for an order. The amount and currency are
// re-derived from Order right now — never from anything the client sent —
// and any still-pending intent for the same order is reused instead of
// creating a duplicate the buyer could end up paying twice.
func (uc *PaymentUseCase) CreateIntent(ctx context.Context, buyerID, orderID string) (*domain.PaymentIntent, error) {
	order, err := uc.orders.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not have access to this order")
	}
	if order.Status != "pending_payment" {
		return nil, apperror.Conflict("This order is not awaiting payment")
	}

	existing, err := uc.intents.FindPendingByOrderID(ctx, orderID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, repository.ErrPaymentIntentNotFound) {
		return nil, apperror.Internal(err)
	}

	if err := domain.ValidateAmount(order.TotalAmount); err != nil {
		return nil, err
	}

	result, err := uc.paymentProvider.CreateIntent(ctx, provider.CreateIntentInput{
		OrderID: orderID, Amount: order.TotalAmount, Currency: order.Currency,
	})
	if err != nil {
		return nil, apperror.Internal(err)
	}

	intent := &domain.PaymentIntent{
		OrderID: orderID, BuyerID: buyerID, Amount: order.TotalAmount, Currency: order.Currency,
		Status: domain.StatusPending, Provider: uc.providerName, ProviderIntentID: result.ProviderIntentID,
	}
	if err := uc.intents.Create(ctx, intent); err != nil {
		return nil, apperror.Internal(err)
	}
	return intent, nil
}

func (uc *PaymentUseCase) GetOwned(ctx context.Context, buyerID, intentID string) (*domain.PaymentIntent, error) {
	return uc.findOwned(ctx, buyerID, intentID)
}

// Simulate stands in for a real provider's hosted checkout page reporting a
// payment outcome. It builds the exact same signed event a real webhook
// delivery would carry and feeds it through ProcessWebhook, so the two can
// never drift apart. It only works when this deployment is wired with the
// mock provider (Simulator is nil otherwise).
func (uc *PaymentUseCase) Simulate(ctx context.Context, buyerID, intentID string, succeeded bool, failureReason string) (*domain.PaymentIntent, error) {
	if uc.simulator == nil {
		return nil, apperror.Validation("Simulating a payment outcome is only available with the mock payment provider")
	}

	intent, err := uc.findOwned(ctx, buyerID, intentID)
	if err != nil {
		return nil, err
	}
	if intent.Status != domain.StatusPending {
		return nil, apperror.Conflict("This payment has already been processed")
	}

	payload, signature, err := uc.simulator.BuildSignedEvent(intent.ProviderIntentID, intent.Amount, intent.Currency, succeeded, failureReason)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	if err := uc.ProcessWebhook(ctx, payload, signature); err != nil {
		return nil, err
	}
	return uc.intents.FindByID(ctx, intent.ID)
}

// ProcessWebhook is the single entry point any provider webhook delivery (or
// the mock "simulate" path) goes through: verify the signature, deduplicate
// by the provider's event id, then apply the outcome.
func (uc *PaymentUseCase) ProcessWebhook(ctx context.Context, payload []byte, signatureHeader string) error {
	event, err := uc.verifier.Verify(payload, signatureHeader)
	if err != nil {
		return apperror.Unauthorized("Invalid webhook signature")
	}

	intent, err := uc.intents.FindByProviderIntentID(ctx, event.ProviderIntentID)
	if err != nil {
		if errors.Is(err, repository.ErrPaymentIntentNotFound) {
			uc.log.Warn().Str("provider_intent_id", event.ProviderIntentID).Msg("webhook received for an unknown payment intent")
			return nil
		}
		return apperror.Internal(err)
	}

	isNew, err := uc.events.RecordIfNew(ctx, event.ProviderEventID, intent.ID, string(event.Type))
	if err != nil {
		return apperror.Internal(err)
	}
	if !isNew {
		uc.log.Info().
			Str("provider_event_id", event.ProviderEventID).
			Str("payment_intent_id", intent.ID).
			Msg("duplicate webhook delivery ignored")
		return nil
	}

	switch event.Type {
	case provider.EventPaymentSucceeded:
		return uc.applySucceeded(ctx, intent, event)
	case provider.EventPaymentFailed:
		return uc.applyFailed(ctx, intent, event)
	default:
		uc.log.Warn().Str("type", string(event.Type)).Msg("unknown webhook event type ignored")
		return nil
	}
}

func (uc *PaymentUseCase) applySucceeded(ctx context.Context, intent *domain.PaymentIntent, event provider.WebhookEvent) error {
	if intent.Status == domain.StatusCaptured {
		return nil // idempotent: this event was already applied
	}
	if event.Amount != intent.Amount || event.Currency != intent.Currency {
		uc.log.Error().
			Str("payment_intent_id", intent.ID).
			Str("order_id", intent.OrderID).
			Msg("webhook amount/currency does not match the payment intent; refusing to mark the order paid")
		return apperror.Conflict("Webhook amount/currency does not match the payment intent")
	}
	if !domain.CanTransition(intent.Status, domain.StatusCaptured) {
		return apperror.Conflict("Payment cannot be captured from status " + string(intent.Status))
	}

	if err := uc.intents.UpdateStatus(ctx, intent.ID, domain.StatusCaptured, nil); err != nil {
		return apperror.Internal(err)
	}
	if err := uc.orders.MarkPaid(ctx, intent.OrderID); err != nil {
		uc.log.Error().Err(err).Str("order_id", intent.OrderID).Msg("payment captured but marking the order paid failed")
	}
	return nil
}

func (uc *PaymentUseCase) applyFailed(ctx context.Context, intent *domain.PaymentIntent, event provider.WebhookEvent) error {
	if intent.Status == domain.StatusFailed {
		return nil // idempotent
	}
	if !domain.CanTransition(intent.Status, domain.StatusFailed) {
		return apperror.Conflict("Payment cannot be failed from status " + string(intent.Status))
	}

	reason := event.FailureReason
	if reason == "" {
		reason = "Payment declined"
	}
	if err := uc.intents.UpdateStatus(ctx, intent.ID, domain.StatusFailed, &reason); err != nil {
		return apperror.Internal(err)
	}
	if err := uc.orders.MarkPaymentFailed(ctx, intent.OrderID, reason); err != nil {
		uc.log.Error().Err(err).Str("order_id", intent.OrderID).Msg("payment failed but cancelling the order failed")
	}
	return nil
}

func (uc *PaymentUseCase) findOwned(ctx context.Context, buyerID, intentID string) (*domain.PaymentIntent, error) {
	intent, err := uc.intents.FindByID(ctx, intentID)
	if err != nil {
		if errors.Is(err, repository.ErrPaymentIntentNotFound) {
			return nil, apperror.NotFound("Payment not found")
		}
		return nil, apperror.Internal(err)
	}
	if intent.BuyerID != buyerID {
		return nil, apperror.Forbidden("You do not have access to this payment")
	}
	return intent, nil
}
