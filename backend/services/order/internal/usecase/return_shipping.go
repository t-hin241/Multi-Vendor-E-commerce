package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/order/internal/adapter"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// ReturnShippingPort stores AF-05 return shipping on return requests.
type ReturnShippingPort interface {
	SaveShipping(ctx context.Context, rr *domain.ReturnRequest) error
	SetReturnShipment(ctx context.Context, returnID, shipmentID string) error
	AddReceipt(ctx context.Context, g *domain.ReturnReceipt) error
	LatestReceipt(ctx context.Context, returnID string) (*domain.ReturnReceipt, error)
	ListDispatchOverdue(ctx context.Context, now time.Time, limit int) ([]*domain.ReturnRequest, error)
	ListDispatchDueSoon(ctx context.Context, now, until time.Time, limit int) ([]*domain.ReturnRequest, error)
	ShippingCounts(ctx context.Context) (missing, overdue, inTransit, disputed int64, err error)
}

// ReturnDestinationGateway reads a shop's verified return destination
// (Vendor); nil when none is verified.
type ReturnDestinationGateway interface {
	ReturnDestination(ctx context.Context, vendorID string) (*domain.ReturnDestination, error)
}

// ReturnParcelGateway is Shipment's way back.
type ReturnParcelGateway interface {
	AuthorizeReturnShipment(ctx context.Context, a adapter.ReturnShipmentAuthorization) (string, error)
	DispatchReturnShipment(ctx context.Context, shipmentID, operationID, carrier, tracking string, at time.Time) error
	ReceiveReturnShipment(ctx context.Context, shipmentID string) error
	ReturnShipmentException(ctx context.Context, shipmentID, reason string) error
}

const (
	notifyReturnShippingInstructions = "return_shipping_instructions"
	// PW-009: the buyer is reminded once, returnDispatchReminder before the
	// deadline, if the parcel was not reported sent.
	notifyReturnDispatchReminder = "return_dispatch_reminder"
	returnDispatchReminder       = 48 * time.Hour
)

// returnShippingOn: new authorizations need the flag and the wiring;
// returns already authorized keep going after the flag is turned off.
func (uc *OrderUseCase) returnShippingOn() bool {
	return uc.ReturnShippingEnabled && uc.ReturnShipping != nil && uc.ReturnDestinations != nil && uc.ReturnParcels != nil
}

func returnError(err error) error {
	switch {
	case errors.Is(err, repository.ErrReturnRequestNotFound):
		return apperror.NotFound("Return request not found")
	case errors.Is(err, repository.ErrStaleState):
		return domain.ReturnChanged()
	}
	return asError(err)
}

// ReturnShippingTerms are the admin's choices for the way back.
type ReturnShippingTerms struct {
	// FeePayer: seller (a seller fault, the default) or buyer (a change of
	// mind under the policy the order was sold with).
	FeePayer string
	// FeeCap: the approved reimbursement cap when the seller pays.
	FeeCap *int64
}

func (t ReturnShippingTerms) normalized() (ReturnShippingTerms, error) {
	switch t.FeePayer {
	case "":
		t.FeePayer = domain.FeePayerSeller
	case domain.FeePayerBuyer, domain.FeePayerSeller:
	default:
		return t, apperror.Validation("fee_payer must be buyer or seller")
	}
	if t.FeeCap != nil && (*t.FeeCap < 0 || t.FeePayer != domain.FeePayerSeller) {
		return t, apperror.Validation("fee_cap is a non-negative amount, only when the seller pays")
	}
	return t, nil
}

func (uc *OrderUseCase) dispatchDays() int {
	if uc.ReturnDispatchDays > 0 {
		return uc.ReturnDispatchDays
	}
	return domain.DefaultReturnDispatchDays
}

