package usecase

import (
	"context"
	"io"
	"slices"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
)

// PW-038: Shipment keeps the evidence of its failure reports (the
// carrier's loss confirmation, photos of a returned package) in its own
// private bucket of the shared object storage.

// EvidenceStore is the private bucket.
type EvidenceStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// EvidenceRepositoryPort stores the evidence rows.
type EvidenceRepositoryPort interface {
	Create(ctx context.Context, e *domain.Evidence) error
	Attach(ctx context.Context, ids []string, ownerID, shipmentID, kind string) (int64, error)
	ForShipment(ctx context.Context, shipmentID, owner string) ([]*domain.Evidence, error)
	Orphans(ctx context.Context, before time.Time, limit int) ([]*domain.Evidence, error)
	MarkDeleted(ctx context.Context, id, from string) (bool, error)
}

// EvidenceOrphanAge is how long an upload waits for its report.
const EvidenceOrphanAge = 24 * time.Hour

// EvidenceFile is a stored file opened for download.
type EvidenceFile struct {
	Body        io.ReadCloser
	ContentType string
	SizeBytes   int64
}

func (uc *ShipmentUseCase) evidenceOn() bool {
	return uc.Evidence != nil && uc.EvidenceRepo != nil
}

// UploadEvidence stores one file for a report the actor is about to make
// on the shipment; the report attaches it.
func (uc *ShipmentUseCase) UploadEvidence(ctx context.Context, actor Actor, shipmentID string, data []byte) (*domain.Evidence, error) {
	if !uc.DeliveryResolution {
		return nil, domain.DeliveryResolutionDisabled()
	}
	if !uc.evidenceOn() {
		return nil, domain.ErrEvidenceStorageOff
	}
	s, err := uc.load(ctx, shipmentID)
	if err != nil {
		return nil, err
	}
	if err := uc.authorize(ctx, actor, s); err != nil {
		return nil, err
	}
	clean, contentType, ext, err := domain.SanitizeEvidence(data)
	if err != nil {
		return nil, err
	}
	e := &domain.Evidence{OwnerID: actor.ID, OwnerRole: actor.Role, ShipmentID: &s.ID,
		ObjectKey: "shipments/" + s.ID + "/" + uuid.NewString() + ext, ContentType: contentType, SizeBytes: int64(len(clean))}
	if err := uc.Evidence.Put(ctx, e.ObjectKey, clean, contentType); err != nil {
		uc.Log.Error().Err(err).Str("shipment_id", s.ID).Msg("shipment_evidence_upload_failed")
		return nil, domain.ErrEvidenceStorageOff
	}
	if err := uc.EvidenceRepo.Create(ctx, e); err != nil {
		uc.deleteObject(ctx, e.ObjectKey)
		return nil, apperror.Internal(err)
	}
	return e, nil
}

// ListEvidence shows the evidence of a shipment's reports to its shop and
// admins, with the actor's own uploads not attached yet.
func (uc *ShipmentUseCase) ListEvidence(ctx context.Context, actor Actor, shipmentID string) ([]*domain.Evidence, error) {
	s, err := uc.load(ctx, shipmentID)
	if err != nil {
		return nil, err
	}
	if err := uc.authorize(ctx, actor, s); err != nil {
		return nil, err
	}
	if uc.EvidenceRepo == nil {
		return []*domain.Evidence{}, nil
	}
	items, err := uc.EvidenceRepo.ForShipment(ctx, s.ID, actor.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return items, nil
}

// OpenEvidence streams one file ListEvidence shows.
func (uc *ShipmentUseCase) OpenEvidence(ctx context.Context, actor Actor, shipmentID, evidenceID string) (*EvidenceFile, error) {
	items, err := uc.ListEvidence(ctx, actor, shipmentID)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(items, func(e *domain.Evidence) bool { return e.ID == evidenceID })
	if i < 0 || uc.Evidence == nil {
		return nil, apperror.NotFound("Evidence not found")
	}
	body, err := uc.Evidence.Open(ctx, items[i].ObjectKey)
	if err != nil {
		uc.Log.Error().Err(err).Str("evidence_id", evidenceID).Msg("shipment_evidence_open_failed")
		return nil, domain.ErrEvidenceStorageOff
	}
	return &EvidenceFile{Body: body, ContentType: items[i].ContentType, SizeBytes: items[i].SizeBytes}, nil
}

// evidenceIDs validates the ids a report cites (deduplicated).
func evidenceIDs(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return nil, apperror.Validation("evidence_ids must be ids of uploaded files")
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) > domain.MaxEvidencePerReport {
		return nil, apperror.Validation("A report carries at most 5 evidence files")
	}
	return out, nil
}

// attachEvidence links the report's files in its transaction; a file
// missing, someone else's or used already refuses the report.
func (uc *ShipmentUseCase) attachEvidence(ctx context.Context, actor Actor, s *domain.Shipment, kind string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if uc.EvidenceRepo == nil {
		return domain.ErrEvidenceUnavailable
	}
	n, err := uc.EvidenceRepo.Attach(ctx, ids, actor.ID, s.ID, kind)
	if err != nil {
		return err
	}
	if n != int64(len(ids)) {
		return domain.ErrEvidenceUnavailable
	}
	return nil
}

// CleanEvidence removes uploads never attached to a report.
func (uc *ShipmentUseCase) CleanEvidence(ctx context.Context) (int, error) {
	if !uc.evidenceOn() {
		return 0, nil
	}
	orphans, err := uc.EvidenceRepo.Orphans(ctx, uc.Now().Add(-EvidenceOrphanAge), 100)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range orphans {
		ok, err := uc.EvidenceRepo.MarkDeleted(ctx, e.ID, domain.EvidenceUploaded)
		if err != nil {
			return removed, err
		}
		if !ok {
			continue // attached meanwhile
		}
		uc.deleteObject(ctx, e.ObjectKey)
		removed++
	}
	return removed, nil
}

func (uc *ShipmentUseCase) deleteObject(ctx context.Context, key string) {
	if err := uc.Evidence.Delete(context.WithoutCancel(ctx), key); err != nil {
		uc.Log.Warn().Err(err).Str("object_key", key).Msg("shipment_evidence_object_delete_failed")
	}
}
