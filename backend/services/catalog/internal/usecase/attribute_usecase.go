package usecase

import (
	"context"
	"errors"
	"sort"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
)

// AttributeUseCase manages the attribute catalog (attributes/options) and
// the versioned, inheritable category rules built on top of it — kept as
// its own use case rather than folded into ProductUseCase, mirroring
// CategoryUseCase already being split out for the same reason.
type AttributeUseCase struct {
	attributes AttributeRepositoryPort
	rules      CategoryAttributeRuleRepositoryPort
	categories CategoryRepositoryPort
}

func NewAttributeUseCase(attributes AttributeRepositoryPort, rules CategoryAttributeRuleRepositoryPort, categories CategoryRepositoryPort) *AttributeUseCase {
	return &AttributeUseCase{attributes: attributes, rules: rules, categories: categories}
}

func (uc *AttributeUseCase) CreateAttribute(ctx context.Context, code, name string, dataType domain.DataType, unit *string, isVariantDefining bool) (*domain.Attribute, error) {
	if err := domain.ValidateAttributeCode(code); err != nil {
		return nil, err
	}
	if err := domain.ValidateAttributeName(name); err != nil {
		return nil, err
	}
	if err := domain.ValidateDataType(dataType); err != nil {
		return nil, err
	}
	if isVariantDefining && dataType != domain.DataTypeSelect {
		return nil, apperror.Validation("Only select attributes can be used to define variants")
	}

	a := &domain.Attribute{Code: code, Name: name, DataType: dataType, Unit: unit, IsVariantDefining: isVariantDefining}
	if err := uc.attributes.Create(ctx, a); err != nil {
		if errors.Is(err, repository.ErrAttributeCodeTaken) {
			return nil, apperror.Conflict("An attribute with this code already exists")
		}
		return nil, apperror.Internal(err)
	}
	return a, nil
}

func (uc *AttributeUseCase) List(ctx context.Context) ([]*domain.Attribute, error) {
	attributes, err := uc.attributes.List(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return attributes, nil
}

// ListWithOptions is List plus each attribute's options batch-loaded in one
// extra query, for the admin console's attribute manager.
func (uc *AttributeUseCase) ListWithOptions(ctx context.Context) ([]*domain.Attribute, map[string][]*domain.AttributeOption, error) {
	attributes, err := uc.attributes.List(ctx)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}
	ids := make([]string, 0, len(attributes))
	for _, a := range attributes {
		ids = append(ids, a.ID)
	}
	options, err := uc.attributes.ListOptionsForAttributes(ctx, ids)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}
	return attributes, options, nil
}

