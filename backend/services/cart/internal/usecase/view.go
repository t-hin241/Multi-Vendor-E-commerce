package usecase

import (
	"context"
	"sync"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/purchasequote"
	"shopee/backend/services/cart/internal/domain"
)

const (
	// lookupConcurrency bounds how many Catalog/Inventory calls one cart
	// view runs at once.
	lookupConcurrency = 8
	// lookupTimeout bounds the whole enrichment phase of one cart view.
	lookupTimeout = 4 * time.Second
	// slowLookup is logged so lookup latency can be watched.
	slowLookup = time.Second

	DefaultPageLimit = domain.MaxLinesPerCart
	MaxPageLimit     = domain.MaxLinesPerCart
	MaxPageOffset    = 1000
)

// Page selects a slice of cart lines. Totals always cover the whole cart.
type Page struct {
	Limit  int
	Offset int
}

// LineView is one cart line enriched with live Catalog/Inventory data.
type LineView struct {
	VendorID           string
	PricingVersion     int64
	SeenPricingVersion *int64
	LineID             string
	LineVersion        int64
	ProductID          string
	ProductName        string
	VariantID          *string
	VariantSKU         *string
	VariantLabel       *string
	Quantity           int64
	// PriceAmount/Currency are Catalog's live price; nil/"" when Catalog
	// could not be reached (the line is then unverified, never priced from
	// a stale reference).
	PriceAmount       *int64
	Currency          string
	SeenPriceAmount   *int64
	SeenCurrency      *string
	Subtotal          *int64
	State             domain.LineState
	Stock             domain.StockStatus
	AvailableQuantity *int64
	PriceChanged      bool
}

// Available keeps the pre-upgrade boolean for legacy clients.
func (l LineView) Available() bool { return l.State.Purchasable() }

// CartView is the buyer's cart. Subtotal is an estimate at current prices;
// Order computes the final amount at checkout.
type CartView struct {
	CartID                   string
	SelectiveCheckoutEnabled bool
	Version                  int64
	Lines                    []LineView
	TotalLines               int
	TotalQuantity            int64
	Subtotal                 *int64
	Currency                 string
	MixedCurrency            bool
	UnavailableLines         int
	PriceChangedLines        int
	OverLineLimit            bool
	CatalogDegraded          bool
	InventoryDegraded        bool
	CheckoutReady            bool
	Page                     Page
}

