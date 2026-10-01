package usecase

import (
	"context"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/domain"
)

// FeeRuleUseCase mirrors catalog's AttributeUseCase.SetCategoryRule:
// setting a fee rule always inserts a new version, never mutates one that
// might already be snapshotted onto an existing shipment. Each version is
// set by a re-verified admin with a reason and audited.
type FeeRuleUseCase struct {
	feeRules FeeRuleRepositoryPort
	carriers CarrierRepositoryPort
	zones    ZoneRepositoryPort
	admin    AdminConfig
}

func NewFeeRuleUseCase(feeRules FeeRuleRepositoryPort, carriers CarrierRepositoryPort, zones ZoneRepositoryPort, admin AdminConfig) *FeeRuleUseCase {
	return &FeeRuleUseCase{feeRules: feeRules, carriers: carriers, zones: zones, admin: admin}
}

func (uc *FeeRuleUseCase) SetCurrent(ctx context.Context, carrierID, zoneID string, baseFeeAmount, freeWeightGrams, extraFeePerKg int64, actorUserID, reason string) (*domain.FeeRule, error) {
	if err := domain.ValidateFeeRule(baseFeeAmount, freeWeightGrams, extraFeePerKg); err != nil {
		return nil, err
	}
	why, err := domain.ValidateNote(reason, true, "Reason")
	if err != nil {
		return nil, err
	}
	var rule *domain.FeeRule
	err = uc.admin.change(ctx, actorUserID, func(ctx context.Context) (domain.AdminAction, error) {
		if _, err := uc.carriers.FindByID(ctx, carrierID); err != nil {
			return domain.AdminAction{}, apperror.Validation("Unknown carrier")
		}
		if _, err := uc.zones.FindByID(ctx, zoneID); err != nil {
			return domain.AdminAction{}, apperror.Validation("Unknown zone")
		}
		currentVersion, err := uc.feeRules.CurrentVersion(ctx, carrierID, zoneID)
		if err != nil {
			return domain.AdminAction{}, err
		}
		rule = &domain.FeeRule{
			CarrierID: carrierID, ZoneID: zoneID, Version: currentVersion + 1,
			BaseFeeAmount: baseFeeAmount, FreeWeightGrams: freeWeightGrams, ExtraFeePerKg: extraFeePerKg,
			CreatedBy: &actorUserID,
		}
		if err := uc.feeRules.Insert(ctx, rule); err != nil {
			return domain.AdminAction{}, err
		}
		return domain.AdminAction{Action: "fee_rule_set", EntityType: domain.AuditFeeRule, EntityID: rule.ID, Reason: why,
			Changes: map[string]any{"carrier_id": carrierID, "zone_id": zoneID, "version": domain.Change(currentVersion, rule.Version),
				"base_fee_amount": baseFeeAmount, "free_weight_grams": freeWeightGrams, "extra_fee_per_kg": extraFeePerKg}}, nil
	})
	if err != nil {
		return nil, err
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
