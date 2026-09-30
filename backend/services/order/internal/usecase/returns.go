package usecase

import (
	"context"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// ReturnInput is a buyer's return request for one delivered order item.
type ReturnInput struct {
	OrderID  string
	ItemID   string
	Quantity int64
	Reason   string
	Evidence string
}

// CreateReturn opens a return under the policy in force: only delivered
// (completed) items, inside the return window, for at most the purchased
// quantity. The policy version and window are copied onto the request.
func (uc *OrderUseCase) CreateReturn(ctx context.Context, buyerID string, in ReturnInput) (*domain.ReturnRequest, error) {
	var rr *domain.ReturnRequest
	err := uc.withOrder(ctx, in.OrderID, func(ctx context.Context) error {
		order, err := uc.findOwnedByBuyer(ctx, buyerID, in.OrderID)
		if err != nil {
			return err
		}
		item, err := uc.Orders.FindItem(ctx, order.ID, in.ItemID)
		if err != nil {
			return notFoundOrInternal(err, repository.ErrOrderNotFound, "Order item not found")
		}
		vo, err := uc.VendorOrders.FindByID(ctx, item.VendorOrderID)
		if err != nil {
			return err
		}
		returned, err := uc.Returns.ReturnedQuantity(ctx, item.ID)
		if err != nil {
			return err
		}
		if in.Quantity == 0 {
			in.Quantity = item.Quantity - returned
		}
		evidence, err := domain.ValidateNote(in.Evidence, 2000, false, "Evidence")
		if err != nil {
			return err
		}
		amount, err := domain.ValidateNewReturn(uc.ReturnPolicy, domain.ReturnEligibility{
			VendorOrderStatus: vo.Status, CompletedAt: vo.CompletedAt, ItemQuantity: item.Quantity, ItemPrice: item.PriceAmount, AlreadyReturned: returned, Now: uc.Now(),
		}, in.Quantity, in.Reason, evidence)
		if err != nil {
			return err
		}
		reason, _ := domain.ValidateNote(in.Reason, 2000, true, "Reason")
		rr = &domain.ReturnRequest{OrderID: order.ID, OrderItemID: item.ID, BuyerID: buyerID, Reason: *reason, Quantity: in.Quantity,
			RefundAmount: amount, PolicyVersion: uc.ReturnPolicy.Version, ReturnWindowDays: ptr(uc.ReturnPolicy.WindowDays), Evidence: evidence}
		if err := uc.Returns.Create(ctx, rr); err != nil {
			return err
		}
		return uc.Returns.AddEvent(ctx, &domain.ReturnEvent{ReturnID: rr.ID, ActorUserID: &buyerID, ActorRole: "buyer", Action: "requested", ToStatus: string(rr.Status)})
	})
	return rr, err
}

// VendorConfirmReturn records the selling vendor's agreement with a return.
func (uc *OrderUseCase) VendorConfirmReturn(ctx context.Context, userID, returnID, note string) (*domain.ReturnRequest, error) {
	orderID, err := uc.authorizeVendorForReturn(ctx, userID, returnID)
	if err != nil {
		return nil, err
	}
	vendorNote, err := domain.ValidateNote(note, 1000, false, "Note")
	if err != nil {
		return nil, err
	}
	return uc.transitionReturn(ctx, orderID, returnID, domain.ReturnVendorConfirmed, "vendor", &userID, "vendor_confirmed", vendorNote,
		repository.ReturnUpdate{VendorNote: vendorNote, VendorActor: &userID})
}

// AdminDecideReturn approves or rejects a return; a rejection needs a
// reason.
func (uc *OrderUseCase) AdminDecideReturn(ctx context.Context, adminID, returnID string, approve bool, note string) (*domain.ReturnRequest, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	decisionNote, err := domain.ValidateNote(note, 1000, !approve, "A rejection reason")
	if err != nil {
		return nil, err
	}
	rr, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil {
		return nil, notFoundOrInternal(err, repository.ErrReturnRequestNotFound, "Return request not found")
	}
	to, action := domain.ReturnApproved, "approved"
	if !approve {
		to, action = domain.ReturnRejected, "rejected"
	}
	return uc.transitionReturn(ctx, rr.OrderID, returnID, to, "admin", &adminID, action, decisionNote,
		repository.ReturnUpdate{DecisionNote: decisionNote, DecidedBy: &adminID})
}

// MarkReturnReceived records that the returned goods arrived and were
// inspected (by the selling vendor or an admin), then requests the refund
// from Payment and, if chosen, puts the units back into stock. Money is
// only marked returned when Payment confirms.
func (uc *OrderUseCase) MarkReturnReceived(ctx context.Context, actorID, role, returnID string, restock bool, note string) (*domain.ReturnRequest, error) {
	var orderID string
	switch role {
	case "vendor":
		id, err := uc.authorizeVendorForReturn(ctx, actorID, returnID)
		if err != nil {
			return nil, err
		}
		orderID = id
	case "admin":
		if err := uc.requireAdmin(ctx, actorID); err != nil {
			return nil, err
		}
		rr, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return nil, notFoundOrInternal(err, repository.ErrReturnRequestNotFound, "Return request not found")
		}
		orderID = rr.OrderID
	default:
		return nil, apperror.Forbidden("Only the vendor or an admin can receive a return")
	}
	inspection, err := domain.ValidateNote(note, 1000, false, "Inspection note")
	if err != nil {
		return nil, err
	}

	var result *domain.ReturnRequest
	err = uc.withOrder(ctx, orderID, func(ctx context.Context) error {
		rr, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return err
		}
		if err := uc.stepReturn(ctx, rr, domain.ReturnReceived, role, &actorID, "received", inspection,
			repository.ReturnUpdate{ReceivedBy: &actorID, InspectionNote: inspection, Restock: &restock}); err != nil {
			return err
		}
		if restock {
			if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: rr.OrderID, Kind: domain.EffectRestockReturn, Target: rr.ID}); err != nil {
				return err
			}
		}
		if err := uc.requestReturnRefund(ctx, rr, actorID); err != nil {
			return err
		}
		result = rr
		return nil
	})
	if err != nil {
		return nil, err
	}
	uc.runEffectsSoon(ctx, orderID)
	return result, nil
}