// AdminDecideReturnWithTerms approves or rejects a return. With return
// shipping on, an approval also authorizes the way back: the shop's
// verified destination is snapshotted (or the return waits for one), the
// deadline starts and Shipment opens the parcel.
func (uc *OrderUseCase) AdminDecideReturnWithTerms(ctx context.Context, adminID, returnID string, approve bool, note string,
	terms ReturnShippingTerms) (*domain.ReturnRequest, error) {
	if !approve || !uc.returnShippingOn() {
		return uc.AdminDecideReturn(ctx, adminID, returnID, approve, note)
	}
	terms, err := terms.normalized()
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	decisionNote, err := domain.ValidateNote(note, 1000, false, "A note")
	if err != nil {
		return nil, err
	}
	rr, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil {
		return nil, returnError(err)
	}
	destination, err := uc.returnDestinationOf(ctx, rr.ID)
	if err != nil {
		return nil, err
	}
	var result *domain.ReturnRequest
	err = uc.withOrder(ctx, rr.OrderID, func(ctx context.Context) error {
		current, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return err
		}
		if err := uc.stepReturn(ctx, current, domain.ReturnApproved, "admin", &adminID, "approved", decisionNote,
			repository.ReturnUpdate{DecisionNote: decisionNote, DecidedBy: &adminID}); err != nil {
			return err
		}
		result = current
		return uc.authorizeReturnShipping(ctx, current, destination, terms, adminID, nil)
	})
	if err != nil {
		return nil, returnError(err)
	}
	uc.runEffectsSoon(ctx, rr.OrderID)
	return result, nil
}

func (uc *OrderUseCase) returnDestinationOf(ctx context.Context, returnID string) (*domain.ReturnDestination, error) {
	_, vendorID, err := uc.Returns.VendorOf(ctx, returnID)
	if err != nil {
		return nil, returnError(err)
	}
	return uc.ReturnDestinations.ReturnDestination(ctx, vendorID)
}

// authorizeReturnShipping snapshots the destination on an approved return
// and asks Shipment for the parcel, in the caller's transaction. Without a
// verified destination the return waits (destination_missing) for an
// admin; nothing is taken from the shop's current address silently.
func (uc *OrderUseCase) authorizeReturnShipping(ctx context.Context, rr *domain.ReturnRequest, destination *domain.ReturnDestination,
	terms ReturnShippingTerms, adminID string, reason *string) error {
	if destination == nil {
		missing := domain.ShippingDestinationMissing
		rr.ShippingStatus = &missing
		if err := uc.ReturnShipping.SaveShipping(ctx, rr); err != nil {
			return err
		}
		uc.Log.Warn().Str("return_id", rr.ID).Msg("order_return_destination_missing")
		return uc.Returns.AddEvent(ctx, &domain.ReturnEvent{ReturnID: rr.ID, ActorUserID: &adminID, ActorRole: "admin",
			Action: "shipping_destination_missing", ToStatus: string(rr.Status), Note: reason})
	}
	now := uc.Now().UTC()
	deadline := now.Add(time.Duration(uc.dispatchDays()) * 24 * time.Hour)
	waiting := domain.ShippingAwaitingDispatch
	rr.AuthorizationVersion++
	rr.AuthorizedAt, rr.AuthorizedBy, rr.Destination = &now, &adminID, destination
	rr.FeePayer, rr.FeeCap, rr.DispatchDeadline, rr.ShippingStatus, rr.DispatchOverdueAt = &terms.FeePayer, terms.FeeCap, &deadline, &waiting, nil
	if err := uc.ReturnShipping.SaveShipping(ctx, rr); err != nil {
		return err
	}
	if err := uc.Returns.AddEvent(ctx, &domain.ReturnEvent{ReturnID: rr.ID, ActorUserID: &adminID, ActorRole: "admin",
		Action: "shipping_authorized", ToStatus: string(rr.Status), Note: reason}); err != nil {
		return err
	}
	if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: rr.OrderID, Kind: domain.EffectAuthorizeReturnShipment,
		Target: rr.ID + ":" + strconv.Itoa(rr.AuthorizationVersion)}); err != nil {
		return err
	}
	notice := domain.NewNotifyEffect(rr.OrderID, rr.BuyerID, notifyReturnShippingInstructions)
	notice.Target = notifyReturnShippingInstructions + ":" + rr.ID + ":" + strconv.Itoa(rr.AuthorizationVersion)
	if err := uc.Effects.Enqueue(ctx, notice); err != nil {
		return err
	}
	uc.Log.Info().Str("return_id", rr.ID).Int("authorization_version", rr.AuthorizationVersion).Str("fee_payer", terms.FeePayer).
		Msg("order_return_shipping_authorized")
	return nil
}

