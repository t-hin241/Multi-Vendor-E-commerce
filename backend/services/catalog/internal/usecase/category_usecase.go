// Package usecase orchestrates Catalog's workflows: category management,
// vendor product publishing (gated on vendor approval), image upload, the
// public storefront listing, and admin moderation with its audit trail.
package usecase

import (
	"context"
	"errors"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
)

type CategoryUseCase struct {
	identity   RoleVerifier
	categories CategoryRepositoryPort
}

func NewCategoryUseCase(categories CategoryRepositoryPort, identity RoleVerifier) *CategoryUseCase {
	return &CategoryUseCase{categories: categories, identity: identity}
}

func (uc *CategoryUseCase) Create(ctx context.Context, actorUserID, name string, parentID *string) (*domain.Category, error) {
	if err := uc.identity.RequireRole(ctx, actorUserID, "admin"); err != nil {
		return nil, err
	}
	if err := domain.ValidateCategoryName(name); err != nil {
		return nil, err
	}

	level := 1
	if parentID != nil {
		parent, err := uc.categories.FindByID(ctx, *parentID)
		if err != nil {
			if errors.Is(err, repository.ErrCategoryNotFound) {
				return nil, apperror.Validation("Parent category does not exist")
			}
			return nil, apperror.Internal(err)
		}
		if parent.Level >= domain.MaxCategoryLevel {
			return nil, apperror.Validation("Cannot create a category under a sub-category (maximum depth is 3 levels)")
		}
		level = parent.Level + 1
	}

	slug, err := uniqueSlug(ctx, name, uc.categories.SlugExists)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	c := &domain.Category{Name: name, Slug: slug, ParentID: parentID, Level: level}
	if err := uc.categories.Create(ctx, c); err != nil {
		if errors.Is(err, repository.ErrSlugTaken) {
			return nil, apperror.Conflict("A category with a similar name already exists, try again")
		}
		return nil, apperror.Internal(err)
	}
	return c, nil
}

func (uc *CategoryUseCase) List(ctx context.Context) ([]*domain.Category, error) {
	categories, err := uc.categories.List(ctx)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return categories, nil
}
