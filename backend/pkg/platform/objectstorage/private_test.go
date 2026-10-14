package objectstorage

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Runs only against the isolated, disposable MinIO test instance. All
// credentials below are explicit fake fixtures, never application secrets.
func TestPrivateStreamingAndSignedDownload(t *testing.T) {
	endpoint := os.Getenv("PRIVATE_STORAGE_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("PRIVATE_STORAGE_TEST_ENDPOINT not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	c, err := NewPrivateClient(ctx, PrivateConfig{Endpoint: endpoint, AccessKey: "fake-af14-access", SecretKey: "fake-af14-secret-not-real", Bucket: "af14-private-test"})
	if err != nil {
		t.Fatal(err)
	}
	key := "test-statement.csv"
	body := "id,amount\nexample,-50\n"
	if err := c.PutStream(ctx, key, strings.NewReader(body), "text/csv"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Delete(ctx, key); err != nil {
			t.Error(err)
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}
	get := func(raw string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("private object request failed")
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(b)
	}
	if status, _ := get("http://" + endpoint + "/af14-private-test/" + key); status != 403 {
		t.Fatalf("anonymous object readable: %d", status)
	}
	u, err := c.DownloadURL(ctx, key, time.Minute)
	if err != nil {
		t.Fatal("could not sign download")
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal("invalid signed download")
	}
	if parsed.Query().Get("X-Amz-Expires") != "60" {
		t.Fatal("wrong expiry")
	}
	if status, got := get(u); status != 200 || got != body {
		t.Fatalf("signed object mismatch, status=%d", status)
	}
}
