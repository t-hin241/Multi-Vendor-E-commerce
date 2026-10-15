package bulkimport

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func multipartBody(t *testing.T, field string, size int) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	_ = w.WriteField("vendor_id", "00000000-0000-0000-0000-000000000001")
	_ = w.WriteField("kind", "prices")
	_ = w.WriteField("template_version", "1")
	if field != "" {
		f, err := w.CreateFormFile(field, "a.csv")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(bytes.Repeat([]byte("a"), size))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &b, w.FormDataContentType()
}

func TestUploadHandlerLimitsBeforeTheUseCase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// No store: these requests must be refused before reaching it.
	Register(r.Group("/v"), r.Group("/a"), Handler{S: &Service{MaxBytes: 1024}, Log: zerolog.Nop()})
	serve := func(method, path string, body *bytes.Buffer, ct string) (int, string) {
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return w.Code, env.Error.Code
	}
	body, ct := multipartBody(t, "file", 2048)
	if status, code := serve("POST", "/v/import-jobs", body, ct); status != 422 || code != "file_invalid" {
		t.Fatalf("oversized file: %d %s", status, code)
	}
	body, ct = multipartBody(t, "", 0)
	if status, _ := serve("POST", "/v/import-jobs", body, ct); status != 400 {
		t.Fatalf("missing file: %d", status)
	}
	if status, _ := serve("POST", "/v/import-jobs/00000000-0000-0000-0000-000000000001/confirm", bytes.NewBufferString(`{"preview_hash":""}`), "application/json"); status != 400 {
		t.Fatalf("empty confirm: %d", status)
	}
	if status, _ := serve("POST", "/a/import-jobs/x/cancellations", bytes.NewBufferString(`not json`), "application/json"); status != 400 {
		t.Fatalf("admin cancel without reason: %d", status)
	}
	if status, _ := serve("GET", "/v/import-jobs?limit=abc", bytes.NewBufferString(""), ""); status != 400 {
		t.Fatalf("bad limit: %d", status)
	}
}
