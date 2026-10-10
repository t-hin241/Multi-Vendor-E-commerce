package domain

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
)

// PW-038: Shipment owns the evidence of a failed delivery it records (a
// lost or returned package): photos or a short video, stored in the shared
// object storage under Shipment's own bucket, served back only through
// Shipment after its access check. An upload is attached by the report of
// the same person, in the report's transaction.

const (
	MaxEvidenceImageBytes = 5 << 20
	MaxEvidenceVideoBytes = 50 << 20
	// MaxEvidencePerReport bounds the files of one report.
	MaxEvidencePerReport = 5
	maxEvidencePixels    = 40_000_000
)

// Evidence states.
const (
	EvidenceUploaded = "uploaded"
	EvidenceAttached = "attached"
	EvidenceDeleted  = "deleted"
)

// Evidence is one file proving a failure report.
type Evidence struct {
	ID          string
	OwnerID     string
	OwnerRole   ActorRole
	ShipmentID  *string
	ReportKind  *string
	ObjectKey   string
	ContentType string
	SizeBytes   int64
	State       string
	CreatedAt   time.Time
}

func evidenceError(status int, code apperror.Code, message string) *apperror.Error {
	return &apperror.Error{Code: code, Status: status, Message: message}
}

var (
	ErrEvidenceUnsupported = evidenceError(http.StatusUnsupportedMediaType, "unsupported_evidence", "Only JPEG or PNG photos and MP4 videos are accepted")
	ErrEvidenceTooLarge    = evidenceError(http.StatusRequestEntityTooLarge, "payload_too_large", "Photos are at most 5 MiB and videos at most 50 MiB")
	ErrEvidenceUnavailable = evidenceError(http.StatusConflict, "evidence_unavailable", "An evidence file is missing, not yours or already used; upload it again")
	ErrEvidenceRequired    = evidenceError(http.StatusUnprocessableEntity, "evidence_required", "Attach the carrier's confirmation (photo or video) to report a package lost")
	ErrEvidenceStorageOff  = evidenceError(http.StatusServiceUnavailable, "evidence_storage_unavailable", "Evidence uploads are not available")
)

// SanitizeEvidence checks a file by its content (the client's type is
// ignored). Photos are re-encoded, which drops EXIF and other metadata; an
// MP4 is kept as sent. It returns the bytes to store, their type and the
// object key extension.
func SanitizeEvidence(data []byte) ([]byte, string, string, error) {
	if len(data) == 0 {
		return nil, "", "", ErrEvidenceUnsupported
	}
	switch http.DetectContentType(data) {
	case "video/mp4":
		if len(data) > MaxEvidenceVideoBytes {
			return nil, "", "", ErrEvidenceTooLarge
		}
		if len(data) < 12 || string(data[4:8]) != "ftyp" {
			return nil, "", "", ErrEvidenceUnsupported
		}
		return data, "video/mp4", ".mp4", nil
	case "image/jpeg", "image/png":
		if len(data) > MaxEvidenceImageBytes {
			return nil, "", "", ErrEvidenceTooLarge
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxEvidencePixels/cfg.Height {
			return nil, "", "", ErrEvidenceUnsupported
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, "", "", ErrEvidenceUnsupported
		}
		var out bytes.Buffer
		if format == "png" {
			err = png.Encode(&out, img)
		} else {
			err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 90})
		}
		if err != nil || out.Len() > MaxEvidenceImageBytes {
			return nil, "", "", ErrEvidenceTooLarge
		}
		if format == "png" {
			return out.Bytes(), "image/png", ".png", nil
		}
		return out.Bytes(), "image/jpeg", ".jpg", nil
	}
	return nil, "", "", ErrEvidenceUnsupported
}
