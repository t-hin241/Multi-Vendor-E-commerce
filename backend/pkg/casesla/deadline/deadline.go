// Package deadline implements versioned service-owned response deadlines.
// It never changes the lifecycle of the resource, money, or inventory.
package deadline

import (
	"time"

	"shopee/backend/pkg/apperror"
)

const PolicyVersion = "case-sla-v1"
const (
	SupportAcknowledgement = 24 * time.Hour
	VendorResponse         = 48 * time.Hour
	StopDelivery           = 4 * time.Hour
	ReturnDecision         = 48 * time.Hour
	ManualRefundReady      = 72 * time.Hour
	OverallLimit           = 7 * 24 * time.Hour
)

type Item struct {
	ID              string     `json:"id"`
	ResourceType    string     `json:"resource_type"`
	ResourceID      string     `json:"resource_id"`
	Stage           string     `json:"stage"`
	WaitingOn       string     `json:"waiting_on"`
	PolicyVersion   string     `json:"sla_policy_version"`
	StageStartedAt  time.Time  `json:"stage_started_at"`
	DueAt           time.Time  `json:"due_at"`
	ReminderAt      time.Time  `json:"reminder_at"`
	OverallDueAt    time.Time  `json:"overall_due_at"`
	PausedAt        *time.Time `json:"paused_at,omitempty"`
	AssigneeID      *string    `json:"assignee_id"`
	DeadlineVersion int64      `json:"deadline_version"`
	Version         int64      `json:"version"`
	Active          bool       `json:"active"`
	Legacy          bool       `json:"legacy"`
	BreachedAt      *time.Time `json:"breached_at,omitempty"`
	NeedsAttention  bool       `json:"needs_attention"`
	URL             string     `json:"url"`
	CreatedAt       time.Time  `json:"created_at"`
}

// StageInput is supplied by the owning domain, inside its transaction.
// An empty stage ends the response obligation, not the business resource.
type StageInput struct {
	ResourceType, ResourceID, Stage, WaitingOn, URL string
	Duration                                        time.Duration
	At, CreatedAt                                   time.Time
	Pause                                           bool
	Legacy                                          bool
	AssigneeID                                      *string
	AssignmentChanged                               bool
}

func New(in StageInput) Item {
	start := in.At.UTC()
	i := Item{ResourceType: in.ResourceType, ResourceID: in.ResourceID,
		CreatedAt: in.CreatedAt.UTC(), OverallDueAt: in.CreatedAt.UTC().Add(OverallLimit),
		Version: 1, DeadlineVersion: 1, Legacy: in.Legacy, URL: in.URL}
	i.start(in, start)
	i.AssigneeID = in.AssigneeID
	return i
}

func (i *Item) start(in StageInput, at time.Time) {
	i.Stage, i.WaitingOn, i.PolicyVersion = in.Stage, in.WaitingOn, PolicyVersion
	i.StageStartedAt, i.DueAt = at, at.Add(in.Duration)
	if i.DueAt.After(i.OverallDueAt) {
		i.DueAt = i.OverallDueAt
	}
	i.ReminderAt = at.Add(i.DueAt.Sub(at) * 3 / 4)
	i.PausedAt = nil
	i.Active = in.Stage != ""
	i.NeedsAttention = false
	if in.Pause {
		i.PausedAt = &at
	}
}

// Apply preserves deadlines on repeated writes/messages. Pausing retains
// the operator's remaining time; the seven-day outer deadline never moves.
func (i *Item) Apply(in StageInput) bool {
	breached := false
	if i.Active && i.BreachedAt == nil && !in.At.Before(i.EffectiveDueAt()) {
		at := i.EffectiveDueAt()
		i.BreachedAt = &at
		breached = true
	}
	if in.Stage == "" {
		if !i.Active {
			return false
		}
		i.Active = false
	} else if in.Pause {
		if i.PausedAt != nil && i.Active {
			if breached {
				i.Version++
			}
			return breached
		}
		at := in.At.UTC()
		i.PausedAt, i.WaitingOn = &at, "buyer"
	} else if i.PausedAt != nil && i.Active && i.Stage == in.Stage {
		pause := in.At.Sub(*i.PausedAt)
		if pause < 0 {
			pause = 0
		}
		i.DueAt = i.DueAt.Add(pause)
		i.ReminderAt = i.ReminderAt.Add(pause)
		if i.DueAt.After(i.OverallDueAt) {
			i.DueAt = i.OverallDueAt
		}
		if i.ReminderAt.After(i.DueAt) {
			i.ReminderAt = i.DueAt
		}
		i.PausedAt, i.WaitingOn = nil, in.WaitingOn
		// Resume the same operator budget, even though the business status
		// changed from waiting_buyer to in_progress.
		i.Stage = in.Stage
	} else if i.Stage != in.Stage || !i.Active {
		i.start(in, in.At.UTC())
		i.Legacy = false
	} else {
		if breached {
			i.Version++
		}
		return breached
	}
	i.Version++
	i.DeadlineVersion++
	return true
}

func (i Item) EffectiveDueAt() time.Time {
	if i.PausedAt != nil || i.DueAt.After(i.OverallDueAt) {
		return i.OverallDueAt
	}
	return i.DueAt
}

func (i *Item) Extend(at, due time.Time) error {
	if !i.Active || i.PausedAt != nil || !due.After(i.DueAt) || !due.After(at) || due.After(i.OverallDueAt) {
		return &apperror.Error{Code: "invalid_extension", Status: 409, Message: "Extension must advance an active deadline within the original seven-day limit"}
	}
	if !at.Before(i.DueAt) && i.BreachedAt == nil {
		old := i.DueAt
		i.BreachedAt = &old
	}
	i.DueAt = due.UTC()
	i.ReminderAt = at.Add(due.Sub(at) * 3 / 4).UTC()
	i.DeadlineVersion++
	i.Version++
	i.NeedsAttention = false
	return nil
}

// Notices catches up after downtime. Receipts, not process memory, dedup.
// Each overdue deadline has at most three daily escalation rounds.
func (i Item) Notices(now time.Time) []string {
	if !i.Active || i.Legacy {
		return nil
	}
	due := i.EffectiveDueAt()
	if now.Before(due) {
		if i.PausedAt == nil && !now.Before(i.ReminderAt) {
			return []string{"reminder"}
		}
		return nil
	}
	n := []string{"overdue"}
	for step := 1; step <= 3; step++ {
		if !now.Before(due.Add(time.Duration(step) * 24 * time.Hour)) {
			n = append(n, []string{"", "escalation_1", "escalation_2", "escalation_3"}[step])
		}
	}
	return n
}
