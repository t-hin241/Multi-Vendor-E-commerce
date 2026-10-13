package transport

import (
	"testing"

	"shopee/backend/pkg/serviceauth"
)

func TestSnapshotSelectionValidation(t *testing.T) {
	r, _ := newTestRouter()
	for _, selection := range []string{`null`, `[]`, `["not-an-id"]`, `["` + someUUID + `","` + someUUID + `"]`} {
		body := `{"operation_id":"` + someUUID + `","selected_line_ids":` + selection + `}`
		if status := serve(r, "POST", "/internal/carts/"+someUUID+"/checkout-snapshots", body, map[string]string{serviceauth.Header: testServiceKey}); status != 422 {
			t.Errorf("%s: status %d", selection, status)
		}
	}
}
