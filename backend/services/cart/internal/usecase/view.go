package usecase

import (
	"context"
	"errors"
	"sync"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/cart/internal/adapter"
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
	LineID       string
	LineVersion  int64
	ProductID    string
	ProductName  string
	VariantID    *string
	VariantSKU   *string
	VariantLabel *string
	Quantity     int64
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
	Version           int64
	Lines             []LineView
	TotalLines        int
	TotalQuantity     int64
	Subtotal          *int64
	Currency          string
	MixedCurrency     bool
	UnavailableLines  int
	PriceChangedLines int
	OverLineLimit     bool
	CatalogDegraded   bool
	InventoryDegraded bool
	CheckoutReady     bool
	Page              Page
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
	view := &CartView{Version: cart.Version, TotalLines: len(items), OverLineLimit: len(items) > domain.MaxLinesPerCart, Page: page,
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
	products          map[string]*adapter.ProductInfo // nil value: not found
	productFailed     map[string]bool
	variants          map[string]*adapter.VariantInfo // nil value: not found
	variantFailed     map[string]bool
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
	if !f.productFailed[item.ProductID] {
		info := f.products[item.ProductID]
		product = &domain.ProductFacts{Found: info != nil}
		if info != nil {
			*product = domain.ProductFacts{Found: true, Status: info.Status, IsVisible: info.IsVisible, HasVariants: info.HasVariants,
				PriceAmount: info.PriceAmount, Currency: info.Currency}
			line.ProductName = info.Name
			price := info.PriceAmount
			line.PriceAmount, line.Currency = &price, info.Currency
			if subtotal, ok := domain.LineSubtotal(price, item.Quantity); ok {
				line.Subtotal = &subtotal
			}
		}
	}

	var variant *domain.VariantFacts
	variantFailed := false
	var stock domain.StockFacts
	if item.VariantID != nil {
		variantFailed = f.variantFailed[*item.VariantID]
		if info, ok := f.variants[*item.VariantID]; ok {
			variant = &domain.VariantFacts{Found: info != nil}
			if info != nil {
				variant.ProductID = info.ProductID
				sku, label := info.SKU, variantLabel(info.Options)
				line.VariantSKU, line.VariantLabel = &sku, &label
			}
		}
		if f.variantStockKnown {
			stock = domain.StockFacts{Known: true, Available: f.variantStock[*item.VariantID]}
		}
	} else {
		stock = f.productStock[item.ProductID]
	}

	eval := domain.EvaluateLine(item, product, variant, variantFailed, stock)
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

	productIDs, variantIDs, plainProductIDs := distinctIDs(items)
	facts := &lineFacts{productStock: map[string]domain.StockFacts{}}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		facts.products, facts.productFailed = uc.lookupProducts(ctx, productIDs)
	}()
	go func() {
		defer wg.Done()
		facts.variants, facts.variantFailed = uc.lookupVariants(ctx, variantIDs)
	}()
	go func() {
		defer wg.Done()
		uc.lookupStock(ctx, variantIDs, plainProductIDs, facts)
	}()
	wg.Wait()

	facts.catalogFailures = len(facts.productFailed) + len(facts.variantFailed)
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

// lookupProducts returns found products (a nil entry means Catalog said not
// found) and the ids whose lookup failed for any other reason.
func (uc *CartUseCase) lookupProducts(ctx context.Context, ids []string) (map[string]*adapter.ProductInfo, map[string]bool) {
	found := make(map[string]*adapter.ProductInfo, len(ids))
	failed := map[string]bool{}
	var mu sync.Mutex
	forEachLimited(ids, func(id string) {
		p, err := uc.catalog.GetProduct(ctx, id)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			found[id] = p
		case isNotFound(err):
			found[id] = nil
		default:
			failed[id] = true
		}
	})
	return found, failed
}

func (uc *CartUseCase) lookupVariants(ctx context.Context, ids []string) (map[string]*adapter.VariantInfo, map[string]bool) {
	found := make(map[string]*adapter.VariantInfo, len(ids))
	failed := map[string]bool{}
	var mu sync.Mutex
	forEachLimited(ids, func(id string) {
		v, err := uc.catalog.GetVariant(ctx, id)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			found[id] = v
		case isNotFound(err):
			found[id] = nil
		default:
			failed[id] = true
		}
	})
	return found, failed
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

func isNotFound(err error) bool {
	var appErr *apperror.Error
	return errors.As(err, &appErr) && appErr.Code == apperror.CodeNotFound
}
