package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// SupportConfig is the rollout switch of support cases (AF-01). Turning it
// off stops new cases only: existing cases stay readable, answerable and
// resolvable.
type SupportConfig struct {
	Enabled bool
	// PilotVendorIDs, when not empty, limits new cases to these shops.
	PilotVendorIDs map[string]bool
	// AttachmentRetention is how long evidence is kept after a case closes.
	AttachmentRetention time.Duration
}

// SupportActor is the authenticated caller; the role comes from the
// verified token, never from the request body.
type SupportActor struct {
	ID   string
	Role string
}

// SupportCapability tells the frontend whether to offer support requests.
type SupportCapability struct {
	Enabled             bool
	AttachmentsEnabled  bool
	MaxAttachments      int
	MaxAttachmentBytes  int64
	MaxMessageChars     int
	ReopenWindowDays    int
	PollIntervalSeconds int
	PilotOnly           bool
}

func (uc *OrderUseCase) SupportCapability() SupportCapability {
	p := uc.SupportPolicy
	return SupportCapability{Enabled: uc.SupportConfig.Enabled, AttachmentsEnabled: uc.Attachments != nil, MaxAttachments: p.MaxAttachments,
		MaxAttachmentBytes: p.MaxAttachmentBytes, MaxMessageChars: p.MaxMessageChars, ReopenWindowDays: int(p.ReopenWindow / (24 * time.Hour)),
		PollIntervalSeconds: 15, PilotOnly: len(uc.SupportConfig.PilotVendorIDs) > 0}
}

// CreateSupportCaseInput is a buyer's new case on one vendor order.
type CreateSupportCaseInput struct {
	OrderID        string
	VendorOrderID  string
	Category       string
	Message        string
	AttachmentIDs  []string
	RelatedCaseID  string
	IdempotencyKey string
}

// SupportMessageInput is a new message; Visibility is only read for admins.
type SupportMessageInput struct {
	Text           string
	AttachmentIDs  []string
	Visibility     string
	IdempotencyKey string
}

// SupportCaseDetail is a case as its caller may see it.
type SupportCaseDetail struct {
	Case     *domain.SupportCase
	Messages []*domain.SupportMessage
	Events   []*domain.SupportCaseEvent
}

// SupportCasePage is one page of a case list with the cursor of the next.
type SupportCasePage struct {
	Items      []*domain.SupportCase
	NextCursor string
}

