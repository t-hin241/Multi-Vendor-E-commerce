package cartselection

import (
	"encoding/json"
	"testing"
)

func TestSelectionWireContract(t *testing.T) {
	const a = "00000000-0000-0000-0000-000000000001"
	const b = "00000000-0000-0000-0000-000000000002"
	for _, body := range []string{`{"selected_line_ids":null}`, `{"selected_line_ids":[]}`, `{"selected_line_ids":["bad"]}`, `{"selected_line_ids":["` + a + `","` + a + `"]}`} {
		var in struct {
			IDs IDs `json:"selected_line_ids"`
		}
		if err := json.Unmarshal([]byte(body), &in); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	var legacy struct {
		IDs IDs `json:"selected_line_ids"`
	}
	if err := json.Unmarshal([]byte(`{}`), &legacy); err != nil || legacy.IDs != nil {
		t.Fatal("absent must mean legacy all-cart")
	}
	if Hash([]string{a, b}) != Hash([]string{b, a}) || Hash(nil) != "" || Hash([]string{a}) == Hash([]string{b}) {
		t.Fatal("invalid canonical hash")
	}
	ids := make([]string, 51)
	if Validate(ids) == nil {
		t.Fatal("limit not enforced")
	}
}
