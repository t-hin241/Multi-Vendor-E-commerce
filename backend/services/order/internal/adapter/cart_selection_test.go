package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"shopee/backend/pkg/cartselection"
)

func TestSelectedSnapshotFailsClosedWithOldCart(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000001"
	var includeHash atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs []string `json:"selected_line_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.IDs) != 1 || body.IDs[0] != id {
			t.Errorf("missing selection: %+v", body)
		}
		data := map[string]any{"operation_id": "op", "cart_version": 1, "lines": []map[string]any{{"line_id": id, "product_id": "p", "quantity": 2}}}
		if includeHash.Load() {
			data["selection_hash"] = cartselection.Hash(body.IDs)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	c := NewHTTPCartClient(s.URL, "fake-test-key-not-a-secret")
	version := int64(1)
	if _, err := c.Snapshot(t.Context(), "buyer", "op", &version, []string{id}); err == nil {
		t.Fatal("old Cart silently ignored selection")
	}
	includeHash.Store(true)
	snap, err := c.Snapshot(t.Context(), "buyer", "op", &version, []string{id})
	if err != nil || len(snap.Lines) != 1 || snap.Lines[0].Quantity != 2 {
		t.Fatalf("selected contract: %+v %v", snap, err)
	}
}