// RetryReturnRefund requests a new refund for a return whose refund failed.
func (uc *OrderUseCase) RetryReturnRefund(ctx context.Context, adminID, returnID string) (*domain.ReturnRequest, error) {
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	rr, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil {
		return nil, notFoundOrInternal(err, repository.ErrReturnRequestNotFound, "Return request not found")
	}
	var result *domain.ReturnRequest
	err = uc.withOrder(ctx, rr.OrderID, func(ctx context.Context) error {
		current, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return err
		}
		if current.Status != domain.ReturnRefundFailed {
			return apperror.Conflict("Only a return whose refund failed can be retried")
		}
		if err := uc.requestReturnRefund(ctx, current, adminID); err != nil {
			return err
		}
		result = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	uc.runEffectsSoon(ctx, rr.OrderID)
	return result, nil
}

// requestReturnRefund creates the refund of a received return (capped by
// what is still refundable) and moves the return to refund_pending.
func (uc *OrderUseCase) requestReturnRefund(ctx context.Context, rr *domain.ReturnRequest, actorID string) error {
	order, err := uc.findOrder(ctx, rr.OrderID)
	if err != nil {
		return err
	}
	vendorOrderID, _, err := uc.Returns.VendorOf(ctx, rr.ID)
	if err != nil {
		return err
	}
	vo, err := uc.VendorOrders.FindByID(ctx, vendorOrderID)
	if err != nil {
		return err
	}
	refundable, err := uc.refundableFor(ctx, order, vo)
	if err != nil {
		return err
	}
	reason, err := domain.ValidateRefundRequest(rr.RefundAmount, refundable, "Return of "+rr.OrderItemID)
	if err != nil {
		return err
	}
	refund := &domain.Refund{OrderID: order.ID, VendorOrderID: &vo.ID, ReturnRequestID: &rr.ID, ReasonCode: domain.RefundReasonReturn,
		Amount: rr.RefundAmount, Currency: order.Currency, Reason: reason, RequestedBy: actorID}
	if err := uc.createRefund(ctx, refund); err != nil {
		return err
	}
	return uc.stepReturn(ctx, rr, domain.ReturnRefundPending, "system", nil, "refund_requested", nil, repository.ReturnUpdate{})
}

// transitionReturn locks the order and applies one audited return step.
func (uc *OrderUseCase) transitionReturn(ctx context.Context, orderID, returnID string, to domain.ReturnRequestStatus, role string, actor *string,
	action string, note *string, u repository.ReturnUpdate) (*domain.ReturnRequest, error) {
	var result *domain.ReturnRequest
	err := uc.withOrder(ctx, orderID, func(ctx context.Context) error {
		rr, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return notFoundOrInternal(err, repository.ErrReturnRequestNotFound, "Return request not found")
		}
		if err := uc.stepReturn(ctx, rr, to, role, actor, action, note, u); err != nil {
			return err
		}
		result = rr
		return nil
	})
	return result, err
}

