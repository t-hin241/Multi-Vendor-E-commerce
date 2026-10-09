package policyrules

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The contract answers 400 without key and value, and never carries a
// hash with a not-ready answer.
func TestHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/r", Handler(func(key, value string) (bool, string, string) {
		if value == "ok" {
			return true, Hash("impl", key, value), ""
		}
		return false, "leaked", "not enforced"
	}))
	get := func(q string) (int, map[string]any) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/r"+q, nil))
		var out struct{ Data map[string]any }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.Data
	}
	if code, _ := get("?key=a"); code != 400 {
		t.Fatalf("missing value: %d", code)
	}
	if code, d := get("?key=a&value=ok"); code != 200 || d["ready"] != true || d["rule_hash"] != Hash("impl", "a", "ok") {
		t.Fatalf("ready: %d %v", code, d)
	}
	if _, d := get("?key=a&value=no"); d["ready"] != false || d["rule_hash"] != "" || d["reason"] != "not enforced" {
		t.Fatalf("not ready must carry no hash: %v", d)
	}
	if Hash("impl", "a", "ok") == Hash("impl2", "a", "ok") {
		t.Fatal("a new implementation changes the hash")
	}
}
