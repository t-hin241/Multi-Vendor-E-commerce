package usecase_test

import (
	"context"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
)

func (f *fakeProductVariantRepository) UpdatePrice(_ context.Context, pid, id string, version, amount int64, actor, request string) (*domain.ProductVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.byID[id]
	if v == nil || v.ProductID != pid || v.PriceVersion != version {
		return nil, repository.ErrVariantNotFound
	}
	v.PriceAmount = amount
	v.PriceVersion++
	cp := *v
	return &cp, nil
}
