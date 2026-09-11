// Package domain holds Catalog's entities and business rules: category and
// product data, pricing validation, and the moderation state machine a
// product may legally move through.
package domain

import (
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

type Category struct {
	ID        string
	Name      string
	Slug      string
	ParentID  *string
	Level     int
	CreatedAt time.Time
}

// MaxCategoryLevel is the deepest a category can nest: main-category (1) ->
// category (2) -> sub-category (3). A sub-category cannot have children.
const MaxCategoryLevel = 3

func ValidateCategoryName(name string) error {
	if strings.TrimSpace(name) == "" {
		return apperror.Validation("Category name is required")
	}
	return nil
}