// stepReturn validates and applies one transition with its audit entry,
// in the caller's transaction.
func (uc *OrderUseCase) stepReturn(ctx context.Context, rr *domain.ReturnRequest, to domain.ReturnRequestStatus, role string, actor *string,
	action string, note *string, u repository.ReturnUpdate) error {
	from := rr.Status
	if !domain.CanTransitionReturn(from, to) {
		return apperror.Conflict("Cannot move this return from " + string(from) + " to " + string(to))
	}
	if err := uc.Returns.Transition(ctx, rr, to, u); err != nil {
		return err
	}
	fromStatus := string(from)
	return uc.Returns.AddEvent(ctx, &domain.ReturnEvent{ReturnID: rr.ID, ActorUserID: actor, ActorRole: role, Action: action,
		FromStatus: &fromStatus, ToStatus: string(to), Note: note})
}

// moveReturn applies a system step driven by a refund outcome.
func (uc *OrderUseCase) moveReturn(ctx context.Context, returnID string, to domain.ReturnRequestStatus, role string, actor *string, action string, note *string) error {
	rr, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil {
		return err
	}
	if rr.Status == to {
		return nil
	}
	return uc.stepReturn(ctx, rr, to, role, actor, action, note, repository.ReturnUpdate{})
}

// authorizeVendorForReturn checks the caller is an approved member of the
// shop that sold the returned item; it returns the order id.
func (uc *OrderUseCase) authorizeVendorForReturn(ctx context.Context, userID, returnID string) (string, error) {
	rr, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil {
		return "", notFoundOrInternal(err, repository.ErrReturnRequestNotFound, "Return request not found")
	}
	_, vendorID, err := uc.Returns.VendorOf(ctx, returnID)
	if err != nil {
		return "", appError(err)
	}
	if _, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vendorID); err != nil {
		return "", apperror.Forbidden("You do not have access to this return")
	}
	return rr.OrderID, nil
}

// restockReturn is the restock_return effect.
func (uc *OrderUseCase) restockReturn(ctx context.Context, returnID string) error {
	rr, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil {
		return appError(err)
	}
	item, err := uc.Orders.FindItem(ctx, rr.OrderID, rr.OrderItemID)
	if err != nil {
		return appError(err)
	}
	return uc.Inventory.RestockReturn(ctx, rr.ID, item.ProductID, item.VariantID, rr.Quantity)
}

// ListMyReturns lists a buyer's own return requests.
func (uc *OrderUseCase) ListMyReturns(ctx context.Context, buyerID string, limit, offset int) ([]*domain.ReturnRequest, error) {
	items, err := uc.Returns.ListByBuyer(ctx, buyerID, limit, offset)
	return items, asError(err)
}

// ListVendorReturns lists returns of items the caller's shop sold.
func (uc *OrderUseCase) ListVendorReturns(ctx context.Context, userID, vendorID, status string, limit, offset int) ([]*domain.ReturnRequest, error) {
	vendorID, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}
	if err := validReturnStatus(status); err != nil {
		return nil, err
	}
	items, err := uc.Returns.ListForVendor(ctx, vendorID, status, limit, offset)
	return items, asError(err)
}

// ListReturns lists return requests for admin, optionally by status.
func (uc *OrderUseCase) ListReturns(ctx context.Context, status string, limit, offset int) ([]*domain.ReturnRequest, error) {
	if err := validReturnStatus(status); err != nil {
		return nil, err
	}
	items, err := uc.Returns.ListByStatus(ctx, status, limit, offset)
	return items, asError(err)
}

// ReturnHistory returns the audit trail of a return. buyerID/vendor access
// is checked by the caller-specific wrappers; admin reads it directly.
func (uc *OrderUseCase) ReturnHistory(ctx context.Context, returnID string) ([]*domain.ReturnEvent, error) {
	events, err := uc.Returns.ListEvents(ctx, returnID)
	return events, asError(err)
}

func validReturnStatus(status string) error {
	if status == "" {
		return nil
	}
	for _, s := range []domain.ReturnRequestStatus{domain.ReturnRequested, domain.ReturnVendorConfirmed, domain.ReturnRejected, domain.ReturnApproved,
		domain.ReturnReceived, domain.ReturnRefundPending, domain.ReturnRefunded, domain.ReturnRefundFailed} {
		if string(s) == status {
			return nil
		}
	}
	return apperror.Validation("Invalid return status filter")
}
