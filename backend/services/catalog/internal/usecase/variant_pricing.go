package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/purchasequote"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
)

func validateVariantPrice(amount int64) error {
	if amount <= 0 {
		return &apperror.Error{Code: "nonpositive_price", Status: 422, Message: "SKU price must be positive"}
	}
	return nil
}

func (uc *ProductUseCase) UpdateVariantPrice(ctx context.Context, actor, productID, variantID string, amount, version int64) (out *domain.ProductVariant, err error) {
	if err = validateVariantPrice(amount); err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, apperror.Validation("expected_version must be positive")
	}
	err = uc.transact(ctx, func(ctx context.Context) error {
		p, e := uc.ownedByUser(ctx, actor, productID, shopaccess.ProductsWrite)
		if e != nil {
			return e
		}
		if !uc.VariantPricingEnabled && amount != p.PriceAmount {
			return apperror.Conflict("Variant pricing is not enabled")
		}
		v, e := uc.variants.FindByID(ctx, variantID)
		if e != nil {
			if errors.Is(e, repository.ErrVariantNotFound) {
				return apperror.NotFound("Variant not found")
			}
			return e
		}
		if v.ProductID != p.ID {
			return apperror.NotFound("Variant not found")
		}
		if v.PriceVersion != version {
			return purchasequote.PriceChanged()
		}
		if v.PriceAmount == amount {
			out = v
			return nil
		}
		// Prices already follow product moderation. A SKU edit uses that policy too.
		if e = uc.revise(ctx, p, actor); e != nil {
			return e
		}
		out, e = uc.variants.UpdatePrice(ctx, p.ID, v.ID, version, amount, actor, middleware.RequestIDFromContext(ctx))
		if errors.Is(e, repository.ErrVariantNotFound) {
			return purchasequote.PriceChanged()
		}
		return e
	})
	return
}

func (uc *ProductUseCase) PurchaseQuotes(ctx context.Context, lines []purchasequote.Input) (*purchasequote.Quote, error) {
	if len(lines) == 0 || len(lines) > 50 {
		return nil, apperror.Validation("Purchase quotes require 1-50 lines")
	}
	products, variants := []string{}, []string{}
	for _, line := range lines {
		if line.Quantity < 1 || line.Quantity > 999 {
			return nil, apperror.Validation("Quantity must be between 1 and 999")
		}
		products = append(products, line.ProductID)
		if line.VariantID != nil {
			variants = append(variants, *line.VariantID)
		}
	}
	snapshot, err := uc.ReadCheckout(ctx, products, variants)
	if err != nil {
		return nil, err
	}
	ps := map[string]domain.CheckoutProduct{}
	vs := map[string]domain.CheckoutVariant{}
	for _, p := range snapshot.Products {
		ps[p.ID] = p
	}
	for _, v := range snapshot.Variants {
		vs[v.ID] = v
	}
	out := &purchasequote.Quote{Lines: make([]purchasequote.Line, 0, len(lines)), VariantPricingEnabled: uc.VariantPricingEnabled}
	for _, in := range lines {
		q := purchasequote.Line{Input: in}
		p, ok := ps[in.ProductID]
		if !ok {
			out.Lines = append(out.Lines, q)
			continue
		}
		q.ProductFound = true
		q.VendorID = p.VendorID
		q.ProductName = p.Name
		q.Status = string(p.Status)
		q.HasVariants = p.HasVariants
		q.ProductVersion = p.Version
		q.Currency = p.Currency
		q.PackageWeight = p.PackageWeightGrams
		q.PricingSource = "product"
		q.UnitPrice = p.PriceAmount
		q.IsSellable = p.Status == domain.StatusApproved && p.IsActive && p.EnforcedVersion == p.Version
		if in.VariantID == nil && p.HasVariants {
			q.IsSellable = false
		}
		if in.VariantID != nil {
			q.PricingSource = "variant"
			q.UnitPrice = 0
			v, found := vs[*in.VariantID]
			q.VariantFound = found && v.ProductID == p.ID
			if !q.VariantFound {
				q.IsSellable = false
			} else {
				q.UnitPrice = v.PriceAmount
				q.VariantVersion = v.PriceVersion
				q.SKU = v.SKU
				labels := make([]string, 0, len(v.Options))
				for _, o := range v.Options {
					labels = append(labels, o.AttributeName+": "+o.OptionValue)
				}
				q.VariantLabel = strings.Join(labels, ", ")
				if !uc.VariantPricingEnabled && v.PriceAmount != p.PriceAmount {
					q.IsSellable = false
				}
			}
		}
		if q.UnitPrice <= 0 || q.Currency != "VND" {
			q.IsSellable = false
		}
		if q.UnitPrice > math.MaxInt64/in.Quantity {
			return nil, apperror.Validation("Line amount is too large")
		}
		out.Lines = append(out.Lines, q)
	}
	data, err := json.Marshal(out.Lines)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	sum := sha256.Sum256(data)
	out.QuoteVersion = hex.EncodeToString(sum[:])
	return out, nil
}
