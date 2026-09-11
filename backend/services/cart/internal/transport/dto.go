package transport

import "shopee/backend/services/cart/internal/usecase"

type addItemRequest struct {
	ProductID string  `json:"product_id" binding:"required"`
	VariantID *string `json:"variant_id"`
	Quantity  int64   `json:"quantity" binding:"required"`
}

type setQuantityRequest struct {
	Quantity int64 `json:"quantity"`
}

type lineResponse struct {
	ProductID    string  `json:"product_id"`
	ProductName  string  `json:"product_name,omitempty"`
	VariantID    *string `json:"variant_id,omitempty"`
	VariantSKU   *string `json:"variant_sku,omitempty"`
	VariantLabel *string `json:"variant_label,omitempty"`
	Quantity     int64   `json:"quantity"`
	PriceAmount  int64   `json:"price_amount"`
	Currency     string  `json:"currency,omitempty"`
	Subtotal     int64   `json:"subtotal"`
	Available    bool    `json:"available"`
}

type cartResponse struct {
	Items []lineResponse `json:"items"`
	Total int64          `json:"total"`
}

func toCartResponse(lines []usecase.LineView) cartResponse {
	resp := cartResponse{Items: make([]lineResponse, 0, len(lines))}
	for _, l := range lines {
		subtotal := l.PriceAmount * l.Quantity
		resp.Items = append(resp.Items, lineResponse{
			ProductID: l.ProductID, ProductName: l.ProductName,
			VariantID: l.VariantID, VariantSKU: l.VariantSKU, VariantLabel: l.VariantLabel,
			Quantity: l.Quantity, PriceAmount: l.PriceAmount, Currency: l.Currency, Subtotal: subtotal, Available: l.Available,
		})
		if l.Available {
			resp.Total += subtotal
		}
	}
	return resp
}
