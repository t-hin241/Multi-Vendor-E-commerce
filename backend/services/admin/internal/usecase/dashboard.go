package usecase

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// opsSources is where each service reports its operations counters (the
// same endpoints its own admin screen uses).
var opsSources = []call{
	{source: "vendor", path: "/api/vendor/admin/operations"},
	{source: "catalog", path: "/api/catalog/operations"},
	{source: "inventory", path: "/api/inventory/admin/operations"},
	{source: "order", path: "/api/orders/admin/operations", query: url.Values{"limit": {"1"}}},
	{source: "payment", path: "/api/payments/admin/reconciliation"},
	{source: "shipment", path: "/api/shipments/admin/operations"},
	{source: "notification", path: "/api/notifications/admin/operations"},
	{source: "review", path: "/api/reviews/admin/operations"},
	// PLT-03: each consumer's parked events (not applied, waiting for an
	// operator to replay or discard).
	{source: "catalog-events", upstream: "catalog", path: "/api/catalog/admin/events/stats"},
	{source: "order-events", upstream: "order", path: "/api/orders/admin/events/stats"},
	{source: "payment-events", upstream: "payment", path: "/api/payments/admin/events/stats"},
	{source: "shipment-events", upstream: "shipment", path: "/api/shipments/admin/events/stats"},
	{source: "notification-events", upstream: "notification", path: "/api/notifications/admin/events/stats"},
}

type counterRef struct{ source, key string }

type tileDef struct {
	key, group, label, hint, link string
	counters                      []counterRef
	// informational tiles show normal workload, not a problem.
	informational bool
}