// AuthorizeReturnShippingInput is an admin authorizing (or correcting,
// before dispatch) a return's way back.
type AuthorizeReturnShippingInput struct {
	Terms           ReturnShippingTerms
	Reason          string
	ExpectedVersion int64
}

// AuthorizeReturnShipping gives an approved return its instructions once
// the shop has a verified destination (returns approved before AF-05, or
// waiting for a destination), or points a parcel not sent yet at the
// shop's newly verified destination. Audited; after dispatch 409.
func (uc *OrderUseCase) AuthorizeReturnShipping(ctx context.Context, adminID, returnID string, in AuthorizeReturnShippingInput) (*domain.ReturnRequest, error) {
	if !uc.returnShippingOn() {
		return nil, domain.ReturnShippingDisabled()
	}
	terms, err := in.Terms.normalized()
	if err != nil {
		return nil, err
	}
	reason, err := adminReason(in.Reason)
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	rr, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil {
		return nil, returnError(err)
	}
	destination, err := uc.returnDestinationOf(ctx, rr.ID)
	if err != nil {
		return nil, err
	}
	if destination == nil {
		return nil, domain.NoReturnDestination()
	}
	var result *domain.ReturnRequest
	err = uc.withOrder(ctx, rr.OrderID, func(ctx context.Context) error {
		current, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return err
		}
		if current.Version != in.ExpectedVersion {
			return domain.ReturnChanged()
		}
		if current.Status != domain.ReturnApproved {
			return domain.ReturnNotApproved()
		}
		if current.Dispatched() {
			return domain.DestinationChanged()
		}
		previous := current.Destination
		if err := uc.authorizeReturnShipping(ctx, current, destination, terms, adminID, reason); err != nil {
			return err
		}
		result = current
		changes := map[string]any{"authorization_version": current.AuthorizationVersion, "fee_payer": terms.FeePayer}
		if previous != nil {
			changes["destination_version"] = domain.Change(previous.DestinationVersion, destination.DestinationVersion)
		}
		return uc.audit(ctx, domain.AdminAction{ActorID: adminID, Action: "return_shipping_authorized", EntityType: domain.AuditReturn,
			EntityID: current.ID, OrderID: &current.OrderID, Reason: reason, Changes: changes})
	})
	if err != nil {
		return nil, returnError(err)
	}
	uc.runEffectsSoon(ctx, rr.OrderID)
	return result, nil
}

// ShippingInstructions is what the buyer needs to send the parcel.
type ShippingInstructions struct {
	Return       *domain.ReturnRequest
	ReturnCode   string
	Instructions string
}

// GetShippingInstructions: the buyer's own authorized return only.
func (uc *OrderUseCase) GetShippingInstructions(ctx context.Context, buyerID, returnID string) (*ShippingInstructions, error) {
	rr, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil || rr.BuyerID != buyerID {
		return nil, apperror.NotFound("Return request not found")
	}
	if !rr.Authorized() {
		return nil, domain.ReturnNotApproved()
	}
	payer := "Người bán chịu phí gửi trả; giữ biên lai gửi hàng."
	if rr.FeePayer != nil && *rr.FeePayer == domain.FeePayerBuyer {
		payer = "Bạn chịu phí gửi trả theo chính sách áp dụng cho đơn này."
	}
	return &ShippingInstructions{Return: rr, ReturnCode: domain.ReturnCode(rr.ID),
		Instructions: "Đóng gói sản phẩm cùng phụ kiện, ghi mã " + domain.ReturnCode(rr.ID) + " bên ngoài kiện và gửi tới địa chỉ nhận trả " +
			"trong thời gian tiếp nhận của shop. " + payer + " Sau khi gửi, nhập tên đơn vị vận chuyển và mã vận đơn."}, nil
}

