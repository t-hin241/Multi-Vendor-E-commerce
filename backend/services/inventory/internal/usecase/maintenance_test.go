package usecase_test

import (
	"testing"

	"shopee/backend/services/inventory/internal/usecase"
)

func TestAlertCountersOnlyReportsActionableDrift(t *testing.T) {
	stats := map[string]int64{
		"held": 40, "legacy_held": 2, "events_pending": 3, "cache_pending": 7,
		"overdue": 1, "reserved_mismatches": 2, "events_parked": 0,
	}
	got := usecase.AlertCounters(stats)
	if len(got) != 2 || got["overdue"] != 1 || got["reserved_mismatches"] != 2 {
		t.Fatalf("unexpected alerts %v", got)
	}
	if len(usecase.AlertCounters(map[string]int64{"held": 5})) != 0 {
		t.Fatal("normal holds must not alert")
	}
}
