package casesla

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
)

type Store struct {
	Pool *pgxpool.Pool
	// Owner callbacks lock the aggregate before its deadline and mirror its
	// assignment. They run inside the mutation transaction, with no network I/O.
	LockResource   func(context.Context, pgx.Tx, string, string) error
	AssignResource func(context.Context, pgx.Tx, *Item) error
}

const AuditSearchSQL = `SELECT id::text,created_at,NULLIF(actor_id,'system'),
 'sla_'||replace(action,'-','_'),'work_item'::text,work_item_id::text,reason,request_id,
 jsonb_build_object('before',before_state,'after',after_state) FROM case_sla_audit`

func readItem(row pgx.Row) (*Item, error) {
	var data []byte
	if err := row.Scan(&data); err != nil {
		return nil, err
	}
	var i Item
	if err := json.Unmarshal(data, &i); err != nil {
		return nil, err
	}
	return &i, nil
}

// Save participates in the owner's transaction; there is no cross-service DB access.
func save(ctx context.Context, tx pgx.Tx, i *Item) error {
	data, err := json.Marshal(i)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE case_sla_work_items SET payload=$2, active=$3, due_at=$4,
		version=$5, next_check_at=LEAST(next_check_at,now()) WHERE id=$1`, i.ID, data, i.Active, i.EffectiveDueAt(), i.Version)
	return err
}

// Sync must run in the same transaction as the resource write. Domain
// adapters supply stages; this component cannot perform domain transitions.
func Sync(ctx context.Context, tx pgx.Tx, in StageInput) (*Item, error) {
	if tx == nil {
		return nil, errors.New("case SLA sync requires owner transaction")
	}
	if in.At.IsZero() || in.CreatedAt.IsZero() {
		return nil, errors.New("case SLA requires original timestamps")
	}
	i, err := readItem(tx.QueryRow(ctx, `SELECT payload FROM case_sla_work_items WHERE resource_type=$1 AND resource_id=$2 FOR UPDATE`, in.ResourceType, in.ResourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		if in.Stage == "" {
			return nil, nil
		}
		n := New(in)
		n.ID = uuid.NewString()
		data, err := json.Marshal(n)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO case_sla_work_items (id,resource_type,resource_id,payload,active,due_at,version,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, n.ID, n.ResourceType, n.ResourceID, data, n.Active, n.EffectiveDueAt(), n.Version, n.CreatedAt)
		return &n, err
	}
	if err != nil {
		return nil, fmt.Errorf("read case deadline: %w", err)
	}
	changed := i.Apply(in)
	if in.AssignmentChanged {
		i.AssigneeID = in.AssigneeID
		i.Version++
		changed = true
	}
	if changed {
		err = save(ctx, tx, i)
	}
	return i, err
}

type Filter struct {
	Status, AssigneeID, Cursor string
	Limit                      int
}

func ParseFilter(q url.Values) (Filter, error) {
	f := Filter{Status: q.Get("status"), AssigneeID: q.Get("assignee_id"), Cursor: q.Get("cursor"), Limit: 20}
	if s := q.Get("limit"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 1 || n > 100 {
			return f, apperror.Validation("limit must be 1-100")
		}
		f.Limit = n
	}
	switch f.Status {
	case "", "active", "overdue", "unassigned", "needs_attention", "legacy":
	default:
		return f, apperror.Validation("Invalid work item status")
	}
	if f.AssigneeID != "" {
		if _, e := uuid.Parse(f.AssigneeID); e != nil {
			return f, apperror.Validation("Invalid assignee_id")
		}
	}
	_, _, e := decodeCursor(f.Cursor)
	return f, e
}

type Page struct {
	Items       []*Item   `json:"items"`
	NextCursor  string    `json:"next_cursor,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`
}