// ReturnDispatchInput is the buyer's report that the parcel left.
type ReturnDispatchInput struct {
	CarrierName     string
	TrackingNumber  string
	DispatchedAt    time.Time
	ExpectedVersion int64
	IdempotencyKey  string
}

// ReportReturnDispatch records the buyer's carrier and tracking number
// (Idempotency-Key required) and forwards them to Shipment. A late parcel
// is still accepted: the deadline puts the return in review, it never
// takes the refund away by itself.
func (uc *OrderUseCase) ReportReturnDispatch(ctx context.Context, buyerID, returnID string, in ReturnDispatchInput) (*domain.ReturnRequest, bool, error) {
	if uc.ReturnShipping == nil {
		return nil, false, domain.ReturnShippingDisabled()
	}
	if in.IdempotencyKey == "" {
		return nil, false, apperror.Validation("Idempotency-Key is required")
	}
	if err := validSupportKey(in.IdempotencyKey); err != nil {
		return nil, false, err
	}
	first, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil || first.BuyerID != buyerID {
		return nil, false, apperror.NotFound("Return request not found")
	}
	carrier, tracking, err := domain.ValidateDispatch(in.CarrierName, in.TrackingNumber, in.DispatchedAt, uc.Now(), first.CreatedAt)
	if err != nil {
		return nil, false, err
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{carrier, tracking, in.DispatchedAt.UTC().Format(time.RFC3339)}, "\n")))
	hash := hex.EncodeToString(sum[:])
	var result *domain.ReturnRequest
	replayed := false
	err = uc.withOrder(ctx, first.OrderID, func(ctx context.Context) error {
		rr, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return err
		}
		if rr.DispatchKey != nil && *rr.DispatchKey == in.IdempotencyKey {
			if rr.DispatchHash == nil || *rr.DispatchHash != hash {
				return domain.SupportKeyReused()
			}
			result, replayed = rr, true
			return nil
		}
		if rr.Version != in.ExpectedVersion {
			return domain.ReturnChanged()
		}
		if rr.Status != domain.ReturnApproved || !rr.Authorized() {
			return domain.ReturnNotApproved()
		}
		if rr.Dispatched() {
			return apperror.Conflict("The parcel was already reported sent")
		}
		at, sent := in.DispatchedAt.UTC(), domain.ShippingAwaitingVerification
		rr.DispatchCarrier, rr.DispatchTracking, rr.DispatchedAt, rr.ShippingStatus = &carrier, &tracking, &at, &sent
		rr.DispatchKey, rr.DispatchHash = &in.IdempotencyKey, &hash
		if err := uc.ReturnShipping.SaveShipping(ctx, rr); err != nil {
			return err
		}
		note := carrier + " " + tracking
		if err := uc.Returns.AddEvent(ctx, &domain.ReturnEvent{ReturnID: rr.ID, ActorUserID: &buyerID, ActorRole: "buyer",
			Action: "dispatched", ToStatus: string(rr.Status), Note: &note}); err != nil {
			return err
		}
		result = rr
		if err := uc.noticeVendorOfReturn(ctx, rr, events.VendorActionReturnDispatched); err != nil {
			return err
		}
		return uc.Effects.Enqueue(ctx, domain.Effect{OrderID: rr.OrderID, Kind: domain.EffectDispatchReturnShipment, Target: rr.ID})
	})
	if err != nil {
		return nil, false, returnError(err)
	}
	if !replayed {
		uc.Log.Info().Str("return_id", returnID).Bool("late", result.DispatchOverdueAt != nil).Msg("order_return_dispatched")
		uc.runEffectsSoon(ctx, result.OrderID)
	}
	return result, replayed, nil
}