// CreateSupportCase opens a case for the buyer's own order. The order lock
// serializes it with other cases and with refunds/returns of the order; the
// case, its first message, the timeline entry and the buyer notification
// are written in one transaction.
func (uc *OrderUseCase) CreateSupportCase(ctx context.Context, buyerID string, in CreateSupportCaseInput) (*domain.SupportCase, bool, error) {
	if uc.Support == nil {
		return nil, false, domain.SupportDisabled()
	}
	category, err := domain.ParseSupportCategory(in.Category)
	if err != nil {
		return nil, false, err
	}
	text, err := uc.SupportPolicy.ValidateSupportText(in.Message)
	if err != nil {
		return nil, false, err
	}
	attachmentIDs, err := uc.validAttachmentIDs(in.AttachmentIDs)
	if err != nil {
		return nil, false, err
	}
	if err := validSupportKey(in.IdempotencyKey); err != nil {
		return nil, false, err
	}
	hash := requestHash(in.OrderID, in.VendorOrderID, string(category), text, in.RelatedCaseID, strings.Join(attachmentIDs, ","))

	var result *domain.SupportCase
	replayed := false
	err = uc.withOrder(ctx, in.OrderID, func(ctx context.Context) error {
		if in.IdempotencyKey != "" {
			existing, err := uc.Support.FindByIdempotencyKey(ctx, buyerID, in.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing != nil {
				if existing.RequestHash == nil || *existing.RequestHash != hash {
					return domain.SupportKeyReused()
				}
				result, replayed = existing, true
				return nil
			}
		}
		order, err := uc.findOrder(ctx, in.OrderID)
		if err != nil {
			return err
		}
		if order.BuyerID != buyerID {
			return apperror.NotFound("Order not found")
		}
		vo, err := uc.VendorOrders.FindByID(ctx, in.VendorOrderID)
		if err != nil || vo.OrderID != order.ID {
			return apperror.NotFound("Vendor order not found in this order")
		}
		if !uc.SupportConfig.Enabled || (len(uc.SupportConfig.PilotVendorIDs) > 0 && !uc.SupportConfig.PilotVendorIDs[vo.VendorID]) {
			return domain.SupportDisabled()
		}
		if err := domain.CheckCaseEligibility(category, vo.Status); err != nil {
			return err
		}
		now := uc.Now()
		existing, err := uc.Support.FindNotClosed(ctx, buyerID, vo.ID, category)
		if err != nil {
			return err
		}
		if existing != nil {
			if !uc.SupportPolicy.ReopenExpired(existing, now) {
				return domain.CaseAlreadyOpen()
			}
			// Past its reopen window: close it so the new case can link to it.
			if err := uc.moveCase(ctx, existing, domain.CaseClosed, "system", nil, "auto_closed", nil); err != nil {
				return err
			}
			if in.RelatedCaseID == "" {
				in.RelatedCaseID = existing.ID
			}
		}
		if err := uc.checkUploads(ctx, buyerID, attachmentIDs); err != nil {
			return err
		}
		c := &domain.SupportCase{OrderID: order.ID, VendorOrderID: vo.ID, VendorID: vo.VendorID, BuyerID: buyerID, Category: category,
			Status: domain.CaseOpen, PolicyVersion: uc.SupportPolicy.Version, DueAt: uc.SupportPolicy.DueAt(domain.CaseOpen, now),
			FinancialHold: category.AffectsMoney(), RequestHash: &hash}
		if in.RelatedCaseID != "" {
			related, err := uc.Support.FindByID(ctx, in.RelatedCaseID)
			if err != nil || related.BuyerID != buyerID || related.OrderID != order.ID {
				return apperror.NotFound("Related case not found")
			}
			if related.Status != domain.CaseClosed {
				return apperror.Conflict("Only a closed case can be linked; reopen it instead")
			}
			c.RelatedCaseID = &related.ID
		}
		if in.IdempotencyKey != "" {
			c.IdempotencyKey = &in.IdempotencyKey
		}
		if err := uc.Support.Create(ctx, c); err != nil {
			if errors.Is(err, repository.ErrSupportKeyTaken) {
				return domain.SupportKeyReused()
			}
			return err
		}
		if _, err := uc.addMessage(ctx, c, SupportActor{ID: buyerID, Role: "buyer"}, text, domain.VisibilityPublic, attachmentIDs, nil, nil); err != nil {
			return err
		}
		if err := uc.Support.AddEvent(ctx, &domain.SupportCaseEvent{CaseID: c.ID, ActorID: &buyerID, ActorRole: "buyer", Action: "opened",
			ToStatus: string(c.Status)}); err != nil {
			return err
		}
		notice := domain.NewNotifyEffect(order.ID, buyerID, notifySupportCaseOpened)
		notice.Target = notifySupportCaseOpened + ":" + c.ID
		if err := uc.Effects.Enqueue(ctx, notice); err != nil {
			return err
		}
		result = c
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if !replayed {
		uc.Log.Info().Str("order_id", result.OrderID).Str("case_id", result.ID).Str("category", string(result.Category)).Msg("order_support_case_opened")
		uc.runEffectsSoon(ctx, result.OrderID)
	}
	return result, replayed, nil
}

// PostSupportMessage appends a message to a case the caller may access.
// A buyer or vendor answer to a case waiting for them puts it back in
// progress. Buyers and vendors only write public messages.
func (uc *OrderUseCase) PostSupportMessage(ctx context.Context, actor SupportActor, caseID string, in SupportMessageInput) (*domain.SupportMessage, bool, error) {
	if uc.Support == nil {
		return nil, false, apperror.NotFound("Support case not found")
	}
	text, err := uc.SupportPolicy.ValidateSupportText(in.Text)
	if err != nil {
		return nil, false, err
	}
	attachmentIDs, err := uc.validAttachmentIDs(in.AttachmentIDs)
	if err != nil {
		return nil, false, err
	}
	if err := validSupportKey(in.IdempotencyKey); err != nil {
		return nil, false, err
	}
	visibility := domain.VisibilityPublic
	if actor.Role == "admin" {
		if err := uc.requireAdmin(ctx, actor.ID); err != nil {
			return nil, false, err
		}
		switch in.Visibility {
		case "", domain.VisibilityPublic:
		case domain.VisibilityInternal:
			visibility = domain.VisibilityInternal
		default:
			return nil, false, apperror.Validation("visibility must be public or internal")
		}
	}
	hash := requestHash(caseID, visibility, text, strings.Join(attachmentIDs, ","))
	c, err := uc.supportCaseFor(ctx, actor, caseID)
	if err != nil {
		return nil, false, err
	}

	var result *domain.SupportMessage
	replayed := false
	err = uc.withOrder(ctx, c.OrderID, func(ctx context.Context) error {
		if in.IdempotencyKey != "" {
			existing, err := uc.Support.FindMessageByKey(ctx, actor.ID, in.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing != nil {
				if existing.CaseID != caseID || existing.RequestHash == nil || *existing.RequestHash != hash {
					return domain.SupportKeyReused()
				}
				result, replayed = existing, true
				return nil
			}
		}
		c, err := uc.Support.FindByID(ctx, caseID)
		if err != nil {
			return appError(err)
		}
		switch {
		case c.Status == domain.CaseClosed:
			return apperror.Conflict("This case is closed; open a new case linked to it")
		case c.Status == domain.CaseResolved && actor.Role == "buyer":
			return apperror.Conflict("This case is resolved; reopen it to add a message")
		case c.Status == domain.CaseResolved && actor.Role == "vendor":
			return apperror.Conflict("This case is resolved")
		}
		if err := uc.checkUploads(ctx, actor.ID, attachmentIDs); err != nil {
			return err
		}
		var key *string
		if in.IdempotencyKey != "" {
			key = &in.IdempotencyKey
		}
		if result, err = uc.addMessage(ctx, c, actor, text, visibility, attachmentIDs, key, &hash); err != nil {
			return err
		}
		switch {
		case actor.Role == "buyer" && c.Status == domain.CaseWaitingBuyer,
			actor.Role == "vendor" && c.Status == domain.CaseWaitingVendor:
			return uc.moveCase(ctx, c, domain.CaseInProgress, actor.Role, &actor.ID, actor.Role+"_replied", nil)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return result, replayed, nil
}

// checkUploads refuses attachment ids that are not the caller's own
// unused uploads, before anything is written. addMessage re-checks while
// linking, which settles two messages racing for the same upload.
func (uc *OrderUseCase) checkUploads(ctx context.Context, ownerID string, ids []string) error {
	for _, id := range ids {
		a, err := uc.Support.FindAttachment(ctx, id)
		if errors.Is(err, repository.ErrAttachmentNotFound) || (err == nil && (a.OwnerID != ownerID || a.State != domain.AttachmentUploaded)) {
			return apperror.Validation("An attachment was not found or is already used; upload it again")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// addMessage writes a message and links its uploads, which must be the
// author's own and not used yet.
func (uc *OrderUseCase) addMessage(ctx context.Context, c *domain.SupportCase, actor SupportActor, text, visibility string,
	attachmentIDs []string, key, hash *string) (*domain.SupportMessage, error) {
	m := &domain.SupportMessage{CaseID: c.ID, AuthorID: actor.ID, AuthorRole: actor.Role, Visibility: visibility, Text: text,
		IdempotencyKey: key, RequestHash: hash}
	if err := uc.Support.AddMessage(ctx, m); err != nil {
		if errors.Is(err, repository.ErrSupportKeyTaken) {
			return nil, domain.SupportKeyReused()
		}
		return nil, err
	}
	m.Attachments = []*domain.CaseAttachment{}
	if len(attachmentIDs) == 0 {
		return m, nil
	}
	n, err := uc.Support.AttachToMessage(ctx, attachmentIDs, actor.ID, c.ID, m.ID)
	if err != nil {
		return nil, err
	}
	if n != int64(len(attachmentIDs)) {
		return nil, apperror.Validation("An attachment was not found or is already used; upload it again")
	}
	for _, id := range attachmentIDs {
		a, err := uc.Support.FindAttachment(ctx, id)
		if err != nil {
			return nil, err
		}
		m.Attachments = append(m.Attachments, a)
	}
	return m, nil
}

// GetSupportCase returns a case with the messages and timeline the caller
// may see: internal notes, admin ids and history notes are admin-only.
func (uc *OrderUseCase) GetSupportCase(ctx context.Context, actor SupportActor, caseID string) (*SupportCaseDetail, error) {
	c, err := uc.supportCaseFor(ctx, actor, caseID)
	if err != nil {
		return nil, err
	}
	admin := actor.Role == "admin"
	messages, err := uc.Support.ListMessages(ctx, c.ID, admin)
	if err != nil {
		return nil, appError(err)
	}
	events, err := uc.Support.ListEvents(ctx, c.ID)
	if err != nil {
		return nil, appError(err)
	}
	if !admin {
		visible := make([]*domain.SupportCaseEvent, 0, len(events))
		for _, e := range events {
			copied := *e
			copied.Note = nil
			if copied.ActorID != nil && *copied.ActorID != actor.ID {
				copied.ActorID = nil
			}
			visible = append(visible, &copied)
		}
		events = visible
		shown := make([]*domain.SupportMessage, 0, len(messages))
		for _, m := range messages {
			copied := *m
			if copied.AuthorID != actor.ID {
				copied.AuthorID = ""
			}
			copied.IdempotencyKey, copied.RequestHash = nil, nil
			shown = append(shown, &copied)
		}
		messages = shown
	}
	return &SupportCaseDetail{Case: redactCase(c, actor), Messages: messages, Events: events}, nil
}

// redactCase hides what the caller's role may not see: admin ids and
// request fingerprints from buyers and vendors, the buyer from the vendor.
func redactCase(c *domain.SupportCase, actor SupportActor) *domain.SupportCase {
	if actor.Role == "admin" {
		return c
	}
	copied := *c
	copied.AssigneeID, copied.IdempotencyKey, copied.RequestHash = nil, nil, nil
	if actor.Role == "vendor" {
		copied.BuyerID, copied.RelatedCaseID = "", nil
	}
	return &copied
}

// supportCaseFor loads a case and checks the caller may see it. A case of
// another buyer or shop is reported as not found.
func (uc *OrderUseCase) supportCaseFor(ctx context.Context, actor SupportActor, caseID string) (*domain.SupportCase, error) {
	if uc.Support == nil {
		return nil, apperror.NotFound("Support case not found")
	}
	c, err := uc.Support.FindByID(ctx, caseID)
	if err != nil {
		return nil, notFoundOrInternal(err, repository.ErrSupportCaseNotFound, "Support case not found")
	}
	switch actor.Role {
	case "buyer":
		if c.BuyerID != actor.ID {
			return nil, apperror.NotFound("Support case not found")
		}
	case "vendor":
		if _, err := uc.Vendors.GetApprovedVendorID(ctx, actor.ID, c.VendorID); err != nil {
			var app *apperror.Error
			if errors.As(err, &app) && app.Code != apperror.CodeInternal {
				return nil, apperror.NotFound("Support case not found")
			}
			return nil, asError(err)
		}
	case "admin":
	default:
		return nil, apperror.NotFound("Support case not found")
	}
	return c, nil
}

// ListMySupportCases lists the buyer's own cases, newest first.
func (uc *OrderUseCase) ListMySupportCases(ctx context.Context, buyerID, status, cursor string, limit int) (*SupportCasePage, error) {
	return uc.listSupportCases(ctx, repository.SupportCaseFilter{BuyerID: buyerID, Status: status}, cursor, limit, SupportActor{ID: buyerID, Role: "buyer"})
}

// ListVendorSupportCases lists cases on the caller's shop's orders.
func (uc *OrderUseCase) ListVendorSupportCases(ctx context.Context, userID, vendorID, status, cursor string, limit int) (*SupportCasePage, error) {
	vendorID, err := uc.Vendors.GetApprovedVendorID(ctx, userID, vendorID)
	if err != nil {
		return nil, err
	}
	return uc.listSupportCases(ctx, repository.SupportCaseFilter{VendorID: vendorID, Status: status}, cursor, limit, SupportActor{ID: userID, Role: "vendor"})
}

// AdminSupportFilter is the admin queue filter. Assignee "me" is the caller.
type AdminSupportFilter struct {
	Status     string
	AssigneeID string
	Unassigned bool
	Overdue    bool
}

// ListSupportCases is the admin queue.
func (uc *OrderUseCase) ListSupportCases(ctx context.Context, adminID string, f AdminSupportFilter, cursor string, limit int) (*SupportCasePage, error) {
	filter := repository.SupportCaseFilter{Status: f.Status, AssigneeID: f.AssigneeID, Unassigned: f.Unassigned}
	if filter.AssigneeID == "me" {
		filter.AssigneeID = adminID
	}
	if f.Overdue {
		now := uc.Now()
		filter.OverdueAt = &now
	}
	return uc.listSupportCases(ctx, filter, cursor, limit, SupportActor{ID: adminID, Role: "admin"})
}

func (uc *OrderUseCase) listSupportCases(ctx context.Context, f repository.SupportCaseFilter, cursor string, limit int, actor SupportActor) (*SupportCasePage, error) {
	if uc.Support == nil {
		return &SupportCasePage{Items: []*domain.SupportCase{}}, nil
	}
	if f.Status != "" && !slices.Contains(domain.SupportCaseStatuses, domain.SupportCaseStatus(f.Status)) {
		return nil, apperror.Validation("Invalid support case status filter")
	}
	after, err := decodeCaseCursor(cursor)
	if err != nil {
		return nil, err
	}
	items, err := uc.Support.List(ctx, f, after, limit+1)
	if err != nil {
		return nil, appError(err)
	}
	page := &SupportCasePage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCaseCursor(last)
	}
	for i, c := range page.Items {
		page.Items[i] = redactCase(c, actor)
	}
	return page, nil
}

// AssignSupportCase gives a case to an admin. The first assignment moves an
// open case in progress; two admins picking the same version: one wins.
func (uc *OrderUseCase) AssignSupportCase(ctx context.Context, adminID, caseID, assigneeID string, expectedVersion int64, reason string) (*domain.SupportCase, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return nil, apperror.Validation("Assignment reason must be 1-500 characters")
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	if err := uc.Identity.RequireRole(ctx, assigneeID, "admin"); err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Code != apperror.CodeInternal {
			return nil, apperror.Validation("The assignee must be an admin")
		}
		return nil, asError(err)
	}
	return uc.mutateCase(ctx, adminID, caseID, expectedVersion, func(ctx context.Context, c *domain.SupportCase) error {
		if c.Status == domain.CaseResolved || c.Status == domain.CaseClosed {
			return apperror.Conflict("A " + string(c.Status) + " case cannot be reassigned")
		}
		previous := c.AssigneeID
		c.AssigneeID = &assigneeID
		changes := map[string]any{"assignee_id": domain.Change(previous, assigneeID)}
		if c.Status == domain.CaseOpen {
			return uc.moveCaseWith(ctx, c, domain.CaseInProgress, "admin", &adminID, "assigned", &reason, changes)
		}
		return uc.saveCaseStep(ctx, c, c.Status, "admin", &adminID, "assigned", &reason, changes)
	})
}

// ChangeSupportCaseStatus lets an admin ask the buyer or the vendor for
// information, or take the case back in progress.
func (uc *OrderUseCase) ChangeSupportCaseStatus(ctx context.Context, adminID, caseID, status string, expectedVersion int64, note string) (*domain.SupportCase, error) {
	to := domain.SupportCaseStatus(status)
	if to != domain.CaseWaitingBuyer && to != domain.CaseWaitingVendor && to != domain.CaseInProgress {
		return nil, apperror.Validation("status must be waiting_buyer, waiting_vendor or in_progress")
	}
	reason, err := domain.ValidateNote(note, 1000, false, "Note")
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	return uc.mutateCase(ctx, adminID, caseID, expectedVersion, func(ctx context.Context, c *domain.SupportCase) error {
		if c.Status == domain.CaseOpen {
			return apperror.Conflict("Assign the case before changing its status")
		}
		if c.Status == domain.CaseResolutionPending || c.Status == domain.CaseResolved {
			return apperror.Conflict("A " + string(c.Status) + " case changes status only through its resolution")
		}
		return uc.moveCase(ctx, c, to, "admin", &adminID, "status_changed", reason)
	})
}

// ResolveInput is an admin's conclusion. A refund or return must already
// exist (created through the refund/return flow) for this vendor order;
// the case is resolved only once its outcome is confirmed.
type ResolveInput struct {
	Kind              string
	LinkedOperationID string
	Reason            string
	ExpectedVersion   int64
}

// ResolveSupportCase records the conclusion. It returns pending=true when
// the case waits for its refund or return (202 for the client).
func (uc *OrderUseCase) ResolveSupportCase(ctx context.Context, adminID, caseID string, in ResolveInput) (*domain.SupportCase, bool, error) {
	reason, err := adminReason(in.Reason)
	if err != nil {
		return nil, false, err
	}
	switch in.Kind {
	case domain.ResolutionNoAction:
		if in.LinkedOperationID != "" {
			return nil, false, apperror.Validation("linked_operation_id is only for a refund or return resolution")
		}
	case domain.ResolutionRefund, domain.ResolutionReturn:
		if in.LinkedOperationID == "" {
			return nil, false, apperror.Validation("linked_operation_id is required for a refund or return resolution")
		}
	default:
		return nil, false, apperror.Validation("resolution_kind must be no_action, refund or return")
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, false, err
	}
	pending := false
	c, err := uc.mutateCase(ctx, adminID, caseID, in.ExpectedVersion, func(ctx context.Context, c *domain.SupportCase) error {
		if c.Status != domain.CaseInProgress && c.Status != domain.CaseWaitingBuyer && c.Status != domain.CaseWaitingVendor {
			return apperror.Conflict("Only a case in progress can be resolved (assign it first; a pending resolution waits for its outcome)")
		}
		done, err := uc.linkedOutcome(ctx, c, in.Kind, in.LinkedOperationID)
		if err != nil {
			return err
		}
		c.ResolutionKind, c.ResolutionNote = &in.Kind, reason
		if in.LinkedOperationID != "" {
			c.ResolutionRef = &in.LinkedOperationID
		}
		changes := map[string]any{"resolution_kind": in.Kind}
		if c.ResolutionRef != nil {
			changes["resolution_ref"] = *c.ResolutionRef
		}
		if !done {
			pending = true
			return uc.moveCaseWith(ctx, c, domain.CaseResolutionPending, "admin", &adminID, "resolution_proposed", reason, changes)
		}
		if err := uc.moveCaseWith(ctx, c, domain.CaseResolved, "admin", &adminID, "resolved", reason, changes); err != nil {
			return err
		}
		return uc.notifyResolved(ctx, c)
	})
	if err != nil {
		return nil, false, err
	}
	uc.runEffectsSoon(ctx, c.OrderID)
	return c, pending, nil
}

// linkedOutcome checks a refund/return belongs to the case's vendor order
// and reports whether its outcome is already confirmed. A failed or
// rejected one cannot resolve the case.
func (uc *OrderUseCase) linkedOutcome(ctx context.Context, c *domain.SupportCase, kind, ref string) (bool, error) {
	switch kind {
	case domain.ResolutionRefund:
		refund, err := uc.Refunds.FindByID(ctx, ref)
		if err != nil {
			if errors.Is(err, repository.ErrRefundNotFound) {
				return false, domain.InvalidLinkedOperation("Refund not found")
			}
			return false, err
		}
		if refund.OrderID != c.OrderID || (refund.VendorOrderID != nil && *refund.VendorOrderID != c.VendorOrderID) {
			return false, domain.InvalidLinkedOperation("The refund does not belong to this case's order")
		}
		switch refund.Status {
		case domain.RefundSucceeded:
			return true, nil
		case domain.RefundRequested, domain.RefundSubmitted:
			return false, nil
		}
		return false, domain.InvalidLinkedOperation("The refund " + string(refund.Status) + "; request a new one first")
	case domain.ResolutionReturn:
		rr, err := uc.Returns.FindByID(ctx, ref)
		if err != nil {
			if errors.Is(err, repository.ErrReturnRequestNotFound) {
				return false, domain.InvalidLinkedOperation("Return request not found")
			}
			return false, err
		}
		vendorOrderID, _, err := uc.Returns.VendorOf(ctx, rr.ID)
		if err != nil {
			return false, err
		}
		if rr.OrderID != c.OrderID || vendorOrderID != c.VendorOrderID {
			return false, domain.InvalidLinkedOperation("The return does not belong to this case's order")
		}
		switch rr.Status {
		case domain.ReturnRefunded:
			return true, nil
		case domain.ReturnRejected:
			return false, domain.InvalidLinkedOperation("The return was rejected")
		}
		return false, nil
	}
	return true, nil
}

// CloseSupportCase closes a resolved case on an admin's decision.
func (uc *OrderUseCase) CloseSupportCase(ctx context.Context, adminID, caseID string, expectedVersion int64, reason string) (*domain.SupportCase, error) {
	note, err := adminReason(reason)
	if err != nil {
		return nil, err
	}
	if err := uc.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	return uc.mutateCase(ctx, adminID, caseID, expectedVersion, func(ctx context.Context, c *domain.SupportCase) error {
		return uc.moveCase(ctx, c, domain.CaseClosed, "admin", &adminID, "closed", note)
	})
}

// ConfirmSupportCase: the buyer accepts the resolution and closes the case.
func (uc *OrderUseCase) ConfirmSupportCase(ctx context.Context, buyerID, caseID string) (*domain.SupportCase, error) {
	return uc.buyerCaseStep(ctx, buyerID, caseID, func(ctx context.Context, c *domain.SupportCase) error {
		if c.Status != domain.CaseResolved {
			return apperror.Conflict("Only a resolved case can be confirmed")
		}
		return uc.moveCase(ctx, c, domain.CaseClosed, "buyer", &buyerID, "confirmed", nil)
	})
}

// ReopenSupportCase: the buyer disagrees with the resolution within the
// reopen window; the message says why. The resolution is cleared (it stays
// in the timeline) and the case is back in progress.
func (uc *OrderUseCase) ReopenSupportCase(ctx context.Context, buyerID, caseID, message string) (*domain.SupportCase, error) {
	text, err := uc.SupportPolicy.ValidateSupportText(message)
	if err != nil {
		return nil, err
	}
	return uc.buyerCaseStep(ctx, buyerID, caseID, func(ctx context.Context, c *domain.SupportCase) error {
		if err := uc.SupportPolicy.CanReopen(c, uc.Now()); err != nil {
			return err
		}
		if _, err := uc.addMessage(ctx, c, SupportActor{ID: buyerID, Role: "buyer"}, text, domain.VisibilityPublic, nil, nil, nil); err != nil {
			return err
		}
		c.ResolutionKind, c.ResolutionRef, c.ResolutionNote, c.ResolvedAt = nil, nil, nil, nil
		return uc.moveCase(ctx, c, domain.CaseInProgress, "buyer", &buyerID, "reopened", nil)
	})
}

func (uc *OrderUseCase) buyerCaseStep(ctx context.Context, buyerID, caseID string, fn func(context.Context, *domain.SupportCase) error) (*domain.SupportCase, error) {
	c, err := uc.supportCaseFor(ctx, SupportActor{ID: buyerID, Role: "buyer"}, caseID)
	if err != nil {
		return nil, err
	}
	var result *domain.SupportCase
	err = uc.withOrder(ctx, c.OrderID, func(ctx context.Context) error {
		current, err := uc.Support.FindByID(ctx, caseID)
		if err != nil {
			return err
		}
		if err := fn(ctx, current); err != nil {
			return err
		}
		result = current
		return nil
	})
	return result, err
}

// mutateCase runs an admin change on the locked case at expectedVersion.
func (uc *OrderUseCase) mutateCase(ctx context.Context, adminID, caseID string, expectedVersion int64,
	fn func(context.Context, *domain.SupportCase) error) (*domain.SupportCase, error) {
	c, err := uc.supportCaseFor(ctx, SupportActor{ID: adminID, Role: "admin"}, caseID)
	if err != nil {
		return nil, err
	}
	var result *domain.SupportCase
	err = uc.withOrder(ctx, c.OrderID, func(ctx context.Context) error {
		current, err := uc.Support.FindByID(ctx, caseID)
		if err != nil {
			return err
		}
		if expectedVersion <= 0 || current.Version != expectedVersion {
			return domain.CaseVersionConflict()
		}
		if err := fn(ctx, current); err != nil {
			return err
		}
		result = current
		return nil
	})
	return result, err
}

// moveCase applies one status transition with its timeline entry (and an
// admin audit row for an admin), in the caller's transaction.
func (uc *OrderUseCase) moveCase(ctx context.Context, c *domain.SupportCase, to domain.SupportCaseStatus, role string, actor *string,
	action string, note *string) error {
	return uc.moveCaseWith(ctx, c, to, role, actor, action, note, nil)
}

func (uc *OrderUseCase) moveCaseWith(ctx context.Context, c *domain.SupportCase, to domain.SupportCaseStatus, role string, actor *string,
	action string, note *string, changes map[string]any) error {
	if !domain.CanTransitionCase(c.Status, to) {
		return apperror.Conflict("Cannot move this case from " + string(c.Status) + " to " + string(to))
	}
	now := uc.Now()
	c.DueAt = uc.SupportPolicy.DueAt(to, now)
	switch to {
	case domain.CaseResolved:
		resolved := now.UTC()
		c.ResolvedAt = &resolved
	case domain.CaseClosed:
		closed := now.UTC()
		c.ClosedAt = &closed
	}
	return uc.saveCaseStep(ctx, c, to, role, actor, action, note, changes)
}

func (uc *OrderUseCase) saveCaseStep(ctx context.Context, c *domain.SupportCase, to domain.SupportCaseStatus, role string, actor *string,
	action string, note *string, changes map[string]any) error {
	from := c.Status
	c.Status = to
	if err := uc.Support.Save(ctx, c); err != nil {
		c.Status = from
		if errors.Is(err, repository.ErrStaleState) {
			return domain.CaseVersionConflict()
		}
		return err
	}
	fromStatus := string(from)
	if err := uc.Support.AddEvent(ctx, &domain.SupportCaseEvent{CaseID: c.ID, ActorID: actor, ActorRole: role, Action: action,
		FromStatus: &fromStatus, ToStatus: string(to), Note: note}); err != nil {
		return err
	}
	if role != "admin" || actor == nil {
		return nil
	}
	if changes == nil {
		changes = map[string]any{}
	}
	if from != to {
		changes["status"] = domain.Change(from, to)
	}
	return uc.audit(ctx, domain.AdminAction{ActorID: *actor, Action: "support_case_" + action, EntityType: domain.AuditSupportCase,
		EntityID: c.ID, OrderID: &c.OrderID, Reason: note, Changes: changes})
}

func (uc *OrderUseCase) notifyResolved(ctx context.Context, c *domain.SupportCase) error {
	notice := domain.NewNotifyEffect(c.OrderID, c.BuyerID, notifySupportCaseResolved)
	notice.Target = notifySupportCaseResolved + ":" + c.ID + ":" + c.ResolvedAt.Format(time.RFC3339Nano)
	return uc.Effects.Enqueue(ctx, notice)
}

// syncSupportResolution moves the cases waiting for a refund or return
// outcome: confirmed → resolved; failed or rejected → back in progress for
// the admin (a failed refund never closes a case). Runs in the outcome's
// transaction, under the order lock.
func (uc *OrderUseCase) syncSupportResolution(ctx context.Context, kind, ref string, succeeded bool, note *string) error {
	if uc.Support == nil {
		return nil
	}
	cases, err := uc.Support.ListPendingResolution(ctx, kind, ref)
	if err != nil {
		return err
	}
	for _, listed := range cases {
		c, err := uc.Support.FindByID(ctx, listed.ID)
		if err != nil {
			return err
		}
		if c.Status != domain.CaseResolutionPending {
			continue
		}
		if succeeded {
			if err := uc.moveCase(ctx, c, domain.CaseResolved, "system", nil, "resolution_confirmed", nil); err != nil {
				return err
			}
			if err := uc.notifyResolved(ctx, c); err != nil {
				return err
			}
			continue
		}
		c.ResolutionKind, c.ResolutionRef = nil, nil
		if err := uc.moveCase(ctx, c, domain.CaseInProgress, "system", nil, "resolution_failed", note); err != nil {
			return err
		}
		uc.Log.Warn().Str("case_id", c.ID).Str("order_id", c.OrderID).Str("kind", kind).Str("ref", ref).Msg("order_support_resolution_failed")
	}
	return nil
}

// CloseExpiredSupportCases closes resolved cases past their reopen window,
// which also releases their payout hold.
func (uc *OrderUseCase) CloseExpiredSupportCases(ctx context.Context, limit int) (int, error) {
	if uc.Support == nil {
		return 0, nil
	}
	cases, err := uc.Support.ListResolvedBefore(ctx, uc.Now().Add(-uc.SupportPolicy.ReopenWindow), limit)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, listed := range cases {
		err := uc.withOrder(ctx, listed.OrderID, func(ctx context.Context) error {
			c, err := uc.Support.FindByID(ctx, listed.ID)
			if err != nil {
				return err
			}
			if !uc.SupportPolicy.ReopenExpired(c, uc.Now()) {
				return nil
			}
			closed++
			return uc.moveCase(ctx, c, domain.CaseClosed, "system", nil, "auto_closed", nil)
		})
		if err != nil {
			uc.Log.Error().Err(err).Str("case_id", listed.ID).Msg("order_support_auto_close_failed")
		}
	}
	return closed, nil
}

func (uc *OrderUseCase) validAttachmentIDs(ids []string) ([]string, error) {
	if len(ids) > uc.SupportPolicy.MaxAttachments {
		return nil, apperror.Validation("At most 5 attachments per message")
	}
	out := slices.Clone(ids)
	slices.Sort(out)
	if len(slices.Compact(out)) != len(ids) {
		return nil, apperror.Validation("An attachment is listed twice")
	}
	if len(out) > 0 && uc.Attachments == nil {
		return nil, domain.AttachmentsUnavailable()
	}
	return out, nil
}

func validSupportKey(key string) error {
	if key != "" && !idempotencyKeyPattern.MatchString(key) {
		return apperror.Validation("Idempotency-Key must be 8-100 letters, digits or ._:-")
	}
	return nil
}

// requestHash fingerprints a normalized request for idempotent replays.
func requestHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func encodeCaseCursor(c *domain.SupportCase) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID))
}

func decodeCaseCursor(raw string) (*repository.CaseCursor, error) {
	if raw == "" {
		return nil, nil
	}
	invalid := apperror.Validation("Invalid cursor")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalid
	}
	at, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return nil, invalid
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil || len(id) != 36 {
		return nil, invalid
	}
	return &repository.CaseCursor{CreatedAt: t, ID: id}, nil
}