func decodeCursor(raw string) (*time.Time, *string, error) {
	if raw == "" {
		return nil, nil, nil
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	t, id, ok := strings.Cut(string(b), "|")
	at, te := time.Parse(time.RFC3339Nano, t)
	_, ie := uuid.Parse(id)
	if e != nil || !ok || te != nil || ie != nil {
		return nil, nil, apperror.Validation("Invalid work item cursor")
	}
	return &at, &id, nil
}
func (s Store) List(ctx context.Context, f Filter, now time.Time) (*Page, error) {
	if f.Limit < 1 || f.Limit > 100 {
		return nil, apperror.Validation("limit must be 1-100")
	}
	at, id, err := decodeCursor(f.Cursor)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT payload FROM case_sla_work_items WHERE active
		AND ($1<>'overdue' OR due_at<=$2)
		AND ($1<>'unassigned' OR payload->>'assignee_id' IS NULL)
		AND ($1<>'needs_attention' OR payload->>'needs_attention'='true')
		AND ($1<>'legacy' OR payload->>'legacy'='true')
		AND ($3='' OR payload->>'assignee_id'=$3)
		AND ($4::timestamptz IS NULL OR (created_at,id)<($4,$5::uuid))
		ORDER BY created_at DESC,id DESC LIMIT $6`, f.Status, now, f.AssigneeID, at, id, f.Limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p := &Page{Items: []*Item{}, GeneratedAt: now.UTC()}
	for rows.Next() {
		i, e := readItem(rows)
		if e != nil {
			return nil, e
		}
		p.Items = append(p.Items, i)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(p.Items) > f.Limit {
		p.Items = p.Items[:f.Limit]
		last := p.Items[len(p.Items)-1]
		p.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID))
	}
	return p, nil
}

type Mutation struct {
	ExpectedVersion int64     `json:"expected_version"`
	Reason          string    `json:"reason"`
	AssigneeID      string    `json:"assignee_id"`
	NewDueAt        time.Time `json:"new_due_at"`
}

func audit(ctx context.Context, tx pgx.Tx, i *Item, actor, action, reason string, before *Item) error {
	oldData, e := json.Marshal(before)
	if e != nil {
		return e
	}
	newData, e := json.Marshal(i)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO case_sla_audit (work_item_id,actor_id,action,reason,before_state,after_state,request_id) VALUES ($1,$2,$3,$4,$5,$6,$7)`, i.ID, actor, action, reason, oldData, newData, middleware.CorrelationID(ctx))
	return e
}

func (s Store) Mutate(ctx context.Context, id, actor, action, key string, m Mutation, now time.Time) (*Item, error) {
	var out *Item
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if s.LockResource != nil {
			var resourceType, resourceID string
			e := tx.QueryRow(ctx, `SELECT resource_type,resource_id::text FROM case_sla_work_items WHERE id=$1`, id).Scan(&resourceType, &resourceID)
			if errors.Is(e, pgx.ErrNoRows) {
				return apperror.NotFound("Work item not found")
			}
			if e != nil {
				return e
			}
			if e = s.LockResource(ctx, tx, resourceType, resourceID); e != nil {
				return e
			}
		}
		i, e := readItem(tx.QueryRow(ctx, `SELECT payload FROM case_sla_work_items WHERE id=$1 FOR UPDATE`, id))
		if errors.Is(e, pgx.ErrNoRows) {
			return apperror.NotFound("Work item not found")
		}
		if e != nil {
			return e
		}
		request, e := json.Marshal(m)
		if e != nil {
			return e
		}
		var previousRequest, previousResult []byte
		e = tx.QueryRow(ctx, `SELECT request,response FROM case_sla_commands WHERE actor_id=$1 AND work_item_id=$2 AND action=$3 AND key=$4`, actor, id, action, key).Scan(&previousRequest, &previousResult)
		if e == nil {
			var old Mutation
			if e = json.Unmarshal(previousRequest, &old); e != nil {
				return e
			}
			if old.ExpectedVersion != m.ExpectedVersion || old.Reason != m.Reason || old.AssigneeID != m.AssigneeID || !old.NewDueAt.Equal(m.NewDueAt) {
				return apperror.Conflict("Idempotency key used for a different request")
			}
			out = &Item{}
			return json.Unmarshal(previousResult, out)
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if !i.Active || i.Version != m.ExpectedVersion {
			return &apperror.Error{Code: "stage_changed", Status: 409, Message: "Work item changed; refresh before acting"}
		}
		before := *i
		switch action {
		case "assignments":
			i.AssigneeID = nil
			if m.AssigneeID != "" {
				i.AssigneeID = &m.AssigneeID
			}
			i.Version++
			if s.AssignResource != nil {
				if e = s.AssignResource(ctx, tx, i); e != nil {
					return e
				}
			}
		case "extensions":
			if e = i.Extend(now, m.NewDueAt); e != nil {
				return e
			}
		case "activations":
			if !i.Legacy {
				return apperror.Conflict("Work item is already active for notices")
			}
			i.Legacy = false
			i.Version++
		case "notice-replays":
			if _, e = tx.Exec(ctx, `UPDATE case_sla_notice_outbox SET attempts=0,parked=false,lease_until=NULL,next_attempt_at=now() WHERE work_item_id=$1 AND parked AND delivered_at IS NULL`, i.ID); e != nil {
				return e
			}
			i.Version++
		default:
			return apperror.Validation("Invalid action")
		}
		if e = save(ctx, tx, i); e != nil {
			return e
		}
		if e = audit(ctx, tx, i, actor, action, m.Reason, &before); e != nil {
			return e
		}
		result, e := json.Marshal(i)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO case_sla_commands (actor_id,work_item_id,action,key,request,response) VALUES ($1,$2,$3,$4,$5,$6)`, actor, id, action, key, request, result)
		out = i
		return e
	})
	return out, err
}
