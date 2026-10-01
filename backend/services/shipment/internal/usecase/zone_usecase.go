package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/repository"
)

// ZoneUseCase manages shipping zones and their provinces; every change is
// made by a re-verified admin and audited.
type ZoneUseCase struct {
	zones ZoneRepositoryPort
	admin AdminConfig
}

func NewZoneUseCase(zones ZoneRepositoryPort, admin AdminConfig) *ZoneUseCase {
	return &ZoneUseCase{zones: zones, admin: admin}
}

func (uc *ZoneUseCase) Create(ctx context.Context, actorID, name, code, note string) (*domain.Zone, error) {
	if err := domain.ValidateZone(name, code); err != nil {
		return nil, err
	}
	why, err := domain.ValidateNote(note, false, "Note")
	if err != nil {
		return nil, err
	}
	zone := &domain.Zone{Name: name, Code: code}
	err = uc.admin.change(ctx, actorID, func(ctx context.Context) (domain.AdminAction, error) {
		if err := uc.zones.Create(ctx, zone); err != nil {
			if errors.Is(err, repository.ErrZoneAlreadyExists) {
				return domain.AdminAction{}, apperror.Conflict("A zone with this code already exists")
			}
			return domain.AdminAction{}, err
		}
		return domain.AdminAction{Action: "zone_created", EntityType: domain.AuditZone, EntityID: zone.ID, Reason: why,
			Changes: map[string]any{"code": code, "name": name}}, nil
	})
	if err != nil {
		return nil, err
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

// AddProvince maps a province to a zone, which changes the quotes of every
// shop delivering there.
func (uc *ZoneUseCase) AddProvince(ctx context.Context, actorID, zoneID, provinceCode, note string) error {
	if err := domain.ValidateProvinceCode(provinceCode); err != nil {
		return err
	}
	why, err := domain.ValidateNote(note, false, "Note")
	if err != nil {
		return err
	}
	return uc.admin.change(ctx, actorID, func(ctx context.Context) (domain.AdminAction, error) {
		if _, err := uc.zones.FindByID(ctx, zoneID); err != nil {
			if errors.Is(err, repository.ErrZoneNotFound) {
				return domain.AdminAction{}, apperror.NotFound("Zone not found")
			}
			return domain.AdminAction{}, err
		}
		if err := uc.zones.AddProvince(ctx, zoneID, provinceCode); err != nil {
			if errors.Is(err, repository.ErrProvinceAlreadyMapped) {
				return domain.AdminAction{}, apperror.Conflict("This province is already mapped to a zone")
			}
			return domain.AdminAction{}, err
		}
		return domain.AdminAction{Action: "zone_province_added", EntityType: domain.AuditZone, EntityID: zoneID, Reason: why,
			Changes: map[string]any{"province_code": provinceCode}}, nil
	})
}

func (uc *ZoneUseCase) ListProvinces(ctx context.Context, zoneID string) ([]string, error) {
	provinces, err := uc.zones.ListProvinces(ctx, zoneID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return provinces, nil
}
