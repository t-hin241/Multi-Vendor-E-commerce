package usecase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"sort"
	"time"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/apperror"
)

// auditPaths is each service's read-only audit search.
var auditPaths = map[string]string{
	"identity":  "/api/auth/admin/audit-events",
	"vendor":    "/api/vendor/admin/audit-events",
	"catalog":   "/api/catalog/admin/audit-events",
	"inventory": "/api/inventory/admin/audit-events",
	"order":     "/api/orders/admin/audit-events",
	"payment":   "/api/payments/admin/audit-events",
	"shipment":  "/api/shipments/admin/audit-events",
	"review":    "/api/reviews/admin/audit-events",
}

// AuditPage is one page of the merged audit, newest first. Complete is
// false when a service did not answer: its rows are missing from this page.
type AuditPage struct {
	Entries    []adminaudit.Entry `json:"entries"`
	NextCursor string             `json:"next_cursor,omitempty"`
	Sources    []SourceStatus     `json:"sources"`
	Complete   bool               `json:"complete"`
}

// auditCursor is the position of the last row shown: the merged order is
// (time, source, id), newest first.
type auditCursor struct {
	T time.Time `json:"t"`
	S string    `json:"s"`
	I string    `json:"i"`
}

var cursorIDPattern = regexp.MustCompile(`^[0-9a-zA-Z-]{1,64}$`)

func encodeCursor(e adminaudit.Entry) string {
	raw, _ := json.Marshal(auditCursor{T: e.OccurredAt, S: e.Source, I: e.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (s Service) decodeCursor(raw string) (*auditCursor, error) {
	if raw == "" {
		return nil, nil
	}
	invalid := apperror.Validation("cursor is invalid")
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 512 {
		return nil, invalid
	}
	var c auditCursor
	if err := json.Unmarshal(decoded, &c); err != nil || c.T.IsZero() || !cursorIDPattern.MatchString(c.I) {
		return nil, invalid
	}
	if _, ok := auditPaths[c.S]; !ok {
		return nil, invalid
	}
	return &c, nil
}

// SearchAudit searches every service's audit (or only one, by name) with
// the same filter and merges one page. The cursor is translated per
// service so paging neither repeats nor skips rows of a service that
// answered.
func (s Service) SearchAudit(ctx context.Context, adminID, authorization string, f adminaudit.Filter, cursor, only string) (*AuditPage, error) {
	if err := s.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	if only != "" {
		if _, ok := auditPaths[only]; !ok {
			return nil, apperror.Validation("source must be one of identity, vendor, catalog, inventory, order, payment, shipment, review")
		}
	}
	c, err := s.decodeCursor(cursor)
	if err != nil {
		return nil, err
	}
	if f.Limit < 1 || f.Limit > adminaudit.MaxLimit {
		f.Limit = adminaudit.DefaultLimit
	}
	var calls []call
	for _, u := range s.Upstreams {
		path, ok := auditPaths[u.Name]
		if !ok || (only != "" && u.Name != only) {
			continue
		}
		calls = append(calls, call{source: u.Name, path: path, query: sourceFilter(f, c, u.Name).Values()})
	}
	results := s.fetch(ctx, authorization, calls)

	page := &AuditPage{Entries: []adminaudit.Entry{}, Complete: true}
	full := false
	for _, call := range calls {
		r := results[call.source]
		if r.status.Status == statusOK {
			var body struct {
				Entries []adminaudit.Entry `json:"entries"`
			}
			if err := json.Unmarshal(r.data, &body); err != nil {
				r.status.Status, r.status.Error, r.status.FetchedAt = statusUnavailable, "unreadable audit page", nil
			} else {
				for _, e := range body.Entries {
					e.Source = call.source
					page.Entries = append(page.Entries, e)
				}
				full = full || len(body.Entries) >= f.Limit
			}
		}
		if r.status.Status != statusOK {
			page.Complete = false
		}
		page.Sources = append(page.Sources, r.status)
	}
	sort.SliceStable(page.Entries, func(i, j int) bool { return after(page.Entries[i], page.Entries[j]) })
	if len(page.Entries) > f.Limit {
		page.Entries, full = page.Entries[:f.Limit], true
	}
	if full && len(page.Entries) > 0 {
		page.NextCursor = encodeCursor(page.Entries[len(page.Entries)-1])
	}
	return page, nil
}

// after orders entries newest first, then by source and id descending
// (byte-wise, as the services compare ids).
func after(a, b adminaudit.Entry) bool {
	if !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.After(b.OccurredAt)
	}
	if a.Source != b.Source {
		return a.Source > b.Source
	}
	return a.ID > b.ID
}

// sourceFilter asks one service for the rows that come after the cursor in
// the merged order: at the cursor time, a service sorting before the
// cursor's one has all its rows after it, one sorting after has none.
func sourceFilter(f adminaudit.Filter, c *auditCursor, source string) adminaudit.Filter {
	f.CursorTime, f.CursorID = nil, ""
	if c == nil {
		return f
	}
	t := c.T
	f.CursorTime = &t
	switch {
	case source == c.S:
		f.CursorID = c.I
	case source < c.S:
		f.CursorID = adminaudit.CursorMax
	default:
		f.CursorID = ""
	}
	return f
}
