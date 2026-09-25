package domain

import (
	"time"

	"shopee/backend/pkg/apperror"
)

// MediaKind distinguishes an image from a short video attached to a product
// as extended description content — a separate concept from ProductImage's
// plain photo gallery.
type MediaKind string

const (
	MediaKindImage MediaKind = "image"
	MediaKindVideo MediaKind = "video"
)

type ProductMedia struct {
	ID          string
	ProductID   string
	Kind        MediaKind
	ObjectKey   string
	URL         string
	ContentType string
	SizeBytes   int64
	Position    int
	CreatedAt   time.Time
}

const (
	maxMediaImageBytes = 5 * 1024 * 1024  // same ceiling as the existing photo gallery
	maxMediaVideoBytes = 20 * 1024 * 1024 // "short video" cap — see ValidateMediaUpload

	// MaxMediaItemsPerProduct caps how many images/videos a vendor can
	// attach as extended-description media for a single product.
	MaxMediaItemsPerProduct = 5
)

var allowedMediaImageContentTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

var allowedMediaVideoContentTypes = map[string]string{
	"video/mp4":  ".mp4",
	"video/webm": ".webm",
}

// ValidateMediaUpload checks content type and size before anything is sent
// to object storage, exactly like ValidateImageUpload does for the photo
// gallery, but across both an image and a video allowlist — whichever list
// matches determines both the returned MediaKind and its own size ceiling.
//
// The size cap is a coarse proxy for "short video": this codebase has no
// video-duration decoder, so a heavily compressed long clip could still
// pass and a poorly compressed short one could still be rejected. That
// tradeoff is accepted for MVP rather than adding a new decoding
// dependency (e.g. shelling out to ffprobe) that nothing else here needs.
func ValidateMediaUpload(contentType string, size int64) (kind MediaKind, extension string, err error) {
	if ext, ok := allowedMediaImageContentTypes[contentType]; ok {
		if size <= 0 || size > maxMediaImageBytes {
			return "", "", apperror.Validation("Image must be no larger than 5MB")
		}
		return MediaKindImage, ext, nil
	}
	if ext, ok := allowedMediaVideoContentTypes[contentType]; ok {
		if size <= 0 || size > maxMediaVideoBytes {
			return "", "", apperror.Validation("Video must be no larger than 20MB")
		}
		return MediaKindVideo, ext, nil
	}
	return "", "", apperror.Validation("Media must be a JPEG/PNG/WebP image or an MP4/WebM video")
}
