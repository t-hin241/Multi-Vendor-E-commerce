package usecase_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/purchasequote"
)

func (f *fakeCatalogGateway) PurchaseQuotes(ctx context.Context, lines []purchasequote.Input) (*purchasequote.Quote, error) {
	if f.quoteFunc != nil {
		return f.quoteFunc(ctx, lines)
	}
	out := &purchasequote.Quote{Lines: []purchasequote.Line{}, VariantPricingEnabled: f.pricingEnabled}
	f.batchCalls++
	for _, in := range lines {
		q := purchasequote.Line{Input: in, PricingSource: "product"}
		p, err := f.GetProduct(ctx, in.ProductID)
		if err != nil {
			if app, ok := err.(*apperror.Error); ok && app.Code == apperror.CodeNotFound {
				out.Lines = append(out.Lines, q)
				continue
			}
			return nil, err
		}
		q.ProductFound = true
		q.ProductName = p.Name
		q.VendorID = p.VendorID
		q.UnitPrice = p.PriceAmount
		q.Currency = p.Currency
		q.ProductVersion = 1
		q.Status = "approved"
		q.HasVariants = p.HasVariants
		q.IsSellable = p.IsVisible
		q.ProductVersion = p.Version
		q.PackageWeight = p.PackageWeightGrams
		if in.VariantID != nil {
			v, err := f.GetVariant(ctx, *in.VariantID)
			if err != nil {
				if app, ok := err.(*apperror.Error); !ok || app.Code != apperror.CodeNotFound {
					return nil, err
				}
			} else if v.ProductID == in.ProductID {
				q.VariantFound = true
				q.SKU = v.SKU
				q.PricingSource = "variant"
				q.VariantVersion = v.PriceVersion
				for i, o := range v.Options {
					if i > 0 {
						q.VariantLabel += ", "
					}
					q.VariantLabel += o.AttributeName + ": " + o.OptionValue
				}
				if v.PriceAmount > 0 {
					q.UnitPrice = v.PriceAmount
				}
			}
			q.IsSellable = q.IsSellable && q.VariantFound
		} else if p.HasVariants {
			q.IsSellable = false
		}
		out.Lines = append(out.Lines, q)
	}
	raw, _ := json.Marshal(out.Lines)
	sum := sha256.Sum256(raw)
	out.QuoteVersion = hex.EncodeToString(sum[:])
	return out, nil
}
