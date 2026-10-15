// Package bizreport is the AF-20 operational business report contract
// shared by the owner services (Order, Payment, Shipment) and the Admin
// BFF: how a report period, grouping and drilldown are asked for, how a
// rate is stated, and the metric definition version. Each owner computes
// its own figures from its own database; nothing here reads another
// service's data, and no figure is derived from another service's labels.
package bizreport

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
)

// DefinitionVersion names the metric definitions (docs AF-20 §4). A change
// of meaning gets a new version; charts never mix two versions unlabeled.
const DefinitionVersion = "business-report-v1"

// MinSample: a shop ranking row with fewer vendor orders says
// "insufficient data" instead of a rate (never used to penalize a seller).
const MinSample = 20

// ReportZone is the business day: Vietnam time (UTC+7, no daylight saving).
// SQL buckets use ReportZoneName; Go uses the fixed offset.
const ReportZoneName = "Asia/Ho_Chi_Minh"

var ReportZone = time.FixedZone("ICT", 7*3600)

// MaxRange bounds a report period.
const MaxRange = 366 * 24 * time.Hour

// Groupings of the series.
const (
	GroupDay   = "day"
	GroupMonth = "month"
)

// Range is a half-open UTC period [From, To), a grouping, and optionally
// one shop.
type Range struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Group    string    `json:"group"`
	VendorID string    `json:"vendor_id,omitempty"`
}

// InvalidRange is the 422 for a period the report cannot serve.
func InvalidRange(message string) *apperror.Error {
	return &apperror.Error{Code: "invalid_range", Status: http.StatusUnprocessableEntity, Message: message}
}

// Disabled is the answer while FEATURE_BUSINESS_REPORTS_ENABLED is off.
func Disabled() *apperror.Error {
	return &apperror.Error{Code: "feature_disabled", Status: http.StatusNotFound, Message: "Business reports are not enabled"}
}

// ParseRange validates from/to (RFC3339 with an offset), group (day,
// default, or month) and an optional shop id.
func ParseRange(from, to, group, vendorID string) (Range, error) {
	r := Range{Group: group, VendorID: strings.TrimSpace(vendorID)}
	if r.Group == "" {
		r.Group = GroupDay
	}
	if r.Group != GroupDay && r.Group != GroupMonth {
		return r, InvalidRange("group must be day or month")
	}
	var err error
	if r.From, err = time.Parse(time.RFC3339, from); err != nil {
		return r, InvalidRange("from must be an RFC3339 time with a time zone offset")
	}
	if r.To, err = time.Parse(time.RFC3339, to); err != nil {
		return r, InvalidRange("to must be an RFC3339 time with a time zone offset")
	}
	r.From, r.To = r.From.UTC(), r.To.UTC()
	if !r.To.After(r.From) || r.To.Sub(r.From) > MaxRange {
		return r, InvalidRange("The period must be positive and at most 366 days")
	}
	if r.VendorID != "" {
		if _, err := uuid.Parse(r.VendorID); err != nil {
			return r, InvalidRange("vendor_id must be a shop id")
		}
	}
	return r, nil
}

// Ratio states a rate with what it was computed from. Rate is null when
// the denominator is zero (never 0%).
type Ratio struct {
	Numerator   int64    `json:"numerator"`
	Denominator int64    `json:"denominator"`
	Rate        *float64 `json:"rate"`
}

func NewRatio(numerator, denominator int64) Ratio {
	r := Ratio{Numerator: numerator, Denominator: denominator}
	if denominator > 0 {
		v := float64(numerator) / float64(denominator)
		r.Rate = &v
	}
	return r
}

// Drilldown asks an owner for the ids behind one figure, at the cutoff the
// figure was computed at (as_of), newest first.
type Drilldown struct {
	Range  Range
	Metric string
	Cutoff time.Time
	After  *Cursor
	Limit  int
}

// Cursor is the position (created_at, id) of the last row returned.
type Cursor struct {
	At time.Time `json:"t"`
	ID string    `json:"i"`
}

func (c Cursor) Encode() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ParseDrilldown validates a drilldown request; metrics lists the figures
// this owner can drill into.
func ParseDrilldown(r Range, metric, cutoff, cursor, limit string, metrics []string) (Drilldown, error) {
	d := Drilldown{Range: r, Metric: metric, Limit: 20}
	known := false
	for _, m := range metrics {
		known = known || m == metric
	}
	if !known {
		return d, apperror.Validation("metric must be one of " + strings.Join(metrics, ", "))
	}
	var err error
	if d.Cutoff, err = time.Parse(time.RFC3339Nano, cutoff); err != nil {
		return d, apperror.Validation("cutoff must be the report's as_of time")
	}
	d.Cutoff = d.Cutoff.UTC()
	if limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil || n < 1 || n > 100 {
			return d, apperror.Validation("limit must be between 1 and 100")
		}
		d.Limit = n
	}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var c Cursor
		if err != nil || len(raw) > 256 || json.Unmarshal(raw, &c) != nil || c.At.IsZero() {
			return d, apperror.Validation("cursor is invalid")
		}
		if _, err := uuid.Parse(c.ID); err != nil {
			return d, apperror.Validation("cursor is invalid")
		}
		d.After = &c
	}
	return d, nil
}

// Page is one drilldown page. Items are owner-specific rows of ids and the
// facts the figure counted; no buyer personal data.
type Page[T any] struct {
	Metric            string    `json:"metric"`
	Cutoff            time.Time `json:"cutoff"`
	DefinitionVersion string    `json:"definition_version"`
	Items             []T       `json:"items"`
	NextCursor        string    `json:"next_cursor,omitempty"`
}

// NewPage builds a page from limit+1 fetched rows; at reports a row's
// cursor position.
func NewPage[T any](d Drilldown, rows []T, at func(T) Cursor) Page[T] {
	p := Page[T]{Metric: d.Metric, Cutoff: d.Cutoff, DefinitionVersion: DefinitionVersion, Items: rows}
	if len(rows) > d.Limit {
		p.Items = rows[:d.Limit]
		p.NextCursor = at(rows[d.Limit-1]).Encode()
	}
	if p.Items == nil {
		p.Items = []T{}
	}
	return p
}

// CursorArgs returns the (time, id) to page after, or a position after
// every row when this is the first page.
func (d Drilldown) CursorArgs() (time.Time, string) {
	if d.After == nil {
		return d.Cutoff.Add(time.Microsecond), "ffffffff-ffff-ffff-ffff-ffffffffffff"
	}
	return d.After.At, d.After.ID
}
