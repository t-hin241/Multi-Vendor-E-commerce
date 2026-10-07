package usecase

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/repository"
)

// orphanAttachmentGrace is how long an upload may wait for its message.
const orphanAttachmentGrace = 24 * time.Hour

// UploadSupportAttachment stores one evidence image for the caller. Only
// real JPEG or PNG images within the size and pixel limits are accepted
// (the content is sniffed, the client's type is ignored); the image is
// re-encoded, which drops EXIF and any other embedded metadata. The upload
// is attached later by a message of the same caller.
func (uc *OrderUseCase) UploadSupportAttachment(ctx context.Context, actor SupportActor, data []byte) (*domain.CaseAttachment, error) {
	if uc.Support == nil || uc.Attachments == nil {
		return nil, domain.AttachmentsUnavailable()
	}
	if actor.Role == "admin" {
		if err := uc.requireAdmin(ctx, actor.ID); err != nil {
			return nil, err
		}
	}
	clean, contentType, err := sanitizeImage(data, uc.SupportPolicy)
	if err != nil {
		return nil, err
	}
	ext := ".jpg"
	if contentType == "image/png" {
		ext = ".png"
	}
	a := &domain.CaseAttachment{OwnerID: actor.ID, ObjectKey: "support/" + uuid.NewString() + ext, ContentType: contentType, SizeBytes: int64(len(clean))}
	if err := uc.Attachments.Put(ctx, a.ObjectKey, clean, contentType); err != nil {
		uc.Log.Error().Err(err).Msg("order_support_attachment_put_failed")
		return nil, domain.AttachmentsUnavailable()
	}
	if err := uc.Support.CreateAttachment(ctx, a); err != nil {
		// The object has no row: remove it now; the bucket is private, so a
		// leftover is unreachable and only costs storage.
		if delErr := uc.Attachments.Delete(context.WithoutCancel(ctx), a.ObjectKey); delErr != nil {
			uc.Log.Error().Err(delErr).Msg("order_support_attachment_cleanup_failed")
		}
		return nil, appError(err)
	}
	return a, nil
}

// sanitizeImage checks data is a JPEG or PNG within the policy limits and
// returns it re-encoded without metadata.
func sanitizeImage(data []byte, p domain.SupportPolicy) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", domain.UnsupportedAttachment("The file is empty")
	}
	if int64(len(data)) > p.MaxAttachmentBytes {
		return nil, "", domain.UnsupportedAttachment("Each image must be at most 5 MiB")
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return nil, "", domain.UnsupportedAttachment("Only JPEG or PNG images are accepted")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", domain.UnsupportedAttachment("The image could not be read")
	}
	if cfg.Width > p.MaxImagePixels/cfg.Height {
		return nil, "", domain.UnsupportedAttachment("The image dimensions are too large")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", domain.UnsupportedAttachment("The image could not be read")
	}
	var out bytes.Buffer
	if contentType == "image/png" {
		err = png.Encode(&out, img)
	} else {
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 90})
	}
	if err != nil {
		return nil, "", domain.UnsupportedAttachment("The image could not be processed")
	}
	if int64(out.Len()) > p.MaxAttachmentBytes {
		return nil, "", domain.UnsupportedAttachment("Each image must be at most 5 MiB")
	}
	return out.Bytes(), contentType, nil
}

// SupportAttachmentFile is an attachment opened for download.
type SupportAttachmentFile struct {
	Body        io.ReadCloser
	ContentType string
	SizeBytes   int64
}

// OpenSupportAttachment streams an attachment of a message the caller may
// see; an internal note's files are admin-only.
func (uc *OrderUseCase) OpenSupportAttachment(ctx context.Context, actor SupportActor, caseID, attachmentID string) (*SupportAttachmentFile, error) {
	c, err := uc.supportCaseFor(ctx, actor, caseID)
	if err != nil {
		return nil, err
	}
	a, err := uc.Support.FindAttachment(ctx, attachmentID)
	if err != nil {
		return nil, notFoundOrInternal(err, repository.ErrAttachmentNotFound, "Attachment not found")
	}
	if a.State != domain.AttachmentAttached || a.CaseID == nil || *a.CaseID != c.ID || a.MessageID == nil {
		return nil, apperror.NotFound("Attachment not found")
	}
	messages, err := uc.Support.ListMessages(ctx, c.ID, actor.Role == "admin")
	if err != nil {
		return nil, appError(err)
	}
	visible := false
	for _, m := range messages {
		if m.ID == *a.MessageID {
			visible = true
			break
		}
	}
	if !visible {
		return nil, apperror.NotFound("Attachment not found")
	}
	if uc.Attachments == nil {
		return nil, domain.AttachmentsUnavailable()
	}
	body, err := uc.Attachments.Open(ctx, a.ObjectKey)
	if err != nil {
		uc.Log.Error().Err(err).Str("attachment_id", a.ID).Msg("order_support_attachment_open_failed")
		return nil, domain.AttachmentsUnavailable()
	}
	return &SupportAttachmentFile{Body: body, ContentType: a.ContentType, SizeBytes: a.SizeBytes}, nil
}

// CleanSupportAttachments deletes uploads never attached after the grace
// period and evidence of cases closed longer than the retention. The row
// becomes a tombstone first (so an upload attached meanwhile is kept), then
// the object is removed; a failed removal leaves an unreachable object in
// the private bucket and is logged for the operator.
func (uc *OrderUseCase) CleanSupportAttachments(ctx context.Context, limit int) (int, error) {
	if uc.Support == nil || uc.Attachments == nil {
		return 0, nil
	}
	now := uc.Now()
	orphans, err := uc.Support.ListOrphanAttachments(ctx, now.Add(-orphanAttachmentGrace), limit)
	if err != nil {
		return 0, err
	}
	expired, err := uc.Support.ListExpiredAttachments(ctx, now.Add(-uc.SupportConfig.AttachmentRetention), limit)
	if err != nil {
		return 0, err
	}
	deleted := 0
	var errs []error
	for _, a := range append(orphans, expired...) {
		marked, err := uc.Support.MarkAttachmentDeleted(ctx, a.ID, a.State)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !marked {
			continue
		}
		if err := uc.Attachments.Delete(ctx, a.ObjectKey); err != nil {
			uc.Log.Error().Err(err).Str("attachment_id", a.ID).Msg("order_support_attachment_delete_failed")
			errs = append(errs, err)
			continue
		}
		deleted++
	}
	return deleted, errors.Join(errs...)
}
