package usecase_test

import (
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/usecase"
)

func newAttributeFixture() (*usecase.AttributeUseCase, *fakeAttributeRepository, *fakeCategoryAttributeRuleRepository, *fakeCategoryRepository) {
	attributes := newFakeAttributeRepository()
	rules := newFakeCategoryAttributeRuleRepository()
	categories := newFakeCategoryRepository()
	return usecase.NewAttributeUseCase(attributes, rules, categories), attributes, rules, categories
}

func TestResolveTemplate_InheritsFromMainCategory(t *testing.T) {
	uc, attributes, rules, categories := newAttributeFixture()
	categories.seed("main-1", "Clothing", "clothing")
	categories.seedChild("mid-1", "Men's Clothing", "mens-clothing", "main-1", 2)
	categories.seedChild("sub-1", "T-Shirts", "t-shirts", "mid-1", 3)
	attributes.seed("attr-1", "material", "Material", domain.DataTypeText)

	if _, err := uc.SetCategoryRule(t.Context(), "main-1", "attr-1", true, false, 1, "admin-1"); err != nil {
		t.Fatalf("unexpected error setting rule: %v", err)
	}

	resolved, err := uc.ResolveTemplate(t.Context(), "sub-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Attribute.ID != "attr-1" {
		t.Fatalf("expected the main-category rule to be inherited, got %+v", resolved)
	}
	if !resolved[0].Required {
		t.Errorf("expected the inherited rule to still be required")
	}
	_ = rules
}

func TestResolveTemplate_ChildOverridesParentRule(t *testing.T) {
	uc, attributes, _, categories := newAttributeFixture()
	categories.seed("main-1", "Clothing", "clothing")
	categories.seedChild("sub-1", "T-Shirts", "t-shirts", "main-1", 2)
	attributes.seed("attr-1", "material", "Material", domain.DataTypeText)

	if _, err := uc.SetCategoryRule(t.Context(), "main-1", "attr-1", true, false, 1, "admin-1"); err != nil {
		t.Fatalf("unexpected error setting main-category rule: %v", err)
	}
	if _, err := uc.SetCategoryRule(t.Context(), "sub-1", "attr-1", false, false, 1, "admin-1"); err != nil {
		t.Fatalf("unexpected error setting sub-category override: %v", err)
	}

	resolved, err := uc.ResolveTemplate(t.Context(), "sub-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected exactly one resolved attribute, got %+v", resolved)
	}
	if resolved[0].Required {
		t.Errorf("expected the sub-category's override (required=false) to win over the main-category's rule")
	}
}

func TestResolveTemplate_ExcludedAttributeIsDropped(t *testing.T) {
	uc, attributes, _, categories := newAttributeFixture()
	categories.seed("main-1", "Clothing", "clothing")
	categories.seedChild("sub-1", "Accessories", "accessories", "main-1", 2)
	categories.seedChild("sub-2", "T-Shirts", "t-shirts", "main-1", 2)
	attributes.seed("attr-1", "material", "Material", domain.DataTypeText)

	if _, err := uc.SetCategoryRule(t.Context(), "main-1", "attr-1", true, false, 1, "admin-1"); err != nil {
		t.Fatalf("unexpected error setting main-category rule: %v", err)
	}
	if _, err := uc.SetCategoryRule(t.Context(), "sub-1", "attr-1", false, true, 1, "admin-1"); err != nil {
		t.Fatalf("unexpected error excluding on sub-1: %v", err)
	}

	excluded, err := uc.ResolveTemplate(t.Context(), "sub-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(excluded) != 0 {
		t.Errorf("expected the excluded attribute to be dropped for sub-1, got %+v", excluded)
	}

	sibling, err := uc.ResolveTemplate(t.Context(), "sub-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sibling) != 1 {
		t.Errorf("expected the sibling branch to still inherit the attribute, got %+v", sibling)
	}
}

func TestSetCategoryRule_IncrementsVersionWithoutMutatingOldRow(t *testing.T) {
	uc, attributes, rules, categories := newAttributeFixture()
	categories.seed("main-1", "Clothing", "clothing")
	attributes.seed("attr-1", "material", "Material", domain.DataTypeText)

	first, err := uc.SetCategoryRule(t.Context(), "main-1", "attr-1", true, false, 1, "admin-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := uc.SetCategoryRule(t.Context(), "main-1", "attr-1", false, false, 2, "admin-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first.Version != 1 || second.Version != 2 {
		t.Fatalf("expected versions 1 then 2, got %d then %d", first.Version, second.Version)
	}
	if len(rules.rules) != 2 {
		t.Fatalf("expected both versions to remain stored (insert-only), got %d rows", len(rules.rules))
	}
	if rules.rules[0].IsRequired != true {
		t.Errorf("expected the first row to keep its original IsRequired=true, got %v", rules.rules[0].IsRequired)
	}
}

func TestSetCategoryRule_RejectsUnknownAttribute(t *testing.T) {
	uc, _, _, categories := newAttributeFixture()
	categories.seed("main-1", "Clothing", "clothing")

	_, err := uc.SetCategoryRule(t.Context(), "main-1", "no-such-attribute", true, false, 1, "admin-1")
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error, got %v", appErr.Code)
	}
}

func TestCreateAttribute_RejectsVariantDefiningOnNonSelect(t *testing.T) {
	uc, _, _, _ := newAttributeFixture()

	_, err := uc.CreateAttribute(t.Context(), "size", "Size", domain.DataTypeText, nil, true)
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error for a variant-defining text attribute, got %v", appErr.Code)
	}
}

func TestCreateAttribute_AllowsVariantDefiningOnSelect(t *testing.T) {
	uc, _, _, _ := newAttributeFixture()

	a, err := uc.CreateAttribute(t.Context(), "size", "Size", domain.DataTypeSelect, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.IsVariantDefining {
		t.Errorf("expected IsVariantDefining to be true")
	}
}
