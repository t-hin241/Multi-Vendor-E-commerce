package usecase

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"shopee/backend/pkg/casesla"
)

type WorkItemSource struct {
	SourceStatus
	Page *casesla.Page `json:"page"`
}
type WorkItems struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Sources     []WorkItemSource `json:"sources"`
}

// WorkItems uses independent owner cursors. Unavailable/missing sources
// explicitly have a null page; they never masquerade as an empty queue.
func (s Service) WorkItems(ctx context.Context, adminID, authorization string, q url.Values) (*WorkItems, error) {
	if e := s.requireAdmin(ctx, adminID); e != nil {
		return nil, e
	}
	var calls []call
	for _, owner := range []struct{ name, path string }{{"order", "/api/orders/admin/work-items"}, {"payment", "/api/payments/admin/work-items"}, {"shipment", "/api/shipments/admin/work-items"},
		// PW-045: shop notices nobody received.
		{"notification", "/api/notifications/admin/work-items"}} {
		query := url.Values{}
		for _, k := range []string{"status", "assignee_id", "limit"} {
			if v := q.Get(k); v != "" {
				query.Set(k, v)
			}
		}
		query.Set("cursor", q.Get(owner.name+"_cursor"))
		if _, e := casesla.ParseFilter(query); e != nil {
			return nil, e
		}
		calls = append(calls, call{source: owner.name, path: owner.path, query: query})
	}
	results := s.fetch(ctx, authorization, calls)
	out := &WorkItems{GeneratedAt: s.now(), Sources: []WorkItemSource{}}
	for _, c := range calls {
		r, ok := results[c.source]
		if !ok {
			r.status = SourceStatus{Name: c.source, Status: statusUnavailable, Error: "source not configured"}
		}
		source := WorkItemSource{SourceStatus: r.status}
		if r.status.Status == statusOK {
			var p casesla.Page
			if e := json.Unmarshal(r.data, &p); e != nil || p.Items == nil || p.GeneratedAt.IsZero() {
				source.Status = statusUnavailable
				source.Error = "unreadable work items"
			} else {
				source.Page = &p
				source.GeneratedAt = &p.GeneratedAt
			}
		}
		out.Sources = append(out.Sources, source)
	}
	return out, nil
}