// tiles are the dashboard's questions. Each count comes from the service
// that owns it; nothing is derived from another service's labels.
var tiles = []tileDef{
	{"vendors_pending", "Moderation", "Shops waiting for approval", "Approve or reject with a reason.", "/admin/vendors",
		[]counterRef{{"vendor", "pending_applications"}}, false},
	{"products_pending", "Moderation", "Products waiting for review", "Approve or reject with a reason.", "/admin/products",
		[]counterRef{{"catalog", "pending_review"}}, false},

	{"awaiting_shipment", "Fulfillment", "Packages paid, not shipped yet", "Normal workload of the shops.", "/admin/orders",
		[]counterRef{{"order", "awaiting_shipment"}}, true},
	{"shipping_late", "Fulfillment", "Paid packages not shipped for 3+ days", "Chase the shop, or cancel with a refund.", "/admin/fulfillment",
		[]counterRef{{"shipment", "fulfillment_lag"}}, false},
	{"tracking_stale", "Fulfillment", "In transit without update for 7+ days", "Ask the carrier and record the outcome.", "/admin/fulfillment",
		[]counterRef{{"shipment", "tracking_stale"}}, false},
	{"interceptions_open", "Fulfillment", "Interceptions without a decision", "Record what the carrier answered.", "/admin/fulfillment",
		[]counterRef{{"shipment", "interception_pending"}}, false},

	{"captures_not_applied", "Money", "Captured payments not applied to an order", "Retry the receipt or the order sync; never edit the database.", "/admin/payments",
		[]counterRef{{"payment", "order_sync_review"}, {"payment", "receipts_parked"}, {"payment", "receipts_unapplied"}}, false},
	{"captures_rejected", "Money", "Captures Order refused, not refunded yet", "Refund the payment from the order page.", "/admin/orders",
		[]counterRef{{"order", "payment_exceptions"}}, false},
	{"payments_stuck", "Money", "Payment links stuck while being created", "Reconcile the intent with the provider.", "/admin/payments",
		[]counterRef{{"payment", "intents_stuck_creating"}}, false},
	{"refunds_pending", "Money", "Refunds not yet confirmed", "Record the provider or bank reference when paid.", "/admin/refunds",
		[]counterRef{{"payment", "refunds_pending"}, {"payment", "refunds_awaiting_provider"}}, false},
	{"return_refunds_failed", "Money", "Return refunds that failed", "Retry the refund with a reason.", "/admin/returns",
		[]counterRef{{"order", "return_refunds_failed"}}, false},

	{"support_cases_unassigned", "Support", "Support cases nobody has picked up", "Assign the case to an admin.", "/admin/support",
		[]counterRef{{"order", "support_cases_unassigned"}}, false},
	{"support_cases_overdue", "Support", "Support cases past their deadline", "Answer the buyer or chase the shop.", "/admin/support",
		[]counterRef{{"order", "support_cases_overdue"}}, false},
	{"support_cases_waiting_money", "Support", "Support cases waiting for a refund or return", "Follow the linked refund or return.", "/admin/support",
		[]counterRef{{"order", "support_cases_resolution_pending"}}, true},

	{"holds_expired", "Stock", "Expired stock holds not released", "Expiry runs every minute; a growing number needs a look.", "/admin/requests",
		[]counterRef{{"inventory", "overdue"}, {"inventory", "expiry_parked"}}, false},
	{"stock_mismatches", "Stock", "Stock holds that disagree with Order", "Repair from the inventory operations list.", "/admin/requests",
		[]counterRef{{"inventory", "order_mismatches"}, {"inventory", "reserved_mismatches"}}, false},

	{"order_jobs_failed", "Background jobs", "Order side effects stopped after retries", "Fix the cause, then replay with a reason.", "/admin/orders",
		[]counterRef{{"order", "parked"}}, false},
	{"vendor_events_failed", "Background jobs", "Shop status updates not delivered", "Replay from the shop list with a reason.", "/admin/vendors",
		[]counterRef{{"vendor", "parked_status_events"}}, false},
	{"catalog_jobs_failed", "Background jobs", "Catalog updates or file clean-ups stopped", "Replay through the catalog operations API (runbook).", "/admin/products",
		[]counterRef{{"catalog", "status_parked"}, {"catalog", "cleanup_parked"}}, false},
	{"inventory_events_failed", "Background jobs", "Stock events or cache updates stopped", "See inventory operations.", "/admin/requests",
		[]counterRef{{"inventory", "events_parked"}, {"inventory", "cache_parked"}}, false},
	{"shipment_events_review", "Background jobs", "Shipment events Order refused", "Check the order, then resend.", "/admin/fulfillment",
		[]counterRef{{"shipment", "order_events_review"}}, false},

	{"notifications_parked", "Notifications", "Emails stopped after retries", "Fix the mail provider, then retry with a reason.", "/admin/notifications",
		[]counterRef{{"notification", "parked"}}, false},
	{"notifications_late", "Notifications", "Emails waiting more than 15 minutes", "The mail provider may be down or slow.", "/admin/notifications",
		[]counterRef{{"notification", "pending_over_15m"}}, false},
	{"notifications_failed", "Notifications", "Emails refused in the last 24 hours", "Unknown or refused recipient; not retried automatically.", "/admin/notifications",
		[]counterRef{{"notification", "failed_24h"}}, false},
	{"notifications_queue_down", "Notifications", "Delivery queue (Redis) unreachable", "Requests are kept in PostgreSQL and requeued once Redis is back.", "/admin/notifications",
		[]counterRef{{"notification", "queue_unreachable"}}, false},
	{"review_reports_open", "Moderation", "Review reports waiting for a decision", "Keep or hide with a reason.", "/admin/reviews",
		[]counterRef{{"review", "open_reports"}}, false},
	{"review_uploads_stuck", "Background jobs", "Review photos left behind by failed uploads", "Check object storage (review runbook).", "/admin/reviews",
		[]counterRef{{"review", "image_cleanup_parked"}}, false},
	{"events_parked", "Background jobs", "Events a service could not apply", "Replay or discard with a reason (platform runbook).", "/admin/events",
		[]counterRef{{"catalog-events", "events_parked"}, {"order-events", "events_parked"}, {"payment-events", "events_parked"},
			{"shipment-events", "events_parked"}, {"notification-events", "events_parked"}}, false},
	{"notices_not_handed_over", "Notifications", "Shop decision notices Notification refused", "See the vendor notice outbox (runbook).", "/admin/notifications",
		[]counterRef{{"vendor", "parked_notices"}}, false},
}

