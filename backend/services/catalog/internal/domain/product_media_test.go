package domain_test

import (
	"testing"

	"shopee/backend/services/catalog/internal/domain"
)

func TestValidateMediaUpload(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		size        int64
		wantKind    domain.MediaKind
		wantExt     string
		wantErr     bool
	}{
		{"valid jpeg", "image/jpeg", 1000, domain.MediaKindImage, ".jpg", false},
		{"valid png", "image/png", 1000, domain.MediaKindImage, ".png", false},
		{"valid webp", "image/webp", 1000, domain.MediaKindImage, ".webp", false},
		{"valid mp4", "video/mp4", 10 * 1024 * 1024, domain.MediaKindVideo, ".mp4", false},
		{"valid webm", "video/webm", 10 * 1024 * 1024, domain.MediaKindVideo, ".webm", false},
		{"disallowed content type", "application/pdf", 1000, "", "", true},
		{"disallowed video type (mov)", "video/quicktime", 1000, "", "", true},
		{"oversized image", "image/jpeg", 6 * 1024 * 1024, "", "", true},
		{"oversized video", "video/mp4", 21 * 1024 * 1024, "", "", true},
		{"empty file", "image/jpeg", 0, "", "", true},
		{"negative size", "video/mp4", -1, "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, ext, err := domain.ValidateMediaUpload(tt.contentType, tt.size)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if kind != tt.wantKind {
				t.Errorf("expected kind %q, got %q", tt.wantKind, kind)
			}
			if ext != tt.wantExt {
				t.Errorf("expected extension %q, got %q", tt.wantExt, ext)
			}
		})
	}
}
