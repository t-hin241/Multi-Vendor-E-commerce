package domain

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"shopee/backend/pkg/apperror"
)

// AF-18: a vacation is a selling-availability interval, separate from
// moderation. A shop on vacation keeps its status (approved or suspended)
// and its existing orders; only new checkouts stop.

type IntervalStatus string

const (
	IntervalScheduled IntervalStatus = "scheduled"
	IntervalActive    IntervalStatus = "active"
	IntervalEnded     IntervalStatus = "ended"
	IntervalCancelled IntervalStatus = "cancelled"
)

// Vacation limits (v1): start now or up to MaxVacationLead ahead; an end,
// when given, is after the start and at most MaxVacationLength later.
const (
	MaxVacationLead    = 365 * 24 * time.Hour
	MaxVacationLength  = 365 * 24 * time.Hour
	MaxVacationMessage = 500
	// StartSkew tolerates a client clock slightly behind: a start up to this
	// far in the past means "now"; further back is refused.
	StartSkew = 5 * time.Minute
)

// AvailabilityInterval is one vacation: [StartsAt, EndsAt) in UTC, EndsAt
// nil until the owner resumes.
type AvailabilityInterval struct {
	ID             string
	VendorID       string
	Status         IntervalStatus
	StartsAt       time.Time
	EndsAt         *time.Time
	Message        string
	CreatedBy      string
	UpdatedBy      *string
	PausedVersion  *int64
	ResumedVersion *int64
	PausedAt       *time.Time
	ClosedAt       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Open is a scheduled or active interval (at most one per shop).
func (i *AvailabilityInterval) Open() bool {
	return i.Status == IntervalScheduled || i.Status == IntervalActive
}

// VacationInput is an owner's requested schedule. StartsAt nil = now.
type VacationInput struct {
	StartsAt        *time.Time
	EndsAt          *time.Time
	Message         string
	ExpectedVersion int64
}

func availabilityError(status int, code apperror.Code, message string) *apperror.Error {
	return &apperror.Error{Code: code, Message: message, Status: status}
}

// ErrInvalidVacation is 422: the schedule itself is not acceptable.
func ErrInvalidVacation(message string) *apperror.Error {
	return availabilityError(http.StatusUnprocessableEntity, "invalid_schedule", message)
}

// ErrAvailabilityVersion: someone else changed the schedule first.
var ErrAvailabilityVersion = availabilityError(http.StatusConflict, "version_conflict",
	"The vacation schedule changed; reload before retrying")

// NormalizeVacation validates in against now and returns the effective
// start (UTC, clamped to now when it is now or slightly behind), the end
// and the trimmed message. current is the open interval being edited, if
// any: an active one keeps its start.
func NormalizeVacation(in VacationInput, current *AvailabilityInterval, now time.Time) (start time.Time, end *time.Time, message string, err error) {
	now = now.UTC()
	message = strings.TrimSpace(in.Message)
	if !utf8.ValidString(message) || utf8.RuneCountInString(message) > MaxVacationMessage {
		return time.Time{}, nil, "", ErrInvalidVacation("The message must be at most 500 characters")
	}
	if strings.ContainsAny(message, "<>") {
		return time.Time{}, nil, "", ErrInvalidVacation("The message is plain text; remove < and >")
	}
	switch {
	case current != nil && current.Status == IntervalActive:
		// Already paused: the start is history; only end and message change.
		if in.StartsAt != nil && !in.StartsAt.UTC().Equal(current.StartsAt) {
			return time.Time{}, nil, "", ErrInvalidVacation("The vacation has started; its start cannot change")
		}
		start = current.StartsAt
	case in.StartsAt == nil:
		start = now
	default:
		start = in.StartsAt.UTC()
		if start.Before(now.Add(-StartSkew)) {
			return time.Time{}, nil, "", ErrInvalidVacation("The start is in the past")
		}
		if start.Before(now) {
			start = now
		}
		if start.After(now.Add(MaxVacationLead)) {
			return time.Time{}, nil, "", ErrInvalidVacation("The start must be within 365 days")
		}
	}
	if in.EndsAt != nil {
		e := in.EndsAt.UTC()
		if !e.After(start) {
			return time.Time{}, nil, "", ErrInvalidVacation("The end must be after the start")
		}
		if !e.After(now) {
			return time.Time{}, nil, "", ErrInvalidVacation("The end must be in the future")
		}
		if e.Sub(start) > MaxVacationLength {
			return time.Time{}, nil, "", ErrInvalidVacation("A vacation lasts at most 365 days")
		}
		end = &e
	}
	return start, end, message, nil
}

// MayScheduleVacation: only a shop that sells or could sell again
// (approved, or suspended by an admin) has a vacation; ending it never
// lifts a suspension.
func MayScheduleVacation(s Status) bool {
	return s == StatusApproved || s == StatusSuspended
}

// Availability is what a shop shows about its vacation.
type Availability struct {
	VendorID            string
	Status              Status
	AcceptingOrders     bool
	AvailabilityVersion int64
	SaleVersion         int64
	EnforcedVersion     int64
	Current             *AvailabilityInterval
}

// EnforcementPending: the latest selling change has not reached the
// consumers' durable stream yet (Vendor's outbox has not delivered it).
func (a *Availability) EnforcementPending() bool { return a.EnforcedVersion < a.SaleVersion }

// NextOpenAt is when a paused shop expects to take orders again, if it set
// an end.
func (a *Availability) NextOpenAt() *time.Time {
	if a.Current == nil || a.Current.Status != IntervalActive {
		return nil
	}
	return a.Current.EndsAt
}
