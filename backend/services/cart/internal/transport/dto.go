package transport

import (
	"time"

	"shopee/backend/services/cart/internal/domain"
	"shopee/backend/services/cart/internal/usecase"
)

type addItemRequest struct {
	ProductID       string  `json:"product_id" binding:"required,uuid"`
	VariantID       *string `json:"variant_id" binding:"omitempty,uuid"`
	Quantity        int64   `json:"quantity" binding:"required"`
	ExpectedVersion *int64  `json:"expected_version" binding:"omitempty,min=1"`
}

type setQuantityRequest struct {
	Quantity        *int64 `json:"quantity" binding:"required"`
	ExpectedVersion *int64 `json:"expected_version" binding:"omitempty,min=1"`
}

type priceConfirmationLine struct {
	LineID      string `json:"line_id" binding:"required,uuid"`
	PriceAmount int64  `json:"price_amount" binding:"min=0"`
	Currency    string `json:"currency" binding:"required,len=3"`
}

type confirmPricesRequest struct {
	ExpectedVersion int64                   `json:"expected_version" binding:"required,min=1"`
	Lines           []priceConfirmationLine `json:"lines" binding:"required,min=1,max=50,dive"`
}

type moneyResponse struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

type lineResponse struct {
	LineID            string  `json:"line_id"`
	LineVersion       int64   `json:"line_version"`
	ProductID         string  `json:"product_id"`
	ProductName       string  `json:"product_name,omitempty"`
	VariantID         *string `json:"variant_id,omitempty"`
	VariantSKU        *string `json:"variant_sku,omitempty"`
	VariantLabel      *string `json:"variant_label,omitempty"`
	Quantity          int64   `json:"quantity"`
	PriceAmount       *int64  `json:"price_amount"`
	Currency          string  `json:"currency,omitempty"`
	SeenPriceAmount   *int64  `json:"seen_price_amount,omitempty"`
	SeenCurrency      *string `json:"seen_currency,omitempty"`
	PriceChanged      bool    `json:"price_changed"`
	Subtotal          *int64  `json:"subtotal"`
	State             string  `json:"state"`
	StockStatus       string  `json:"stock_status"`
	AvailableQuantity *int64  `json:"available_quantity,omitempty"`
	Available         bool    `json:"available"`
}

type degradedResponse struct {
	Catalog   bool `json:"catalog"`
	Inventory bool `json:"inventory"`
}

type limitsResponse struct {
	MaxLines           int `json:"max_lines"`
	MaxQuantityPerLine int `json:"max_quantity_per_line"`
}

type pageResponse struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

type cartResponse struct {
	Version           int64            `json:"version"`
	Items             []lineResponse   `json:"items"`
	Subtotal          *moneyResponse   `json:"subtotal"`
	Total             int64            `json:"total"` // legacy: Subtotal.Amount, or 0 when unknown
	Currency          string           `json:"currency,omitempty"`
	MixedCurrency     bool             `json:"mixed_currency"`
	ItemCount         int64            `json:"item_count"`
	LineCount         int              `json:"line_count"`
	UnavailableLines  int              `json:"unavailable_lines"`
	PriceChangedLines int              `json:"price_changed_lines"`
	OverLineLimit     bool             `json:"over_line_limit"`
	CheckoutReady     bool             `json:"checkout_ready"`
	Degraded          degradedResponse `json:"degraded"`
	Limits            limitsResponse   `json:"limits"`
	Page              pageResponse     `json:"page"`
}

