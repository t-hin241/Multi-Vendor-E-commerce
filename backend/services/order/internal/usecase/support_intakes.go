package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// SupportIntakePort stores buyers' requests without an order id (PW-012).
type SupportIntakePort interface {
	Create(ctx context.Context, in *domain.SupportIntake) error
	FindByID(ctx context.Context, id string) (*domain.SupportIntake, error)
	FindByKey(ctx context.Context, buyerID, key string) (*domain.SupportIntake, error)
	CountOpen(ctx context.Context, buyerID string) (int, error)
	ListByBuyer(ctx context.Context, buyerID string, limit int) ([]*domain.SupportIntake, error)
	List(ctx context.Context, status string, limit, offset int) ([]*domain.SupportIntake, error)
	Save(ctx context.Context, in *domain.SupportIntake) error
}

// SupportIntakeInput is what the buyer sends.
type SupportIntakeInput struct {
	ReferenceKind  string
	Reference      string
	Message        string
	IdempotencyKey string
}

func intakeError(err error) error {
	switch {
	case errors.Is(err, repository.ErrIntakeNotFound):
		return apperror.NotFound("Support request not found")
	case errors.Is(err, repository.ErrStaleState):
		return domain.IntakeChanged()
	case errors.Is(err, repository.ErrSupportKeyTaken):
		return domain.SupportKeyReused()
	}
	return asError(err)
}

// CreateSupportIntake records the request; at most a few open per buyer.
func (uc *OrderUseCase) CreateSupportIntake(ctx context.Context, buyerID string, in SupportIntakeInput) (*domain.SupportIntake, bool, error) {
	if uc.Intakes == nil || uc.Support == nil || !uc.SupportConfig.Enabled {
		return nil, false, domain.SupportDisabled()
	}
	reference, err := domain.ValidateIntakeReference(in.ReferenceKind, in.Reference)
	if err != nil {
		return nil, false, err
	}
	text, err := uc.SupportPolicy.ValidateSupportText(in.Message)
	if err != nil {
		return nil, false, err
	}
	if err := validSupportKey(in.IdempotencyKey); err != nil {
		return nil, false, err
	}
	hash := requestHash(in.ReferenceKind, reference, text)
	var intake *domain.SupportIntake
	replayed := false
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		if in.IdempotencyKey != "" {
			existing, err := uc.Intakes.FindByKey(ctx, buyerID, in.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing != nil {
				if existing.RequestHash == nil || *existing.RequestHash != hash {
					return domain.SupportKeyReused()
				}
				intake, replayed = existing, true
				return nil
			}
		}
		open, err := uc.Intakes.CountOpen(ctx, buyerID)
		if err != nil {
			return err
		}
		if open >= domain.MaxOpenIntakes {
			return domain.TooManyIntakes()
		}
		intake = &domain.SupportIntake{BuyerID: buyerID, ReferenceKind: in.ReferenceKind, Reference: reference, Message: text, RequestHash: &hash}
		if in.IdempotencyKey != "" {
			intake.IdempotencyKey = &in.IdempotencyKey
		}
		if err := uc.Intakes.Create(ctx, intake); err != nil {
			return err
		}
		return uc.queueBuyerNotice(ctx, intake.BuyerID, noticeIntakeReceived, intake.ID)
	})
	if err != nil {
		return nil, false, intakeError(err)
	}
	if !replayed {
		uc.Log.Info().Str("intake_id", intake.ID).Str("reference_kind", intake.ReferenceKind).Msg("order_support_intake_opened")
	}
	return intake, replayed, nil
}

func (uc *OrderUseCase) ListMySupportIntakes(ctx context.Context, buyerID string) ([]*domain.SupportIntake, error) {
	if uc.Intakes == nil {
		return []*domain.SupportIntake{}, nil
	}
	out, err := uc.Intakes.ListByBuyer(ctx, buyerID, 50)
	return out, intakeError(err)
}

func (uc *OrderUseCase) ListSupportIntakes(ctx context.Context, status string, limit, offset int) ([]*domain.SupportIntake, error) {
	switch status {
	case "", domain.IntakeOpen, domain.IntakeLinked, domain.IntakeClosed:
	default:
		return nil, apperror.Validation("status must be open, linked or closed")
	}
	if uc.Intakes == nil {
		return []*domain.SupportIntake{}, nil
	}
	out, err := uc.Intakes.List(ctx, status, limit, offset)
	return out, intakeError(err)
}

// LinkIntakeInput is the order an admin verified for an intake.
type LinkIntakeInput struct {
	OrderID         string
	VendorOrderID   string
	Category        string
	ExpectedVersion int64
	Reason          string
}