func (uc *CartUseCase) View(ctx context.Context, userID string, page Page) (*CartView, error) {
	cart, err := uc.carts.GetOrCreateForUser(ctx, userID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	items, err := uc.items.ListByCart(ctx, cart.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	facts := uc.lookup(ctx, items)
	view := &CartView{CartID: cart.ID, SelectiveCheckoutEnabled: uc.SelectiveCheckoutEnabled, Version: cart.Version, TotalLines: len(items), OverLineLimit: len(items) > domain.MaxLinesPerCart, Page: page,
		CatalogDegraded: facts.catalogFailures > 0, InventoryDegraded: facts.inventoryFailures > 0}

	all := make([]LineView, 0, len(items))
	currencies := map[string]bool{}
	var subtotal int64
	subtotalOK := true
	for _, item := range items {
		line := facts.lineView(item)
		all = append(all, line)
		view.TotalQuantity += item.Quantity
		if line.PriceChanged {
			view.PriceChangedLines++
		}
		if !line.State.Purchasable() {
			view.UnavailableLines++
			continue
		}
		currencies[line.Currency] = true
		if line.Subtotal == nil {
			subtotalOK = false
			continue
		}
		var ok bool
		if subtotal, ok = domain.AddAmounts(subtotal, *line.Subtotal); !ok {
			subtotalOK = false
		}
	}

	view.MixedCurrency = len(currencies) > 1
	if !view.MixedCurrency && subtotalOK {
		for c := range currencies {
			view.Currency = c
		}
		view.Subtotal = &subtotal
	}
	view.CheckoutReady = len(items) > 0 && view.UnavailableLines == 0 && view.PriceChangedLines == 0 &&
		!view.MixedCurrency && !view.OverLineLimit && view.Subtotal != nil

	start := min(page.Offset, len(all))
	end := min(start+page.Limit, len(all))
	view.Lines = all[start:end]
	return view, nil
}

// lineFacts is everything learned about a cart's lines in one bounded,
// deduplicated round of Catalog/Inventory lookups.
type lineFacts struct {
	quotes            map[string]purchasequote.Line
	variantStock      map[string]int64
	variantStockKnown bool
	productStock      map[string]domain.StockFacts
	catalogFailures   int
	inventoryFailures int
}

func (f *lineFacts) lineView(item *domain.CartItem) LineView {
	line := LineView{
		LineID: item.ID, LineVersion: item.Version, ProductID: item.ProductID, VariantID: item.VariantID,
		Quantity: item.Quantity, SeenPriceAmount: item.SeenPriceAmount, SeenCurrency: item.SeenCurrency,
	}

	var product *domain.ProductFacts
	var variant *domain.VariantFacts
	var stock domain.StockFacts
	line.SeenPricingVersion = item.SeenPricingVersion
	q, found := f.quotes[purchasequote.Key(item.ProductID, item.VariantID)]
	if found {
		product = &domain.ProductFacts{Found: q.ProductFound, Status: q.Status, IsVisible: q.IsSellable, HasVariants: q.HasVariants, PriceAmount: q.UnitPrice, Currency: q.Currency, PricingVersion: q.VariantVersion,
			ShopPaused: q.ShopPaused}
		line.ProductName = q.ProductName
		line.VendorID = q.VendorID
		line.PricingVersion = q.VariantVersion
		if q.ProductFound && q.UnitPrice > 0 {
			price := q.UnitPrice
			line.PriceAmount = &price
			line.Currency = q.Currency
			if subtotal, ok := domain.LineSubtotal(price, item.Quantity); ok {
				line.Subtotal = &subtotal
			}
		}
		if item.VariantID != nil {
			variant = &domain.VariantFacts{Found: q.VariantFound, ProductID: q.ProductID}
			sku, label := q.SKU, q.VariantLabel
			line.VariantSKU = &sku
			line.VariantLabel = &label
		}
	}
	if item.VariantID != nil {
		if f.variantStockKnown {
			stock = domain.StockFacts{Known: true, Available: f.variantStock[*item.VariantID]}
		}
	} else {
		stock = f.productStock[item.ProductID]
	}

	eval := domain.EvaluateLine(item, product, variant, false, stock)
	line.State, line.Stock, line.PriceChanged = eval.State, eval.Stock, eval.PriceChanged
	if stock.Known && (eval.Stock == domain.StockInsufficient || eval.Stock == domain.StockOut) {
		available := max(stock.Available, 0)
		line.AvailableQuantity = &available
	}
	if !line.State.Purchasable() {
		line.Subtotal = nil
	}
	return line
}

// lookup resolves every distinct product, variant and stock figure the
// lines need, with bounded concurrency and one overall deadline. Failures
// are recorded, never turned into a guess.
func (uc *CartUseCase) lookup(ctx context.Context, items []*domain.CartItem) *lineFacts {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	_, variantIDs, plainProductIDs := distinctIDs(items)
	facts := &lineFacts{productStock: map[string]domain.StockFacts{}, quotes: map[string]purchasequote.Line{}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if len(items) == 0 {
			return
		}
		// Old oversized carts stay readable, in bounded batches.
		for start := 0; start < len(items); start += domain.MaxLinesPerCart {
			batch := items[start:min(start+domain.MaxLinesPerCart, len(items))]
			inputs := make([]purchasequote.Input, 0, len(batch))
			for _, item := range batch {
				inputs = append(inputs, purchasequote.Input{ProductID: item.ProductID, VariantID: item.VariantID, Quantity: min(item.Quantity, domain.MaxQuantityPerLine)})
			}
			quote, err := uc.catalog.PurchaseQuotes(ctx, inputs)
			if err != nil {
				facts.catalogFailures += len(batch)
				continue
			}
			for _, q := range quote.Lines {
				facts.quotes[purchasequote.Key(q.ProductID, q.VariantID)] = q
			}
		}
	}()
	go func() { defer wg.Done(); uc.lookupStock(ctx, variantIDs, plainProductIDs, facts) }()
	wg.Wait()

	elapsed := time.Since(started)
	if facts.catalogFailures > 0 || facts.inventoryFailures > 0 || elapsed > slowLookup {
		uc.log.Warn().Str("request_id", middleware.RequestIDFromContext(ctx)).Int("lines", len(items)).
			Int("catalog_failures", facts.catalogFailures).Int("inventory_failures", facts.inventoryFailures).
			Int64("duration_ms", elapsed.Milliseconds()).Msg("cart_lookup_degraded")
	}
	return facts
}

func distinctIDs(items []*domain.CartItem) (products, variants, plainProducts []string) {
	seenP, seenV, seenPlain := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range items {
		if !seenP[item.ProductID] {
			seenP[item.ProductID] = true
			products = append(products, item.ProductID)
		}
		if item.VariantID != nil {
			if !seenV[*item.VariantID] {
				seenV[*item.VariantID] = true
				variants = append(variants, *item.VariantID)
			}
		} else if !seenPlain[item.ProductID] {
			seenPlain[item.ProductID] = true
			plainProducts = append(plainProducts, item.ProductID)
		}
	}
	return products, variants, plainProducts
}

func (uc *CartUseCase) lookupStock(ctx context.Context, variantIDs, plainProductIDs []string, facts *lineFacts) {
	var mu sync.Mutex
	if len(variantIDs) > 0 {
		stock, err := uc.inventory.GetVariantStock(ctx, variantIDs)
		if err != nil {
			facts.inventoryFailures++
		} else {
			facts.variantStock, facts.variantStockKnown = stock, true
		}
	}
	forEachLimited(plainProductIDs, func(id string) {
		qty, exists, err := uc.inventory.GetProductStock(ctx, id)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			facts.inventoryFailures++
			return
		}
		if !exists {
			qty = 0
		}
		facts.productStock[id] = domain.StockFacts{Known: true, Available: qty}
	})
}

func forEachLimited(ids []string, fn func(id string)) {
	sem := make(chan struct{}, lookupConcurrency)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(id)
		}(id)
	}
	wg.Wait()
}