// ReturnReceiptInput is what the shop (or an admin) received.
type ReturnReceiptInput struct {
	Sellable, Damaged, Missing int64
	Note                       string
	ExpectedVersion            int64
}

// RecordReturnReceipt records the goods that came back: sellable units go
// back to stock; when everything is sellable the refund is requested as
// before, otherwise the return waits for an admin (no silent deduction,
// disputes go through a support case). One receipt per return: two people
// recording at once, one wins.
func (uc *OrderUseCase) RecordReturnReceipt(ctx context.Context, actor SupportActor, returnID string, in ReturnReceiptInput) (*domain.ReturnRequest, error) {
	if uc.ReturnShipping == nil {
		return nil, domain.ReturnShippingDisabled()
	}
	note, err := domain.ValidateNote(in.Note, 1000, false, "A note")
	if err != nil {
		return nil, err
	}
	var orderID string
	switch actor.Role {
	case "vendor":
		if orderID, err = uc.authorizeVendorForReturn(ctx, actor.ID, returnID); err != nil {
			return nil, err
		}
	case "admin":
		if err := uc.requireAdmin(ctx, actor.ID); err != nil {
			return nil, err
		}
		rr, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return nil, returnError(err)
		}
		orderID = rr.OrderID
	default:
		return nil, apperror.Forbidden("Only the shop or an admin can receive a return")
	}
	var result *domain.ReturnRequest
	err = uc.withOrder(ctx, orderID, func(ctx context.Context) error {
		rr, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return err
		}
		if rr.Version != in.ExpectedVersion {
			return domain.ReturnChanged()
		}
		if rr.Status != domain.ReturnApproved {
			return apperror.Conflict("Only an approved return can be received")
		}
		if err := domain.ValidateReturnReceipt(in.Sellable, in.Damaged, in.Missing, rr.Quantity); err != nil {
			return err
		}
		g := &domain.ReturnReceipt{ReturnID: rr.ID, Version: 1, RecordedBy: actor.ID, ActorRole: actor.Role, Sellable: in.Sellable,
			Damaged: in.Damaged, Missing: in.Missing, Note: note}
		if err := uc.ReturnShipping.AddReceipt(ctx, g); err != nil {
			return err
		}
		disputed, restock := g.Disputed(), in.Sellable > 0
		u := repository.ReturnUpdate{ReceivedBy: &actor.ID, InspectionNote: note, Restock: &restock, RestockQuantity: &in.Sellable,
			InspectionDisputed: &disputed}
		if rr.ShippingStatus != nil {
			received := domain.ShippingReceived
			u.ShippingStatus = &received
		}
		if err := uc.stepReturn(ctx, rr, domain.ReturnReceived, actor.Role, &actor.ID, "received", note, u); err != nil {
			return err
		}
		if restock {
			if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: rr.OrderID, Kind: domain.EffectRestockReturn, Target: rr.ID}); err != nil {
				return err
			}
		}
		if rr.Authorized() {
			if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: rr.OrderID, Kind: domain.EffectCloseReturnShipment, Target: rr.ID}); err != nil {
				return err
			}
		}
		result = rr
		if disputed {
			why := "Damaged " + strconv.FormatInt(in.Damaged, 10) + ", missing " + strconv.FormatInt(in.Missing, 10) + ": the refund waits for an admin"
			return uc.Returns.AddEvent(ctx, &domain.ReturnEvent{ReturnID: rr.ID, ActorRole: "system", Action: "inspection_disputed",
				ToStatus: string(rr.Status), Note: &why})
		}
		return uc.requestReturnRefund(ctx, rr, actor.ID)
	})
	if err != nil {
		return nil, returnError(err)
	}
	uc.Log.Info().Str("return_id", returnID).Str("actor_role", actor.Role).Int64("sellable", in.Sellable).Int64("damaged", in.Damaged).
		Int64("missing", in.Missing).Msg("order_return_goods_received")
	uc.runEffectsSoon(ctx, orderID)
	return result, nil
}

