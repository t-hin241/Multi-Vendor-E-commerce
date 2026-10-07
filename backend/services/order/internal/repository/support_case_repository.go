package repository

import (
	"context"
	"errors"
	"shopee/backend/pkg/casesla"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/order/internal/domain"
)

var (
	ErrSupportCaseNotFound = errors.New("repository: support case not found")
	ErrAttachmentNotFound  = errors.New("repository: case attachment not found")
	// ErrSupportKeyTaken: the idempotency key already names another case
	// or message; the use case replays or refuses it.
	ErrSupportKeyTaken = errors.New("repository: support idempotency key already used")
)

type SupportCaseRepository struct{ pool *pgxpool.Pool }

func NewSupportCaseRepository(pool *pgxpool.Pool) *SupportCaseRepository {
	return &SupportCaseRepository{pool: pool}
}

const supportCaseColumns = `id, order_id, vendor_order_id, vendor_id, buyer_id, category, status,
 CASE WHEN EXISTS(SELECT 1 FROM case_sla_work_items w WHERE w.resource_type='support' AND w.resource_id=support_cases.id)
 THEN (SELECT (w.payload->>'assignee_id')::uuid FROM case_sla_work_items w WHERE w.resource_type='support' AND w.resource_id=support_cases.id) ELSE assignee_id END,
 policy_version, COALESCE((SELECT due_at FROM case_sla_work_items w WHERE w.resource_type='support' AND w.resource_id=support_cases.id AND w.active),due_at),
	financial_hold, resolution_kind, resolution_ref, resolution_note, resolved_at, closed_at, related_case_id, idempotency_key,
	request_hash, version, created_at, updated_at,
 (SELECT due_at FROM case_sla_work_items w WHERE w.resource_type='support' AND w.resource_id=support_cases.id AND w.active),
 COALESCE((SELECT payload->>'waiting_on' FROM case_sla_work_items w WHERE w.resource_type='support' AND w.resource_id=support_cases.id AND w.active),'')`

func scanSupportCase(row pgx.Row) (*domain.SupportCase, error) {
	var c domain.SupportCase
	if err := row.Scan(&c.ID, &c.OrderID, &c.VendorOrderID, &c.VendorID, &c.BuyerID, &c.Category, &c.Status, &c.AssigneeID, &c.PolicyVersion,
		&c.DueAt, &c.FinancialHold, &c.ResolutionKind, &c.ResolutionRef, &c.ResolutionNote, &c.ResolvedAt, &c.ClosedAt, &c.RelatedCaseID,
		&c.IdempotencyKey, &c.RequestHash, &c.Version, &c.CreatedAt, &c.UpdatedAt, &c.ActionDueAt, &c.WaitingOn); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSupportCaseNotFound
		}
		return nil, err
	}
	return &c, nil
}

