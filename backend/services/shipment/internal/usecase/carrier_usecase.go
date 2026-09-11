package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

// CarrierUseCase is admin-only reference-data management — plain CRUD, no
// versioning, since a carrier is a structural identity, not a rule.
type CarrierUseCase struct {
	carriers CarrierRepositoryPort
}

func NewCarrierUseCase(carriers CarrierRepositoryPort) *CarrierUseCase {
	return &CarrierUseCase{carriers: carriers}
}

func (uc *CarrierUseCase) Create(ctx context.Context, name, code string) (*domain.Carrier, error) {
	if err := domain.ValidateCarrier(name, code); err != nil {
		return nil, err
	}
	carrier := &domain.Carrier{Name: name, Code: code}
	if err := uc.carriers.Create(ctx, carrier); err != nil {
		if errors.Is(err, repository.ErrCarrierAlreadyExists) {
			return nil, apperror.Conflict("A carrier with this code already exists")
		}
		return nil, apperror.Internal(err)
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

func (uc *CarrierUseCase) SetActive(ctx context.Context, id string, isActive bool) error {
	if err := uc.carriers.SetActive(ctx, id, isActive); err != nil {
		if errors.Is(err, repository.ErrCarrierNotFound) {
			return apperror.NotFound("Carrier not found")
		}
		return apperror.Internal(err)
	}
	return nil
}