// ReturnShippingDecision is an admin acting on a return's way back.
type ReturnShippingDecision struct {
	// Action: refund (a disputed inspection: refund in full anyway) or
	// mark_lost (the parcel never reached the shop; refund then goes
	// through a support case).
	Action          string
	Reason          string
	ExpectedVersion int64
}

// DecideReturnShipping settles what the inspection or the parcel left open.
func (uc *OrderUseCase) DecideReturnShipping(ctx context.Context, adminID, returnID string, in ReturnShippingDecision) (*domain.ReturnRequest, error) {
	reason, err := adminReason(in.Reason)
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	first, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil {
		return nil, returnError(err)
	}
	var result *domain.ReturnRequest
	err = uc.withOrder(ctx, first.OrderID, func(ctx context.Context) error {
		rr, err := uc.Returns.FindByID(ctx, returnID)
		if err != nil {
			return err
		}
		if rr.Version != in.ExpectedVersion {
			return domain.ReturnChanged()
		}
		switch in.Action {
		case "refund":
			if rr.Status != domain.ReturnReceived || !rr.InspectionDisputed {
				return apperror.Conflict("Only a received return with damaged or missing goods waits for this decision")
			}
			if err := uc.requestReturnRefund(ctx, rr, adminID); err != nil {
				return err
			}
		case "mark_lost":
			if rr.Status != domain.ReturnApproved || rr.ShippingStatus == nil || *rr.ShippingStatus != domain.ShippingAwaitingVerification {
				return apperror.Conflict("Only a parcel reported sent and not received can be marked lost")
			}
			lost := domain.ShippingLost
			rr.ShippingStatus = &lost
			if err := uc.ReturnShipping.SaveShipping(ctx, rr); err != nil {
				return err
			}
			if err := uc.Returns.AddEvent(ctx, &domain.ReturnEvent{ReturnID: rr.ID, ActorUserID: &adminID, ActorRole: "admin",
				Action: "parcel_lost", ToStatus: string(rr.Status), Note: reason}); err != nil {
				return err
			}
			if err := uc.Effects.Enqueue(ctx, domain.Effect{OrderID: rr.OrderID, Kind: domain.EffectCloseReturnShipment, Target: rr.ID}); err != nil {
				return err
			}
		default:
			return apperror.Validation("action must be refund or mark_lost")
		}
		result = rr
		return uc.audit(ctx, domain.AdminAction{ActorID: adminID, Action: "return_shipping_" + in.Action, EntityType: domain.AuditReturn,
			EntityID: rr.ID, OrderID: &rr.OrderID, Reason: reason, Changes: map[string]any{"status": string(rr.Status)}})
	})
	if err != nil {
		return nil, returnError(err)
	}
	uc.Log.Info().Str("return_id", returnID).Str("action", in.Action).Msg("order_return_shipping_decided")
	uc.runEffectsSoon(ctx, first.OrderID)
	return result, nil
}

// ReturnReceipt is the return's receipt for its buyer, shop or an admin.
func (uc *OrderUseCase) ReturnReceipt(ctx context.Context, returnID string) (*domain.ReturnReceipt, error) {
	if uc.ReturnShipping == nil {
		return nil, nil
	}
	g, err := uc.ReturnShipping.LatestReceipt(ctx, returnID)
	return g, asError(err)
}

