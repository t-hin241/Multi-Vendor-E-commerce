package httpresponse_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/httpresponse"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestOK_WrapsPayloadInDataEnvelope(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	httpresponse.OK(c, http.StatusOK, gin.H{"id": "123"})

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level %q field, got %v", "data", body)
	}
	if data["id"] != "123" {
		t.Errorf("expected data.id = 123, got %v", data["id"])
	}
}

func TestError_NeverLeaksInternalDetail(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("request_id", "req-1")

	httpresponse.Error(c, http.StatusInternalServerError, "internal_error", "Something went wrong")

	var body map[string]httpresponse.ErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	errBody, ok := body["error"]
	if !ok {
		t.Fatalf("expected top-level %q field, got %v", "error", body)
	}
	if errBody.Code != "internal_error" {
		t.Errorf("expected code internal_error, got %q", errBody.Code)
	}
	if errBody.RequestID != "req-1" {
		t.Errorf("expected request id to be propagated, got %q", errBody.RequestID)
	}
}
