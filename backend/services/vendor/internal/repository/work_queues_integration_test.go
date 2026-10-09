package repository_test

import (
	"testing"

	"shopee/backend/services/vendorsvc/internal/repository"
)

// PW-008: every operator queue reads on the real schema and an empty
// database needs nobody.
func TestWorkQueuesReadOnTheSchema(t *testing.T) {
	pool := setup(t).db
	for _, q := range repository.WorkQueues {
		var items int64
		var oldest float64
		if err := pool.QueryRow(t.Context(), q.SQL).Scan(&items, &oldest); err != nil {
			t.Fatalf("%s: %v", q.Name, err)
		}
		if q.Severity != "critical" && q.Severity != "warning" {
			t.Fatalf("%s: severity %q", q.Name, q.Severity)
		}
		if items != 0 || oldest != 0 {
			t.Fatalf("%s: an empty database needs nobody, got %d (%vs)", q.Name, items, oldest)
		}

	}
}
