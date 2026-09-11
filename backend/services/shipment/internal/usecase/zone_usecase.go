package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

type ZoneUseCase struct {
	zones ZoneRepositoryPort
}

func NewZoneUseCase(zones ZoneRepositoryPort) *ZoneUseCase {
	return &ZoneUseCase{zones: zones}
}

func (uc *ZoneUseCase) Create(ctx context.Context, name, code string) (*domain.Zone, error) {
	if err := domain.ValidateZone(name, code); err != nil {
		return nil, err
	}
	zone := &domain.Zone{Name: name, Code: code}
	if err := uc.zones.Create(ctx, zone); err != nil {
		if errors.Is(err, repository.ErrZoneAlreadyExists) {
			return nil, apperror.Conflict("A zone with this code already exists")
		}
		return nil, apperror.Internal(err)
	}
	return zone, nil
}

func (uc *ZoneUseCase) List(ctx context.Context) ([]*domain.Zone, error) {
	zones, err := uc.zones.List(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return zones, nil
}

func (uc *ZoneUseCase) AddProvince(ctx context.Context, zoneID, provinceCode string) error {
	if err := domain.ValidateProvinceCode(provinceCode); err != nil {
		return err
	}
	if _, err := uc.zones.FindByID(ctx, zoneID); err != nil {
		if errors.Is(err, repository.ErrZoneNotFound) {
			return apperror.NotFound("Zone not found")
		}
		return apperror.Internal(err)
	}
	if err := uc.zones.AddProvince(ctx, zoneID, provinceCode); err != nil {
		if errors.Is(err, repository.ErrProvinceAlreadyMapped) {
			return apperror.Conflict("This province is already mapped to a zone")
		}
		return apperror.Internal(err)
	}
	return nil
}

func (uc *ZoneUseCase) ListProvinces(ctx context.Context, zoneID string) ([]string, error) {
	provinces, err := uc.zones.ListProvinces(ctx, zoneID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return provinces, nil
}
