package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
)

func TestVacationTimeBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	instant := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	for _, tc := range []struct {
		name       string
		start, end *time.Time
		invalid    bool
	}{
		{"immediate", nil, nil, false},
		{"starts exactly now", instant(0), instant(time.Hour), false},
		{"clock skew boundary", instant(-StartSkew), nil, false},
		{"clock skew exceeded", instant(-StartSkew - time.Nanosecond), nil, true},
		{"lead boundary", instant(MaxVacationLead), nil, false},
		{"lead exceeded", instant(MaxVacationLead + time.Nanosecond), nil, true},
		{"end equals start", instant(time.Hour), instant(time.Hour), true},
		{"end equals now", nil, instant(0), true},
		{"end before start", instant(time.Hour), instant(time.Minute), true},
		{"length boundary", nil, instant(MaxVacationLength), false},
		{"length exceeded", nil, instant(MaxVacationLength + time.Nanosecond), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end, message, err := NormalizeVacation(VacationInput{StartsAt: tc.start, EndsAt: tc.end, Message: "  Nghỉ lễ  "}, nil, now)
			if tc.invalid {
				var app *apperror.Error
				if !errors.As(err, &app) || app.Code != "invalid_schedule" || app.Status != 422 {
					t.Fatalf("invalid schedule: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := now
			if tc.start != nil && tc.start.After(now) {
				want = *tc.start
			}
			if !start.Equal(want) || start.Location() != time.UTC || message != "Nghỉ lễ" {
				t.Fatalf("normalization: %v %q", start, message)
			}
			if (tc.end == nil) != (end == nil) || end != nil && (!end.Equal(*tc.end) || end.Location() != time.UTC) {
				t.Fatalf("end: %v", end)
			}
		})
	}
}

func TestActiveVacationKeepsHistoricalStart(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	cur := &AvailabilityInterval{Status: IntervalActive, StartsAt: now.Add(-24 * time.Hour)}
	end := now.Add(time.Hour)
	start, _, _, err := NormalizeVacation(VacationInput{EndsAt: &end}, cur, now)
	if err != nil || !start.Equal(cur.StartsAt) {
		t.Fatalf("historical start: %v %v", start, err)
	}
	if _, _, _, err := NormalizeVacation(VacationInput{StartsAt: &now}, cur, now); err == nil {
		t.Fatal("changed active start")
	}
	if _, _, _, err := NormalizeVacation(VacationInput{EndsAt: &now}, cur, now); err == nil {
		t.Fatal("accepted elapsed end")
	}
	for _, s := range []Status{StatusPending, StatusRejected} {
		if MayScheduleVacation(s) {
			t.Fatalf("allowed %s", s)
		}
	}
	for _, s := range []Status{StatusApproved, StatusSuspended} {
		if !MayScheduleVacation(s) {
			t.Fatalf("refused %s", s)
		}
	}
}

func TestVacationMessageCountsUnicodeCharacters(t *testing.T) {
	for _, tc := range []struct {
		message string
		valid   bool
	}{
		{strings.Repeat("ế", 500), true}, {strings.Repeat("ế", 501), false}, {"<script>", false}, {string([]byte{0xff}), false},
	} {
		_, _, _, err := NormalizeVacation(VacationInput{Message: tc.message}, nil, time.Now())
		if (err == nil) != tc.valid {
			t.Fatalf("valid=%v: %v", tc.valid, err)
		}
	}
}
