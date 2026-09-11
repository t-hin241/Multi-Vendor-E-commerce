package usecase_test

import (
	"testing"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/usecase"
)

func newCategoryFixture() (*usecase.CategoryUseCase, *fakeCategoryRepository) {
	categories := newFakeCategoryRepository()
	return usecase.NewCategoryUseCase(categories), categories
}

func TestCategoryCreate_RootCategoryHasLevelOne(t *testing.T) {
	uc, _ := newCategoryFixture()

	c, err := uc.Create(t.Context(), "Quần áo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Level != 1 {
		t.Errorf("expected level 1, got %d", c.Level)
	}
	if c.ParentID != nil {
		t.Errorf("expected no parent, got %v", *c.ParentID)
	}
}

func TestCategoryCreate_ChildInheritsParentLevelPlusOne(t *testing.T) {
	uc, categories := newCategoryFixture()
	categories.seed("main-1", "Quần áo", "quan-ao")

	c, err := uc.Create(t.Context(), "Quần áo nam", strPtr("main-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Level != 2 {
		t.Errorf("expected level 2, got %d", c.Level)
	}
	if c.ParentID == nil || *c.ParentID != "main-1" {
		t.Errorf("expected parent main-1, got %v", c.ParentID)
	}
}

func TestCategoryCreate_RejectsUnknownParent(t *testing.T) {
	uc, _ := newCategoryFixture()

	_, err := uc.Create(t.Context(), "Áo thun nam", strPtr("no-such-category"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error, got %v", appErr.Code)
	}
}

func TestCategoryCreate_RejectsNestingUnderSubCategory(t *testing.T) {
	uc, categories := newCategoryFixture()
	categories.seed("main-1", "Quần áo", "quan-ao")
	categories.seedChild("mid-1", "Quần áo nam", "quan-ao-nam", "main-1", 2)
	categories.seedChild("sub-1", "Áo thun nam", "ao-thun-nam", "mid-1", 3)

	_, err := uc.Create(t.Context(), "Áo thun nam cổ tròn", strPtr("sub-1"))
	appErr := mustAppError(t, err)
	if appErr.Code != apperror.CodeValidation {
		t.Errorf("expected validation error nesting under a sub-category, got %v", appErr.Code)
	}
}

func strPtr(s string) *string { return &s }
