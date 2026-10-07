package usecase

import (
	"context"
	"strconv"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/catalog/internal/domain"
)

func (uc *ProductUseCase) UpdateContent(ctx context.Context, userID, productID, name, description string, price, version int64, attributes []AttributeValueInput) (result *domain.Product, err error) {
	err = uc.transact(ctx, func(ctx context.Context) error {
		p, err := uc.ownedByUser(ctx, userID, productID, shopaccess.ProductsWrite)
		if err != nil {
			return err
		}
		if version != p.Version {
			return apperror.Conflict("Product changed; refresh before editing")
		}
		if err := domain.ValidateProductInput(name, description, price); err != nil {
			return err
		}
		template, err := uc.attributeTemplate.ResolveTemplate(ctx, p.CategoryID)
		if err != nil {
			return err
		}
		values, pkg, err := buildAttributeValues(template, attributes)
		if err != nil {
			return err
		}
		p.Name = name
		p.Description = domain.SanitizeDescription(description)
		p.PriceAmount = price
		if err := uc.products.UpdateContent(ctx, p); err != nil {
			return err
		}
		if err := uc.attributeValues.ReplaceForProduct(ctx, p.ID, values); err != nil {
			return err
		}
		if err := uc.packaging.Upsert(ctx, p.ID, pkg); err != nil {
			return err
		}
		if err := uc.auditLogs.Create(ctx, p.ID, userID, "content_revision", nil); err != nil {
			return err
		}
		result, err = uc.products.FindByID(ctx, p.ID)
		return err
	})
	return
}

func (uc *ProductUseCase) GetForOwner(ctx context.Context, userID, productID string) (*domain.Product, []*domain.ProductAttributeValue, domain.Packaging, error) {
	p, err := uc.ownedByUser(ctx, userID, productID, shopaccess.ProductsRead)
	if err != nil {
		return nil, nil, domain.Packaging{}, err
	}
	values, err := uc.attributeValues.ListForProduct(ctx, p.ID)
	if err != nil {
		return nil, nil, domain.Packaging{}, apperror.Internal(err)
	}
	pkg, err := uc.packaging.Get(ctx, p.ID)
	if err != nil {
		return nil, nil, domain.Packaging{}, apperror.Internal(err)
	}
	return p, values, pkg, nil
}

func (uc *ProductUseCase) revise(ctx context.Context, p *domain.Product, userID string) error {
	if p.Status == domain.StatusDraft {
		return nil
	}
	if err := uc.products.UpdateStatus(ctx, p.ID, domain.StatusDraft, nil); err != nil {
		return apperror.Internal(err)
	}
	if err := uc.auditLogs.Create(ctx, p.ID, userID, "content_revision", nil); err != nil {
		return apperror.Internal(err)
	}
	p.Status = domain.StatusDraft
	p.RejectionReason = nil
	return nil
}

// validateCurrentRules rechecks drafts against the current category template at submission and approval.
func (uc *ProductUseCase) validateCurrentRules(ctx context.Context, p *domain.Product) error {
	if err := uc.validateCategory(ctx, p.CategoryID); err != nil {
		return err
	}
	template, err := uc.attributeTemplate.ResolveTemplate(ctx, p.CategoryID)
	if err != nil {
		return err
	}
	values, err := uc.attributeValues.ListForProduct(ctx, p.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	inputs := map[string]*AttributeValueInput{}
	for _, value := range values {
		input := inputs[value.AttributeID]
		if input == nil {
			input = &AttributeValueInput{AttributeID: value.AttributeID}
			inputs[value.AttributeID] = input
		}
		if value.OptionID != nil {
			input.OptionIDs = append(input.OptionIDs, *value.OptionID)
		}
		if value.ValueText != nil {
			input.Value = value.ValueText
		}
		if value.ValueNumber != nil {
			x := strconv.FormatFloat(*value.ValueNumber, 'f', -1, 64)
			input.Value = &x
		}
		if value.ValueBoolean != nil {
			x := strconv.FormatBool(*value.ValueBoolean)
			input.Value = &x
		}
	}
	pkg, err := uc.packaging.Get(ctx, p.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	var submitted []AttributeValueInput
	for _, r := range template {
		if input := inputs[r.Attribute.ID]; input != nil {
			submitted = append(submitted, *input)
		}
		var value *int64
		switch domain.PackagingAttributeCode(r.Attribute.Code) {
		case domain.PackagingCodeWeight:
			value = pkg.WeightGrams
		case domain.PackagingCodeLength:
			value = pkg.LengthMM
		case domain.PackagingCodeWidth:
			value = pkg.WidthMM
		case domain.PackagingCodeHeight:
			value = pkg.HeightMM
		}
		if value != nil {
			x := strconv.FormatInt(*value, 10)
			submitted = append(submitted, AttributeValueInput{AttributeID: r.Attribute.ID, Value: &x})
		}
	}
	if _, _, err := buildAttributeValues(template, submitted); err != nil {
		return err
	}
	variants, err := uc.variants.ListForProduct(ctx, p.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	hasAxes := false
	for _, r := range template {
		hasAxes = hasAxes || r.Attribute.IsVariantDefining
	}
	if hasAxes && len(variants) == 0 {
		return apperror.Validation("Add at least one variant before submitting")
	}
	ids := make([]string, 0, len(variants))
	for _, v := range variants {
		ids = append(ids, v.ID)
	}
	selections, err := uc.variants.ListOptionsForVariants(ctx, ids)
	if err != nil {
		return apperror.Internal(err)
	}
	for _, v := range variants {
		var options []string
		for _, o := range selections[v.ID] {
			options = append(options, o.OptionID)
		}
		if _, err := matchVariantOptions(template, options); err != nil {
			return err
		}
	}
	return nil
}