// VendorReturnAccess checks the caller may read a return as its shop.
func (uc *OrderUseCase) VendorReturnAccess(ctx context.Context, userID, returnID string) (*domain.ReturnRequest, error) {
	rr, err := uc.Returns.FindByID(ctx, returnID)
	if err != nil {
		return nil, returnError(err)
	}
	_, vendorID, err := uc.Returns.VendorOf(ctx, returnID)
	if err != nil {
		return nil, appError(err)
	}
	if _, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vendorID, shopaccess.ReturnsHandle); err != nil {
		if shopaccess.IsUnavailable(err) {
			return nil, err
		}
		return nil, apperror.NotFound("Return request not found")
	}
	return rr, nil
}

// authorizeReturnShipment is the parcel effect: Shipment opens (or
// re-points) the parcel for the authorization the target names.
func (uc *OrderUseCase) authorizeReturnShipment(ctx context.Context, e *domain.Effect) error {
	if uc.ReturnParcels == nil || uc.ReturnShipping == nil {
		return errors.New("return shipping is not wired")
	}
	id, version, _ := strings.Cut(e.Target, ":")
	rr, err := uc.Returns.FindByID(ctx, id)
	if err != nil {
		return appError(err)
	}
	if strconv.Itoa(rr.AuthorizationVersion) != version || rr.Destination == nil {
		return nil // a newer authorization has its own effect
	}
	_, vendorID, err := uc.Returns.VendorOf(ctx, rr.ID)
	if err != nil {
		return appError(err)
	}
	d := rr.Destination
	shipmentID, err := uc.ReturnParcels.AuthorizeReturnShipment(ctx, adapter.ReturnShipmentAuthorization{
		OperationID: "return:" + rr.ID + ":auth:" + version, ReturnID: rr.ID, OrderID: rr.OrderID, VendorID: vendorID, BuyerID: rr.BuyerID,
		AuthorizationVersion: rr.AuthorizationVersion, ReceivingHours: d.ReceivingHours,
		Destination: domain.Destination{RecipientName: d.RecipientName, Phone: d.Phone, Province: d.Province, District: d.District,
			Ward: d.Ward, StreetAddress: d.StreetAddress}})
	if err != nil {
		return err
	}
	return uc.ReturnShipping.SetReturnShipment(ctx, rr.ID, shipmentID)
}

// dispatchReturnShipment forwards the buyer's dispatch to the parcel.
func (uc *OrderUseCase) dispatchReturnShipment(ctx context.Context, e *domain.Effect) error {
	if uc.ReturnParcels == nil {
		return errors.New("return shipping is not wired")
	}
	rr, err := uc.Returns.FindByID(ctx, e.Target)
	if err != nil {
		return appError(err)
	}
	if rr.DispatchTracking == nil || rr.DispatchedAt == nil {
		return nil
	}
	if rr.ReturnShipmentID == nil {
		return apperror.Internal(errors.New("the return parcel is not open yet"))
	}
	return uc.ReturnParcels.DispatchReturnShipment(ctx, *rr.ReturnShipmentID, "return_dispatch:"+rr.ID, *rr.DispatchCarrier, *rr.DispatchTracking, *rr.DispatchedAt)
}

// closeReturnShipment closes the parcel: received, or lost on the way.
func (uc *OrderUseCase) closeReturnShipment(ctx context.Context, e *domain.Effect) error {
	if uc.ReturnParcels == nil {
		return errors.New("return shipping is not wired")
	}
	rr, err := uc.Returns.FindByID(ctx, e.Target)
	if err != nil {
		return appError(err)
	}
	if rr.ReturnShipmentID == nil {
		if rr.Authorized() {
			return apperror.Internal(errors.New("the return parcel is not open yet"))
		}
		return nil
	}
	if rr.ShippingStatus != nil && *rr.ShippingStatus == domain.ShippingLost {
		return uc.ReturnParcels.ReturnShipmentException(ctx, *rr.ReturnShipmentID, "Marked lost on the way back by an admin")
	}
	return uc.ReturnParcels.ReceiveReturnShipment(ctx, *rr.ReturnShipmentID)
}