// LinkSupportIntake attaches an intake to an order that belongs to the
// same buyer: the buyer's message opens a support case on the vendor
// order, or joins the buyer's case already open there for the category.
func (uc *OrderUseCase) LinkSupportIntake(ctx context.Context, adminID, intakeID string, in LinkIntakeInput) (*domain.SupportCase, error) {
	if uc.Intakes == nil || uc.Support == nil {
		return nil, domain.SupportDisabled()
	}
	reason, err := adminReason(in.Reason)
	if err != nil {
		return nil, err
	}
	category, err := domain.ParseSupportCategory(in.Category)
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	var result *domain.SupportCase
	err = uc.withOrder(ctx, in.OrderID, func(ctx context.Context) error {
		intake, err := uc.Intakes.FindByID(ctx, intakeID)
		if err != nil {
			return err
		}
		if intake.Status != domain.IntakeOpen || intake.Version != in.ExpectedVersion {
			return domain.IntakeChanged()
		}
		order, err := uc.findOrder(ctx, in.OrderID)
		if err != nil {
			return err
		}
		// The admin found the order from the buyer's reference; it must be
		// that buyer's, whatever the reference said.
		if order.BuyerID != intake.BuyerID {
			return domain.IntakeOrderMismatch()
		}
		vo, err := uc.VendorOrders.FindByID(ctx, in.VendorOrderID)
		if err != nil || vo.OrderID != order.ID {
			return apperror.NotFound("Vendor order not found in this order")
		}
		if err := domain.CheckCaseEligibility(category, vo.Status); err != nil {
			return err
		}
		buyer := SupportActor{ID: intake.BuyerID, Role: "buyer"}
		c, err := uc.Support.FindNotClosed(ctx, intake.BuyerID, vo.ID, category)
		if err != nil {
			return err
		}
		action := "intake_added"
		if c == nil {
			now := uc.Now()
			c = &domain.SupportCase{OrderID: order.ID, VendorOrderID: vo.ID, VendorID: vo.VendorID, BuyerID: intake.BuyerID, Category: category,
				Status: domain.CaseOpen, PolicyVersion: uc.SupportPolicy.Version, DueAt: uc.SupportPolicy.DueAt(domain.CaseOpen, now),
				FinancialHold: category.AffectsMoney()}
			if err := uc.Support.Create(ctx, c); err != nil {
				return err
			}
			if err := uc.prepareCaseHold(ctx, c); err != nil {
				return err
			}
			notice := domain.NewNotifyEffect(order.ID, intake.BuyerID, notifySupportCaseOpened)
			notice.Target = notifySupportCaseOpened + ":" + c.ID
			if err := uc.Effects.Enqueue(ctx, notice); err != nil {
				return err
			}
			action = "opened_from_intake"
		}
		if _, err := uc.addMessage(ctx, c, buyer, intake.Message, domain.VisibilityPublic, nil, nil, nil); err != nil {
			return err
		}
		note := "Request " + intake.ID + " (" + intake.ReferenceKind + " " + intake.Reference + "): " + *reason
		if err := uc.Support.AddEvent(ctx, &domain.SupportCaseEvent{CaseID: c.ID, ActorID: &adminID, ActorRole: "admin", Action: action,
			ToStatus: string(c.Status), Note: &note}); err != nil {
			return err
		}
		now := uc.Now().UTC()
		intake.Status, intake.LinkedCaseID, intake.HandledBy, intake.HandledAt = domain.IntakeLinked, &c.ID, &adminID, &now
		if err := uc.Intakes.Save(ctx, intake); err != nil {
			return err
		}
		if err := uc.audit(ctx, domain.AdminAction{ActorID: adminID, Action: "support_intake_linked", EntityType: domain.AuditSupportIntake,
			EntityID: intake.ID, OrderID: &order.ID, Reason: reason,
			Changes: map[string]any{"case_id": c.ID, "vendor_order_id": vo.ID, "category": string(category), "new_case": action == "opened_from_intake"}}); err != nil {
			return err
		}
		result = c
		return nil
	})
	if err != nil {
		return nil, intakeError(err)
	}
	uc.Log.Info().Str("intake_id", intakeID).Str("case_id", result.ID).Str("order_id", result.OrderID).Msg("order_support_intake_linked")
	uc.runEffectsSoon(ctx, result.OrderID)
	return result, nil
}

// CloseSupportIntake answers an intake that matches no order of the
// buyer; the reason is shown to the buyer.
func (uc *OrderUseCase) CloseSupportIntake(ctx context.Context, adminID, intakeID string, expectedVersion int64, reason string) (*domain.SupportIntake, error) {
	if uc.Intakes == nil {
		return nil, domain.SupportDisabled()
	}
	note, err := adminReason(reason)
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	var intake *domain.SupportIntake
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		intake, err = uc.Intakes.FindByID(ctx, intakeID)
		if err != nil {
			return err
		}
		if intake.Status != domain.IntakeOpen || intake.Version != expectedVersion {
			return domain.IntakeChanged()
		}
		now := uc.Now().UTC()
		intake.Status, intake.HandledBy, intake.HandledAt, intake.CloseReason = domain.IntakeClosed, &adminID, &now, note
		if err := uc.Intakes.Save(ctx, intake); err != nil {
			return err
		}
		if err := uc.queueBuyerNotice(ctx, intake.BuyerID, noticeIntakeClosed, intake.ID); err != nil {
			return err
		}
		return uc.audit(ctx, domain.AdminAction{ActorID: adminID, Action: "support_intake_closed", EntityType: domain.AuditSupportIntake,
			EntityID: intake.ID, Reason: note})
	})
	if err != nil {
		return nil, intakeError(err)
	}
	return intake, nil
}