// Tile is one dashboard figure. Count is null when a service it depends on
// did not answer: an unavailable figure is never shown as zero.
type Tile struct {
	Key     string   `json:"key"`
	Group   string   `json:"group"`
	Label   string   `json:"label"`
	Hint    string   `json:"hint"`
	Link    string   `json:"link"`
	Count   *int64   `json:"count"`
	Status  string   `json:"status"` // ok | attention | unavailable
	Sources []string `json:"sources"`
}

type Dashboard struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Sources     []SourceStatus `json:"sources"`
	Tiles       []Tile         `json:"tiles"`
}

// Dashboard reads every service's operations counters in parallel and
// builds the tiles, marking those whose service did not answer.
func (s Service) Dashboard(ctx context.Context, adminID, authorization string) (*Dashboard, error) {
	if err := s.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	results := s.fetch(ctx, authorization, opsSources)
	counters := map[string]map[string]int64{}
	out := &Dashboard{GeneratedAt: s.now()}
	for _, c := range opsSources {
		r, ok := results[c.source]
		if !ok {
			continue // service not configured
		}
		if r.status.Status == statusOK {
			values, generated, err := numericCounters(r.data)
			if err != nil {
				r.status.Status, r.status.Error, r.status.FetchedAt = statusUnavailable, "unreadable counters", nil
			} else {
				counters[c.source], r.status.GeneratedAt = values, generated
			}
		}
		out.Sources = append(out.Sources, r.status)
	}
	for _, d := range tiles {
		if !configured(results, d.counters) {
			continue // e.g. reviews disabled
		}
		t := Tile{Key: d.key, Group: d.group, Label: d.label, Hint: d.hint, Link: d.link, Status: "ok"}
		var total int64
		available := true
		for _, ref := range d.counters {
			t.Sources = appendOnce(t.Sources, ref.source)
			values, ok := counters[ref.source]
			v, found := values[ref.key]
			if !ok || !found {
				available = false
				continue
			}
			total += v
		}
		switch {
		case !available:
			t.Status = statusUnavailable
		case total > 0 && !d.informational:
			t.Status, t.Count = "attention", &total
		default:
			t.Count = &total
		}
		out.Tiles = append(out.Tiles, t)
	}
	return out, nil
}

// configured reports whether every service a tile reads is configured.
func configured(results map[string]result, refs []counterRef) bool {
	for _, ref := range refs {
		if _, ok := results[ref.source]; !ok {
			return false
		}
	}
	return true
}

// numericCounters reads the numbers a service reports: top-level numbers
// and the members of a "counts" object, plus "generated_at" if present.
func numericCounters(data json.RawMessage) (map[string]int64, *time.Time, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, nil, err
	}
	out := map[string]int64{}
	var generated *time.Time
	for k, raw := range top {
		var n json.Number
		if err := json.Unmarshal(raw, &n); err == nil {
			if v, err := n.Int64(); err == nil {
				out[k] = v
			} else if f, err := n.Float64(); err == nil {
				out[k] = int64(f)
			}
			continue
		}
		switch k {
		case "counts":
			var nested map[string]int64
			if err := json.Unmarshal(raw, &nested); err == nil {
				for nk, nv := range nested {
					out[nk] = nv
				}
			}
		case "generated_at":
			var t time.Time
			if err := json.Unmarshal(raw, &t); err == nil {
				generated = &t
			}
		}
	}
	return out, generated, nil
}

func appendOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