// FlagOverdueReturns puts returns whose parcel is late in review (once
// each); the buyer may still send it and keeps the right to the refund.
func (uc *OrderUseCase) FlagOverdueReturns(ctx context.Context) {
	if uc.ReturnShipping == nil {
		return
	}
	late, err := uc.ReturnShipping.ListDispatchOverdue(ctx, uc.Now().UTC(), 100)
	if err != nil {
		if ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("order_return_overdue_scan_failed")
		}
		return
	}
	for _, found := range late {
		err := uc.withOrder(ctx, found.OrderID, func(ctx context.Context) error {
			rr, err := uc.Returns.FindByID(ctx, found.ID)
			if err != nil || rr.Status != domain.ReturnApproved || rr.ShippingStatus == nil ||
				*rr.ShippingStatus != domain.ShippingAwaitingDispatch || rr.DispatchOverdueAt != nil {
				return err
			}
			now := uc.Now().UTC()
			rr.DispatchOverdueAt = &now
			if err := uc.ReturnShipping.SaveShipping(ctx, rr); err != nil {
				return err
			}
			note := "The parcel was not reported sent by the deadline; the buyer keeps the right to the refund"
			return uc.Returns.AddEvent(ctx, &domain.ReturnEvent{ReturnID: rr.ID, ActorRole: "system", Action: "dispatch_overdue",
				ToStatus: string(rr.Status), Note: &note})
		})
		if err != nil {
			if ctx.Err() == nil {
				uc.Log.Error().Err(err).Str("return_id", found.ID).Msg("order_return_overdue_flag_failed")
			}
			continue
		}
		uc.Log.Warn().Str("return_id", found.ID).Msg("order_return_dispatch_overdue")
	}
}

// RemindReturnDispatch queues one reminder per return whose dispatch
// deadline is near and whose parcel was not reported sent (PW-009). The
// notify effect's target makes a second reminder impossible.
func (uc *OrderUseCase) RemindReturnDispatch(ctx context.Context) {
	if uc.ReturnShipping == nil {
		return
	}
	now := uc.Now().UTC()
	due, err := uc.ReturnShipping.ListDispatchDueSoon(ctx, now, now.Add(returnDispatchReminder), 100)
	if err != nil {
		if ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("order_return_reminder_scan_failed")
		}
		return
	}
	for _, found := range due {
		err := uc.withOrder(ctx, found.OrderID, func(ctx context.Context) error {
			rr, err := uc.Returns.FindByID(ctx, found.ID)
			if err != nil || rr.Status != domain.ReturnApproved || rr.ShippingStatus == nil || *rr.ShippingStatus != domain.ShippingAwaitingDispatch {
				return err
			}
			notice := domain.NewNotifyEffect(rr.OrderID, rr.BuyerID, notifyReturnDispatchReminder)
			notice.Target = notifyReturnDispatchReminder + ":" + rr.ID
			return uc.Effects.Enqueue(ctx, notice)
		})
		if err != nil {
			if ctx.Err() == nil {
				uc.Log.Error().Err(err).Str("return_id", found.ID).Msg("order_return_reminder_failed")
			}
			continue
		}
		uc.Log.Info().Str("return_id", found.ID).Msg("order_return_dispatch_reminded")
	}
}

// reportReturnShipping logs returns waiting on someone.
func (uc *OrderUseCase) reportReturnShipping(ctx context.Context) {
	if uc.ReturnShipping == nil {
		return
	}
	missing, overdue, inTransit, disputed, err := uc.ReturnShipping.ShippingCounts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("order_return_shipping_report_failed")
		}
		return
	}
	if missing+overdue+inTransit+disputed == 0 {
		return
	}
	levelFor(uc.Log, missing > 0 || overdue > 0 || disputed > 0).Int64("destination_missing", missing).Int64("dispatch_overdue", overdue).
		Int64("in_transit", inTransit).Int64("inspection_disputed", disputed).Msg("order_return_shipping_backlog")
}