func (uc *AttributeUseCase) AddOption(ctx context.Context, attributeID, value string) (*domain.AttributeOption, error) {
	if err := domain.ValidateAttributeOptionValue(value); err != nil {
		return nil, err
	}

	attr, err := uc.attributes.FindByID(ctx, attributeID)
	if err != nil {
		if errors.Is(err, repository.ErrAttributeNotFound) {
			return nil, apperror.Validation("Attribute does not exist")
		}
		return nil, apperror.Internal(err)
	}
	if !attr.DataType.IsChoice() {
		return nil, apperror.Validation("Only select/multi_select attributes can have options")
	}

	position, err := uc.attributes.CountOptionsForAttribute(ctx, attributeID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	o := &domain.AttributeOption{AttributeID: attributeID, Value: value, Position: position}
	if err := uc.attributes.AddOption(ctx, o); err != nil {
		if errors.Is(err, repository.ErrOptionValueTaken) {
			return nil, apperror.Conflict("This option already exists for the attribute")
		}
		return nil, apperror.Internal(err)
	}
	return o, nil
}

// SetCategoryRule inserts a new rule version for this (category, attribute)
// pair — never updates or deletes an existing row, so any product that
// already captured a value against an earlier version keeps its snapshot.
func (uc *AttributeUseCase) SetCategoryRule(ctx context.Context, categoryID, attributeID string, isRequired, isExcluded bool, position int, actorUserID string) (*domain.CategoryAttributeRule, error) {
	if _, err := uc.categories.FindByID(ctx, categoryID); err != nil {
		if errors.Is(err, repository.ErrCategoryNotFound) {
			return nil, apperror.Validation("Category does not exist")
		}
		return nil, apperror.Internal(err)
	}
	if _, err := uc.attributes.FindByID(ctx, attributeID); err != nil {
		if errors.Is(err, repository.ErrAttributeNotFound) {
			return nil, apperror.Validation("Attribute does not exist")
		}
		return nil, apperror.Internal(err)
	}

	currentVersion, err := uc.rules.CurrentVersion(ctx, categoryID, attributeID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	rule := &domain.CategoryAttributeRule{
		CategoryID:  categoryID,
		AttributeID: attributeID,
		Version:     currentVersion + 1,
		IsRequired:  isRequired,
		IsExcluded:  isExcluded,
		Position:    position,
		CreatedBy:   &actorUserID,
	}
	if err := uc.rules.Insert(ctx, rule); err != nil {
		return nil, apperror.Internal(err)
	}
	return rule, nil
}

// ResolveTemplate computes the effective, inheritance-merged attribute list
// for a category: walk its ancestor chain root-first (main -> category ->
// sub-category, at most 3 hops), then apply each level's current rules in
// order so a deeper level's rule for the same attribute fully overwrites
// whatever an ancestor defined (not a partial per-field merge). A rule
// marked IsExcluded drops that attribute from the result entirely, distinct
// from IsRequired == false (kept, but optional).
func (uc *AttributeUseCase) ResolveTemplate(ctx context.Context, categoryID string) ([]domain.ResolvedAttribute, error) {
	chain, err := uc.ancestorChainRootFirst(ctx, categoryID)
	if err != nil {
		return nil, err
	}

	rules, err := uc.rules.CurrentRulesForCategories(ctx, chain)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	rulesByCategory := make(map[string][]*domain.CategoryAttributeRule, len(chain))
	for _, rule := range rules {
		rulesByCategory[rule.CategoryID] = append(rulesByCategory[rule.CategoryID], rule)
	}

	effective := make(map[string]*domain.CategoryAttributeRule)
	for _, ancestorID := range chain {
		for _, rule := range rulesByCategory[ancestorID] {
			effective[rule.AttributeID] = rule
		}
	}

	attributeIDs := make([]string, 0, len(effective))
	for attributeID, rule := range effective {
		if rule.IsExcluded {
			continue
		}
		attributeIDs = append(attributeIDs, attributeID)
	}

	attributes, err := uc.attributes.ListByIDs(ctx, attributeIDs)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	optionsByAttribute, err := uc.attributes.ListOptionsForAttributes(ctx, attributeIDs)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	resolved := make([]domain.ResolvedAttribute, 0, len(attributeIDs))
	for _, attributeID := range attributeIDs {
		attr, ok := attributes[attributeID]
		if !ok {
			continue // attribute row no longer exists; skip rather than fail the whole template
		}
		rule := effective[attributeID]
		resolved = append(resolved, domain.ResolvedAttribute{
			Attribute: *attr,
			Options:   derefOptions(optionsByAttribute[attributeID]),
			RuleID:    rule.ID,
			Required:  rule.IsRequired,
			Position:  rule.Position,
		})
	}

	sort.Slice(resolved, func(i, j int) bool { return resolved[i].Position < resolved[j].Position })
	return resolved, nil
}

// LookupAttributeLabels resolves attributes/options directly by id, bypassing
// category_attribute_rules entirely — used as ResolveTemplate's fallback when
// a persisted selection references an attribute/option the current
// rule-driven template doesn't (or no longer) covers.
func (uc *AttributeUseCase) LookupAttributeLabels(ctx context.Context, attributeIDs, optionIDs []string) (map[string]*domain.Attribute, map[string]*domain.AttributeOption, error) {
	attrs, err := uc.attributes.ListByIDs(ctx, attributeIDs)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}
	opts, err := uc.attributes.ListOptionsByIDs(ctx, optionIDs)
	if err != nil {
		return nil, nil, apperror.Internal(err)
	}
	return attrs, opts, nil
}

// ancestorChainRootFirst walks categoryID's parent_id chain up to the root,
// returning ids ordered main-category first. A plain loop of FindByID calls
// is enough given the hard 3-level cap (domain.MaxCategoryLevel) — no
// recursive query needed, matching the single-parent-hop check
// CategoryUseCase.Create already does.
func (uc *AttributeUseCase) ancestorChainRootFirst(ctx context.Context, categoryID string) ([]string, error) {
	var chain []string
	currentID := categoryID
	for i := 0; i < domain.MaxCategoryLevel; i++ {
		cat, err := uc.categories.FindByID(ctx, currentID)
		if err != nil {
			if errors.Is(err, repository.ErrCategoryNotFound) {
				return nil, apperror.Validation("Category does not exist")
			}
			return nil, apperror.Internal(err)
		}
		chain = append(chain, cat.ID)
		if cat.ParentID == nil {
			break
		}
		currentID = *cat.ParentID
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

func derefOptions(options []*domain.AttributeOption) []domain.AttributeOption {
	out := make([]domain.AttributeOption, 0, len(options))
	for _, o := range options {
		out = append(out, *o)
	}
	return out
}
