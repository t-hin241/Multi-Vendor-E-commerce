package usecase

import (
	"context"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
)

// FeeRuleUseCase mirrors catalog's AttributeUseCase.SetCategoryRule:
// setting a fee rule always inserts a new version, never mutates one that
// might already be snapshotted onto an existing shipment.
type FeeRuleUseCase struct {
	feeRules FeeRuleRepositoryPort
	carriers CarrierRepositoryPort
	zones    ZoneRepositoryPort
}

func NewFeeRuleUseCase(feeRules FeeRuleRepositoryPort, carriers CarrierRepositoryPort, zones ZoneRepositoryPort) *FeeRuleUseCase {
	return &FeeRuleUseCase{feeRules: feeRules, carriers: carriers, zones: zones}
}

func (uc *FeeRuleUseCase) SetCurrent(ctx context.Context, carrierID, zoneID string, baseFeeAmount, freeWeightGrams, extraFeePerKg int64, actorUserID string) (*domain.FeeRule, error) {
	if err := domain.ValidateFeeRule(baseFeeAmount, freeWeightGrams, extraFeePerKg); err != nil {
		return nil, err
	}
	if _, err := uc.carriers.FindByID(ctx, carrierID); err != nil {
		return nil, apperror.Validation("Unknown carrier")
	}
	if _, err := uc.zones.FindByID(ctx, zoneID); err != nil {
		return nil, apperror.Validation("Unknown zone")
	}

	currentVersion, err := uc.feeRules.CurrentVersion(ctx, carrierID, zoneID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	rule := &domain.FeeRule{
		CarrierID: carrierID, ZoneID: zoneID, Version: currentVersion + 1,
		BaseFeeAmount: baseFeeAmount, FreeWeightGrams: freeWeightGrams, ExtraFeePerKg: extraFeePerKg,
		CreatedBy: &actorUserID,
	}
	if err := uc.feeRules.Insert(ctx, rule); err != nil {
		return nil, apperror.Internal(err)
	}
	return rule, nil
}

func (uc *FeeRuleUseCase) List(ctx context.Context) ([]*domain.FeeRule, error) {
	rules, err := uc.feeRules.List(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return rules, nil
}