func toCartResponse(v *usecase.CartView) cartResponse {
	resp := cartResponse{
		Version: v.Version, Items: make([]lineResponse, 0, len(v.Lines)), Currency: v.Currency, MixedCurrency: v.MixedCurrency,
		ItemCount: v.TotalQuantity, LineCount: v.TotalLines, UnavailableLines: v.UnavailableLines,
		PriceChangedLines: v.PriceChangedLines, OverLineLimit: v.OverLineLimit, CheckoutReady: v.CheckoutReady,
		Degraded: degradedResponse{Catalog: v.CatalogDegraded, Inventory: v.InventoryDegraded},
		Limits:   limitsResponse{MaxLines: domain.MaxLinesPerCart, MaxQuantityPerLine: domain.MaxQuantityPerLine},
		Page:     pageResponse{Limit: v.Page.Limit, Offset: v.Page.Offset, Total: v.TotalLines},
	}
	if v.Subtotal != nil {
		resp.Subtotal = &moneyResponse{Amount: *v.Subtotal, Currency: v.Currency}
		resp.Total = *v.Subtotal
	}
	for _, l := range v.Lines {
		resp.Items = append(resp.Items, lineResponse{
			LineID: l.LineID, LineVersion: l.LineVersion, ProductID: l.ProductID, ProductName: l.ProductName,
			VariantID: l.VariantID, VariantSKU: l.VariantSKU, VariantLabel: l.VariantLabel, Quantity: l.Quantity,
			PriceAmount: l.PriceAmount, Currency: l.Currency, SeenPriceAmount: l.SeenPriceAmount, SeenCurrency: l.SeenCurrency,
			PriceChanged: l.PriceChanged, Subtotal: l.Subtotal, State: string(l.State), StockStatus: string(l.Stock),
			AvailableQuantity: l.AvailableQuantity, Available: l.Available(),
		})
	}
	return resp
}

// ---- internal checkout contract (Order → Cart) ----

type snapshotRequest struct {
	OperationID     string `json:"operation_id" binding:"required,uuid"`
	ExpectedVersion *int64 `json:"expected_version" binding:"omitempty,min=1"`
}

type snapshotLineResponse struct {
	LineID          string  `json:"line_id"`
	ProductID       string  `json:"product_id"`
	VariantID       *string `json:"variant_id,omitempty"`
	Quantity        int64   `json:"quantity"`
	LineVersion     int64   `json:"line_version"`
	SeenPriceAmount *int64  `json:"seen_price_amount,omitempty"`
	SeenCurrency    *string `json:"seen_currency,omitempty"`
}

type snapshotResponse struct {
	OperationID string                 `json:"operation_id"`
	BuyerID     string                 `json:"buyer_id"`
	CartVersion int64                  `json:"cart_version"`
	CreatedAt   time.Time              `json:"created_at"`
	Consumed    bool                   `json:"consumed"`
	Replayed    bool                   `json:"replayed"`
	Lines       []snapshotLineResponse `json:"lines"`
}

func toSnapshotResponse(op *domain.CheckoutOperation, replayed bool) snapshotResponse {
	resp := snapshotResponse{OperationID: op.OperationID, BuyerID: op.BuyerID, CartVersion: op.CartVersion,
		CreatedAt: op.CreatedAt, Consumed: op.Consumed(), Replayed: replayed, Lines: make([]snapshotLineResponse, 0, len(op.Lines))}
	for _, l := range op.Lines {
		resp.Lines = append(resp.Lines, snapshotLineResponse{LineID: l.LineID, ProductID: l.ProductID, VariantID: l.VariantID,
			Quantity: l.Quantity, LineVersion: l.LineVersion, SeenPriceAmount: l.SeenPriceAmount, SeenCurrency: l.SeenCurrency})
	}
	return resp
}

type consumeLineRequest struct {
	LineID   string `json:"line_id" binding:"required,uuid"`
	Quantity int64  `json:"quantity" binding:"required,min=1"`
}

type consumeRequest struct {
	Lines []consumeLineRequest `json:"lines" binding:"required,min=1,max=50,dive"`
}

type consumeResponse struct {
	OperationID string                     `json:"operation_id"`
	CartVersion int64                      `json:"cart_version"`
	ConsumedAt  time.Time                  `json:"consumed_at"`
	Replayed    bool                       `json:"replayed"`
	Lines       []domain.ConsumeLineResult `json:"lines"`
}
