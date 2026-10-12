package repository_test

import (
	"context"
	"shopee/backend/pkg/purchasequote"
)

func (s *stubCatalog) PurchaseQuotes(ctx context.Context, inputs []purchasequote.Input) (*purchasequote.Quote, error) {
	out := &purchasequote.Quote{QuoteVersion: "test", Lines: []purchasequote.Line{}}
	for _, in := range inputs {
		p, err := s.GetProduct(ctx, in.ProductID)
		if err != nil {
			return nil, err
		}
		out.Lines = append(out.Lines, purchasequote.Line{Input: in, ProductFound: true, ProductName: p.Name, UnitPrice: p.PriceAmount, Currency: p.Currency, IsSellable: p.IsVisible, Status: p.Status, PricingSource: "product", ProductVersion: 1})
	}
	return out, nil
}
