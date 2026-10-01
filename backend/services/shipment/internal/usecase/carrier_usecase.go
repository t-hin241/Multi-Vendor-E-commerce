package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

// CarrierUseCase is admin-only reference-data management — plain CRUD, no
// versioning, since a carrier is a structural identity, not a rule. Every
// change is made by a re-verified admin and audited.
type CarrierUseCase struct {
	carriers CarrierRepositoryPort
	admin    AdminConfig
}

func NewCarrierUseCase(carriers CarrierRepositoryPort, admin AdminConfig) *CarrierUseCase {
	return &CarrierUseCase{carriers: carriers, admin: admin}
}

// Create adds a carrier (inactive until an admin turns it on); the note is
// optional.
func (uc *CarrierUseCase) Create(ctx context.Context, actorID, name, code, note string) (*domain.Carrier, error) {
	if err := domain.ValidateCarrier(name, code); err != nil {
		return nil, err
	}
	why, err := domain.ValidateNote(note, false, "Note")
	if err != nil {
		return nil, err
	}
	carrier := &domain.Carrier{Name: name, Code: code}
	err = uc.admin.change(ctx, actorID, func(ctx context.Context) (domain.AdminAction, error) {
		if err := uc.carriers.Create(ctx, carrier); err != nil {
			if errors.Is(err, repository.ErrCarrierAlreadyExists) {
				return domain.AdminAction{}, apperror.Conflict("A carrier with this code already exists")
			}
			return domain.AdminAction{}, err
		}
		return domain.AdminAction{Action: "carrier_created", EntityType: domain.AuditCarrier, EntityID: carrier.ID, Reason: why,
			Changes: map[string]any{"code": code, "name": name}}, nil
	})
	if err != nil {
		return nil, err
	}
	return carrier, nil
}

func (uc *CarrierUseCase) List(ctx context.Context) ([]*domain.Carrier, error) {
	carriers, err := uc.carriers.List(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return carriers, nil
}

// ListActive serves the public/vendor-facing read: a vendor choosing which
// carrier to enable for their shop only ever needs to see the ones admin
// has actually turned on, same trust level as Catalog's public
// GET /api/catalog/categories for admin-authored reference data.
func (uc *CarrierUseCase) ListActive(ctx context.Context) ([]*domain.Carrier, error) {
	carriers, err := uc.carriers.List(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	active := make([]*domain.Carrier, 0, len(carriers))
	for _, c := range carriers {
		if c.IsActive {
			active = append(active, c)
		}
	}
	return active, nil
}

// SetActive turns a carrier on or off for new quotes; the reason is
// required because it changes which shops can sell.
func (uc *CarrierUseCase) SetActive(ctx context.Context, actorID, id string, isActive bool, reason string) error {
	why, err := domain.ValidateNote(reason, true, "Reason")
	if err != nil {
		return err
	}
	return uc.admin.change(ctx, actorID, func(ctx context.Context) (domain.AdminAction, error) {
		current, err := uc.carriers.FindByID(ctx, id)
		if errors.Is(err, repository.ErrCarrierNotFound) {
			return domain.AdminAction{}, apperror.NotFound("Carrier not found")
		}
		if err != nil {
			return domain.AdminAction{}, err
		}
		if err := uc.carriers.SetActive(ctx, id, isActive); err != nil {
			return domain.AdminAction{}, err
		}
		return domain.AdminAction{Action: "carrier_active_set", EntityType: domain.AuditCarrier, EntityID: id, Reason: why,
			Changes: map[string]any{"is_active": domain.Change(current.IsActive, isActive)}}, nil
	})
}
