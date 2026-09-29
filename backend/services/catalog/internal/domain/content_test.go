package domain_test

import (
	"bytes"
	"image"
	"image/png"
	"shopee/backend/services/catalog/internal/domain"
	"strings"
	"testing"
)

func TestMediaContentValidation(t *testing.T) {
	var valid, large bytes.Buffer
	if err := png.Encode(&valid, image.NewGray(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&large, image.NewGray(image.Rect(0, 0, 8193, 1))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mime string
		data       []byte
		fail       bool
	}{
		{"valid PNG", "image/png", valid.Bytes(), false},
		{"MIME spoof", "image/jpeg", valid.Bytes(), true},
		{"script upload", "image/png", []byte("<script>alert(1)</script>"), true},
		{"truncated image", "image/png", valid.Bytes()[:40], true},
		{"oversized dimension", "image/png", large.Bytes(), true},
		{"oversized file", "image/png", make([]byte, 5*1024*1024+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := domain.ValidateMediaBytes(tc.mime, tc.data, true)
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestDescriptionSanitizationAndURLPolicy(t *testing.T) {
	input := `<p onclick="bad()">Hello <strong>buyer</strong></p><script>alert(1)</script><svg onload="bad()"></svg><a href="javascript:bad()">link</a><img src="data:text/html,bad"><iframe src="https://example.test"></iframe>`
	output := domain.SanitizeDescription(input)
	if output != `<p>Hello <strong>buyer</strong></p>link` {
		t.Fatalf("unexpected sanitized content: %s", output)
	}
	if domain.SanitizeDescription(output) != output {
		t.Fatal("sanitization is not idempotent")
	}
	for _, raw := range []string{"javascript:alert(1)", "//example.test/a", "data:image/png;base64,x", "https://user:pass@example.test/a"} {
		if domain.SafeMediaURL(raw) {
			t.Fatal("unsafe URL allowed")
		}
	}
	if !domain.SafeMediaURL("https://media.example.test/products/test.png") {
		t.Fatal("valid media URL rejected")
	}
	if strings.Contains(domain.SanitizeDescription(`<math><mtext><script>x</script></mtext></math>`), "script") {
		t.Fatal("foreign executable element retained")
	}
}
