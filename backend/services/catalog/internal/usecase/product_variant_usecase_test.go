package usecase_test

import (
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/domain"
)

func seedSizeAxisTemplate(f *productTestFixture, categoryID string) {
	f.attributeTemplate.templates[categoryID] = []domain.ResolvedAttribute{
		{
			Attribute: domain.Attribute{ID: "attr-size", Name: "Size", DataType: domain.DataTypeSelect, IsVariantDefining: true},
			Options:   []domain.AttributeOption{{ID: "opt-s", Value: "S"}, {ID: "opt-m", Value: "M"}},
			RuleID:    "rule-size",
		},
	}
}

func TestCreate_SkipsRequiredCheckForVariantDefiningAttribute(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()
	f.attributeTemplate.templates["cat-1"] = []domain.ResolvedAttribute{
		{
			Attribute: domain.Attribute{ID: "attr-size", Name: "Size", DataType: domain.DataTypeSelect, IsVariantDefining: true},
			Options:   []domain.AttributeOption{{ID: "opt-s", Value: "S"}},
			RuleID:    "rule-size",
			Required:  true,
		},
	}

	// No "attributes" submitted at all for the required, variant-defining
	// Size attribute — its value is expressed per-variant instead, so
	// product creation must not demand one up front.
	p, err := f.products.Create(ctx, "user-1", "vendor-1", "cat-1", "Shirt", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.attributeValues.byProduct[p.ID]) != 0 {
		t.Errorf("expected no product_attribute_values row for a variant-defining attribute, got %+v", f.attributeValues.byProduct[p.ID])
	}
}

func TestCreateVariant_BuildsStableKeyAndRejectsDuplicateCombination(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()
	seedSizeAxisTemplate(f, "cat-1")

	p, err := f.products.Create(ctx, "user-1", "vendor-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, details, err := f.products.CreateVariant(ctx, "user-1", p.ID, "SNK-S", []string{"opt-s"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(details) != 1 || details[0].AttributeName != "Size" || details[0].OptionValue != "S" {
		t.Fatalf("expected resolved Size=S label, got %+v", details)
	}

	// Same combination again (different SKU) must be rejected as a duplicate.
	_, _, err = f.products.CreateVariant(ctx, "user-1", p.ID, "SNK-S-2", []string{"opt-s"})
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict for a duplicate option combination, got %v", appErr.Code)
	}
	_ = v
}

func TestCreateVariant_RejectsDuplicateSKU(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()
	seedSizeAxisTemplate(f, "cat-1")

	p, err := f.products.Create(ctx, "user-1", "vendor-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err := f.products.CreateVariant(ctx, "user-1", p.ID, "SAME-SKU", []string{"opt-s"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = f.products.CreateVariant(ctx, "user-1", p.ID, "SAME-SKU", []string{"opt-m"})
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeConflict {
		t.Errorf("expected conflict for a reused SKU, got %v", appErr.Code)
	}
}

func TestCreateVariant_RejectsMissingAxis(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()
	f.attributeTemplate.templates["cat-1"] = []domain.ResolvedAttribute{
		{Attribute: domain.Attribute{ID: "attr-size", Name: "Size", DataType: domain.DataTypeSelect, IsVariantDefining: true}, Options: []domain.AttributeOption{{ID: "opt-s", Value: "S"}}},
		{Attribute: domain.Attribute{ID: "attr-color", Name: "Color", DataType: domain.DataTypeSelect, IsVariantDefining: true}, Options: []domain.AttributeOption{{ID: "opt-red", Value: "Red"}}},
	}

	p, err := f.products.Create(ctx, "user-1", "vendor-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only Size submitted; Color (also a variant-defining axis) is missing.
	_, _, err = f.products.CreateVariant(ctx, "user-1", p.ID, "SNK-S", []string{"opt-s"})
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a missing axis, got %v", appErr.Code)
	}
}

func TestCreateVariant_RejectsWhenNoVariantDefiningAttributes(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	ctx := t.Context()
	f.attributeTemplate.templates["cat-1"] = []domain.ResolvedAttribute{
		{Attribute: domain.Attribute{ID: "attr-material", Name: "Material", DataType: domain.DataTypeText}, RuleID: "rule-1"},
	}

	p, err := f.products.Create(ctx, "user-1", "vendor-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = f.products.CreateVariant(ctx, "user-1", p.ID, "SNK-1", []string{"whatever"})
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error when the category has no variant-defining attributes, got %v", appErr.Code)
	}
}

func TestListVariantsForOwner_ResolvesLabelsAndRejectsNonOwner(t *testing.T) {
	f := newProductFixture()
	f.vendors.approvedVendors["user-1"] = "vendor-1"
	f.vendors.approvedVendors["user-2"] = "vendor-2"
	ctx := t.Context()
	seedSizeAxisTemplate(f, "cat-1")

	p, err := f.products.Create(ctx, "user-1", "vendor-1", "cat-1", "Sneakers", "desc", 100000, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err := f.products.CreateVariant(ctx, "user-1", p.ID, "SNK-S", []string{"opt-s"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	variants, details, err := f.products.ListVariantsForOwner(ctx, "user-1", p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(variants) != 1 || details[variants[0].ID][0].OptionValue != "S" {
		t.Fatalf("expected one variant with resolved label Size=S, got %+v / %+v", variants, details)
	}

	_, _, err = f.products.ListVariantsForOwner(ctx, "user-2", p.ID)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeForbidden {
		t.Errorf("expected forbidden for a non-owner, got %v", appErr.Code)
	}
}
