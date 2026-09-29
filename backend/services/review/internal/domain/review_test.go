package domain

import "testing"

func TestValidateReview(t *testing.T) {
	t.Run("normalizes a valid comment", func(t *testing.T) {
		comment, err := ValidateReview(5, "  useful product  ")
		if err != nil || comment != "useful product" {
			t.Fatalf("ValidateReview() = %q, %v", comment, err)
		}
	})
	for _, tc := range []struct {
		name    string
		rating  int
		comment string
	}{
		{"rating below range", 0, "comment"},
		{"rating above range", 6, "comment"},
		{"empty comment", 4, "  "},
		{"comment is too long", 4, string(make([]rune, 2001))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateReview(tc.rating, tc.comment); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateImage(t *testing.T) {
	if ext, err := ValidateImage("image/webp", 1); err != nil || ext != ".webp" {
		t.Fatalf("ValidateImage() = %q, %v", ext, err)
	}
	if _, err := ValidateImage("image/gif", 1); err == nil {
		t.Fatal("expected unsupported MIME type to be rejected")
	}
	if _, err := ValidateImage("image/jpeg", 5*1024*1024+1); err == nil {
		t.Fatal("expected oversized image to be rejected")
	}
}