// Create inserts a case the use case already validated. A second case not
// yet closed for the same buyer, vendor order and category is refused by
// the unique index, which also covers two concurrent requests.
func (r *SupportCaseRepository) Create(ctx context.Context, c *domain.SupportCase) error {
	if !InTransaction(ctx) {
		return (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error { return r.Create(ctx, c) })
	}
	err := connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO support_cases (order_id, vendor_order_id, vendor_id, buyer_id, category, status, policy_version, due_at, financial_hold,
		    related_case_id, idempotency_key, request_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, version, created_at, updated_at`,
		c.OrderID, c.VendorOrderID, c.VendorID, c.BuyerID, c.Category, c.Status, c.PolicyVersion, c.DueAt, c.FinancialHold,
		c.RelatedCaseID, c.IdempotencyKey, c.RequestHash,
	).Scan(&c.ID, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "support_cases_idempotency_key" {
			return ErrSupportKeyTaken
		}
		return domain.CaseAlreadyOpen()
	}
	if err != nil {
		return err
	}
	return r.syncSLA(ctx, c)
}

// FindByID returns a case; inside a transaction the row is locked.
func (r *SupportCaseRepository) FindByID(ctx context.Context, id string) (*domain.SupportCase, error) {
	lock := ""
	if InTransaction(ctx) {
		lock = " FOR UPDATE"
	}
	return scanSupportCase(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+supportCaseColumns+` FROM support_cases WHERE id = $1`+lock, id))
}

// FindByIdempotencyKey returns the buyer's case created with key, or nil.
func (r *SupportCaseRepository) FindByIdempotencyKey(ctx context.Context, buyerID, key string) (*domain.SupportCase, error) {
	c, err := scanSupportCase(connection(ctx, r.pool).QueryRow(ctx,
		`SELECT `+supportCaseColumns+` FROM support_cases WHERE buyer_id = $1 AND idempotency_key = $2`, buyerID, key))
	if errors.Is(err, ErrSupportCaseNotFound) {
		return nil, nil
	}
	return c, err
}

// FindNotClosed returns the buyer's case not yet closed for the vendor
// order and category, or nil.
func (r *SupportCaseRepository) FindNotClosed(ctx context.Context, buyerID, vendorOrderID string, category domain.SupportCategory) (*domain.SupportCase, error) {
	c, err := scanSupportCase(connection(ctx, r.pool).QueryRow(ctx, `SELECT `+supportCaseColumns+` FROM support_cases
		WHERE buyer_id = $1 AND vendor_order_id = $2 AND category = $3 AND status <> 'closed'`, buyerID, vendorOrderID, category))
	if errors.Is(err, ErrSupportCaseNotFound) {
		return nil, nil
	}
	return c, err
}

// Save writes a case's mutable fields with compare-and-set on the version
// the caller read; a concurrent change fails with ErrStaleState.
func (r *SupportCaseRepository) Save(ctx context.Context, c *domain.SupportCase) error {
	if !InTransaction(ctx) {
		return (Transactions{Pool: r.pool}).Run(ctx, func(ctx context.Context) error { return r.Save(ctx, c) })
	}
	var previousAssignee *string
	if err := connection(ctx, r.pool).QueryRow(ctx, `SELECT assignee_id FROM support_cases WHERE id=$1 FOR UPDATE`, c.ID).Scan(&previousAssignee); err != nil {
		return err
	}
	var updatedAt time.Time
	err := connection(ctx, r.pool).QueryRow(ctx, `
		UPDATE support_cases SET status = $3, assignee_id = $4, due_at = $5, resolution_kind = $6, resolution_ref = $7,
		    resolution_note = $8, resolved_at = $9, closed_at = $10, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 RETURNING updated_at`,
		c.ID, c.Version, c.Status, c.AssigneeID, c.DueAt, c.ResolutionKind, c.ResolutionRef, c.ResolutionNote, c.ResolvedAt, c.ClosedAt,
	).Scan(&updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleState
	}
	if err != nil {
		return err
	}
	c.Version++
	c.UpdatedAt = updatedAt
	in := c.SLAStage()
	in.AssignmentChanged = (previousAssignee == nil) != (c.AssigneeID == nil) || (previousAssignee != nil && c.AssigneeID != nil && *previousAssignee != *c.AssigneeID)
	tx, _ := ctx.Value(transactionKey{}).(pgx.Tx)
	i, e := casesla.Sync(ctx, tx, in)
	if i != nil && i.Active {
		due := i.EffectiveDueAt()
		c.ActionDueAt = &due
		c.WaitingOn = i.WaitingOn
	} else {
		c.ActionDueAt = nil
		c.WaitingOn = ""
	}
	return e
}

// SupportCaseFilter narrows a case list; empty fields match everything.
type SupportCaseFilter struct {
	BuyerID    string
	VendorID   string
	Status     string
	AssigneeID string
	Unassigned bool
	// OverdueAt lists only cases whose deadline passed before it.
	OverdueAt *time.Time
}

// CaseCursor is the position after the last case of a page (newest first).
type CaseCursor struct {
	CreatedAt time.Time
	ID        string
}

// List returns up to limit cases newest first, after cursor if given.
func (r *SupportCaseRepository) List(ctx context.Context, f SupportCaseFilter, after *CaseCursor, limit int) ([]*domain.SupportCase, error) {
	var cursorAt *time.Time
	var cursorID *string
	if after != nil {
		cursorAt, cursorID = &after.CreatedAt, &after.ID
	}
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+supportCaseColumns+` FROM support_cases
		WHERE ($1 = '' OR buyer_id::text = $1)
		  AND ($2 = '' OR vendor_id::text = $2)
		  AND ($3 = '' OR status = $3)
		  AND ($4 = '' OR EXISTS(SELECT 1 FROM case_sla_work_items w WHERE w.resource_type='support' AND w.resource_id=support_cases.id AND w.payload->>'assignee_id'=$4))
		  AND (NOT $5 OR NOT EXISTS(SELECT 1 FROM case_sla_work_items w WHERE w.resource_type='support' AND w.resource_id=support_cases.id AND w.payload->>'assignee_id' IS NOT NULL))
		  AND ($6::timestamptz IS NULL OR EXISTS(SELECT 1 FROM case_sla_work_items w WHERE w.resource_type='support' AND w.resource_id=support_cases.id AND w.active AND w.due_at<=$6))
		  AND ($7::timestamptz IS NULL OR (created_at, id) < ($7, $8::uuid))
		ORDER BY created_at DESC, id DESC LIMIT $9`,
		f.BuyerID, f.VendorID, f.Status, f.AssigneeID, f.Unassigned, f.OverdueAt, cursorAt, cursorID, limit)
	if err != nil {
		return nil, err
	}
	return collectSupportCases(rows)
}

// ListPendingResolution returns the cases waiting for the outcome of the
// refund or return ref.
func (r *SupportCaseRepository) ListPendingResolution(ctx context.Context, kind, ref string) ([]*domain.SupportCase, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+supportCaseColumns+` FROM support_cases
		WHERE status = 'resolution_pending' AND resolution_kind = $1 AND resolution_ref = $2 ORDER BY created_at, id`, kind, ref)
	if err != nil {
		return nil, err
	}
	return collectSupportCases(rows)
}

// ListResolvedBefore returns cases resolved before t (past their reopen
// window), oldest first.
func (r *SupportCaseRepository) ListResolvedBefore(ctx context.Context, t time.Time, limit int) ([]*domain.SupportCase, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+supportCaseColumns+` FROM support_cases
		WHERE status = 'resolved' AND resolved_at < $1 ORDER BY resolved_at, id LIMIT $2`, t, limit)
	if err != nil {
		return nil, err
	}
	return collectSupportCases(rows)
}

func collectSupportCases(rows pgx.Rows) ([]*domain.SupportCase, error) {
	defer rows.Close()
	out := []*domain.SupportCase{}
	for rows.Next() {
		c, err := scanSupportCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddMessage appends an immutable message in the caller's transaction.
func (r *SupportCaseRepository) AddMessage(ctx context.Context, m *domain.SupportMessage) error {
	err := connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO support_messages (case_id, author_id, author_role, visibility, text, idempotency_key, request_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`,
		m.CaseID, m.AuthorID, m.AuthorRole, m.Visibility, m.Text, m.IdempotencyKey, m.RequestHash).Scan(&m.ID, &m.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "support_messages_idempotency_key" {
		return ErrSupportKeyTaken
	}
	return err
}

// FindMessageByKey returns the author's message written with key, or nil.
func (r *SupportCaseRepository) FindMessageByKey(ctx context.Context, authorID, key string) (*domain.SupportMessage, error) {
	var m domain.SupportMessage
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT id, case_id, author_id, author_role, visibility, text, idempotency_key, request_hash, created_at
		FROM support_messages WHERE author_id = $1 AND idempotency_key = $2`, authorID, key).
		Scan(&m.ID, &m.CaseID, &m.AuthorID, &m.AuthorRole, &m.Visibility, &m.Text, &m.IdempotencyKey, &m.RequestHash, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ListMessages returns a case's messages oldest first with their
// attachments; internal notes only when includeInternal.
func (r *SupportCaseRepository) ListMessages(ctx context.Context, caseID string, includeInternal bool) ([]*domain.SupportMessage, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT id, case_id, author_id, author_role, visibility, text, idempotency_key, request_hash, created_at
		FROM support_messages WHERE case_id = $1 AND ($2 OR visibility = 'public') ORDER BY created_at, id`, caseID, includeInternal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.SupportMessage{}
	byID := map[string]*domain.SupportMessage{}
	for rows.Next() {
		var m domain.SupportMessage
		if err := rows.Scan(&m.ID, &m.CaseID, &m.AuthorID, &m.AuthorRole, &m.Visibility, &m.Text, &m.IdempotencyKey, &m.RequestHash, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Attachments = []*domain.CaseAttachment{}
		out = append(out, &m)
		byID[m.ID] = &m
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	attachments, err := r.listAttachments(ctx, `WHERE case_id = $1 AND state <> 'deleted' AND message_id IS NOT NULL ORDER BY created_at, id`, caseID)
	if err != nil {
		return nil, err
	}
	for _, a := range attachments {
		if m, ok := byID[*a.MessageID]; ok {
			m.Attachments = append(m.Attachments, a)
		}
	}
	return out, nil
}

// AddEvent appends one timeline entry in the caller's transaction.
func (r *SupportCaseRepository) AddEvent(ctx context.Context, e *domain.SupportCaseEvent) error {
	return connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO support_case_history (case_id, actor_id, actor_role, action, from_status, to_status, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`,
		e.CaseID, e.ActorID, e.ActorRole, e.Action, e.FromStatus, e.ToStatus, e.Note).Scan(&e.ID, &e.CreatedAt)
}

func (r *SupportCaseRepository) ListEvents(ctx context.Context, caseID string) ([]*domain.SupportCaseEvent, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT id, case_id, actor_id, actor_role, action, from_status, to_status, note, created_at
		FROM support_case_history WHERE case_id = $1 ORDER BY created_at, id`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.SupportCaseEvent{}
	for rows.Next() {
		var e domain.SupportCaseEvent
		if err := rows.Scan(&e.ID, &e.CaseID, &e.ActorID, &e.ActorRole, &e.Action, &e.FromStatus, &e.ToStatus, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

const attachmentColumns = `id, owner_id, case_id, message_id, object_key, content_type, size_bytes, state, created_at`

func (r *SupportCaseRepository) listAttachments(ctx context.Context, where string, args ...any) ([]*domain.CaseAttachment, error) {
	rows, err := connection(ctx, r.pool).Query(ctx, `SELECT `+attachmentColumns+` FROM case_attachments `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.CaseAttachment{}
	for rows.Next() {
		var a domain.CaseAttachment
		if err := rows.Scan(&a.ID, &a.OwnerID, &a.CaseID, &a.MessageID, &a.ObjectKey, &a.ContentType, &a.SizeBytes, &a.State, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// CreateAttachment records an object already written to the private bucket.
func (r *SupportCaseRepository) CreateAttachment(ctx context.Context, a *domain.CaseAttachment) error {
	return connection(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO case_attachments (owner_id, object_key, content_type, size_bytes) VALUES ($1, $2, $3, $4)
		RETURNING id, state, created_at`, a.OwnerID, a.ObjectKey, a.ContentType, a.SizeBytes).Scan(&a.ID, &a.State, &a.CreatedAt)
}

// FindAttachment returns one attachment row.
func (r *SupportCaseRepository) FindAttachment(ctx context.Context, id string) (*domain.CaseAttachment, error) {
	items, err := r.listAttachments(ctx, `WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrAttachmentNotFound
	}
	return items[0], nil
}

// AttachToMessage links the owner's still-unattached uploads to a message,
// locking them; it returns how many were linked, so the caller can refuse
// ids that are not the owner's or already used.
func (r *SupportCaseRepository) AttachToMessage(ctx context.Context, ids []string, ownerID, caseID, messageID string) (int64, error) {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE case_attachments SET state = 'attached', case_id = $3, message_id = $4, attached_at = now()
		WHERE id::text = ANY($1) AND owner_id = $2 AND state = 'uploaded'`, ids, ownerID, caseID, messageID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListOrphanAttachments returns uploads never attached before t.
func (r *SupportCaseRepository) ListOrphanAttachments(ctx context.Context, before time.Time, limit int) ([]*domain.CaseAttachment, error) {
	return r.listAttachments(ctx, `WHERE state = 'uploaded' AND created_at < $1 ORDER BY created_at, id LIMIT $2`, before, limit)
}

// ListExpiredAttachments returns attachments of cases closed before t.
func (r *SupportCaseRepository) ListExpiredAttachments(ctx context.Context, closedBefore time.Time, limit int) ([]*domain.CaseAttachment, error) {
	return r.listAttachments(ctx, `WHERE state = 'attached' AND case_id IN (
		SELECT id FROM support_cases WHERE status = 'closed' AND closed_at < $1) ORDER BY created_at, id LIMIT $2`, closedBefore, limit)
}

// MarkAttachmentDeleted turns a row still in state from into a tombstone;
// false means it changed meanwhile (an upload attached at the last moment)
// and its object must be kept.
func (r *SupportCaseRepository) MarkAttachmentDeleted(ctx context.Context, id, from string) (bool, error) {
	tag, err := connection(ctx, r.pool).Exec(ctx, `UPDATE case_attachments SET state = 'deleted', deleted_at = now() WHERE id = $1 AND state = $2`, id, from)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *SupportCaseRepository) syncSLA(ctx context.Context, c *domain.SupportCase) error {
	tx, _ := ctx.Value(transactionKey{}).(pgx.Tx)
	i, err := casesla.Sync(ctx, tx, c.SLAStage())
	if err != nil {
		return err
	}
	if i != nil {
		due := i.EffectiveDueAt()
		c.ActionDueAt = &due
		c.WaitingOn = i.WaitingOn
		if !i.Active {
			c.ActionDueAt = nil
			c.WaitingOn = ""
		}
	}
	return nil
}
