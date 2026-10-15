package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/bulkimport"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/inventory/internal/domain"
)

// BulkCatalog resolves a shop's SKUs and products with Catalog.
type BulkCatalog interface {
	VendorTargets(ctx context.Context, vendor string, skus, variantIDs, productIDs []string) ([]domain.CatalogTarget, error)
}

// BulkItemStore reads a shop's stock items for imports and exports.
type BulkItemStore interface {
	ForTargets(ctx context.Context, vendor string, productIDs, variantIDs []string) ([]domain.InventoryItem, error)
	After(ctx context.Context, vendor, after string, limit int) ([]domain.InventoryItem, error)
}

// BulkAuthorizer checks inventory.read/inventory.adjust for import jobs.
func (u StockSelfService) BulkAuthorizer() bulkimport.Authorizer {
	return func(ctx context.Context, actor, vendor string, write bool) error {
		if actor == "" {
			return apperror.Unauthorized("Authentication required")
		}
		permission := shopaccess.InventoryRead
		if write {
			permission = shopaccess.InventoryAdjust
		}
		_, err := u.Vendors.GetApprovedVendorID(ctx, actor, vendor, permission)
		return err
	}
}

// StockReceiptImport records goods received for many SKUs at once (template
// stock_receipts v1). Each row is one AF-13 receipt adjustment: a delta,
// never an absolute overwrite; counts keep their own flow.
type StockReceiptImport struct {
	Stock   StockSelfService
	Catalog BulkCatalog
	Items   BulkItemStore
}

var receiptQuantity = regexp.MustCompile(`^[0-9]{1,18}$`)

func (StockReceiptImport) Template() bulkimport.Template {
	return bulkimport.Template{Kind: "stock_receipts", Version: 1, Exportable: true, Columns: []bulkimport.Column{
		{Name: "sku", Description: "SKU of the shop. Leave empty for a product without SKUs.", Example: "AO-THUN-DO-L"},
		{Name: "product_id", Description: "Product id; required when sku is empty, must match the SKU's product otherwise.", Example: "00000000-0000-0000-0000-000000000000"},
		{Name: "quantity", Required: true, Description: "Units received, a positive whole number. Added to available stock.", Example: "50"},
		{Name: "reason", Required: true, Description: "Why stock is added (3-500 bytes).", Example: "Nhập hàng đợt 10/2026"},
		{Name: "evidence_ref", Required: true, Description: "Delivery note or invoice number (1-120 bytes).", Example: "PN-2026-0012"},
		{Name: "product_name", ReadOnly: true, Description: "For reference; ignored on import."},
		{Name: "available", ReadOnly: true, Description: "Available stock at export; ignored on import."},
		{Name: "reserved", ReadOnly: true, Description: "Reserved stock at export; ignored on import."},
	}, Notes: []string{
		"Each row adds quantity to available stock (a receipt). To set an exact count use the stock count form.",
		"Receipts above the shop's limit wait for marketplace approval, as single receipts do.",
		"Returned goods are not received here; they go through the inspected return flow.",
		"Stock that changed after the preview (for example new orders) fails the row; upload those rows again.",
	}}
}

func (k StockReceiptImport) Available(ctx context.Context, vendor string) (bool, string, error) {
	if !k.Stock.Enabled {
		return false, "Stock self-service is not enabled", nil
	}
	p, err := k.Stock.Store.Policy(ctx, vendor)
	if err != nil {
		return false, "", err
	}
	if !p.Enabled {
		return false, "Stock self-service is not enabled for this shop", nil
	}
	return true, "", nil
}

func (k StockReceiptImport) Plan(ctx context.Context, vendor string, records []bulkimport.Record) ([]bulkimport.Plan, error) {
	skus, productIDs := []string{}, []string{}
	for _, r := range records {
		if sku := r.Values["sku"]; sku != "" {
			skus = append(skus, sku)
		} else if _, err := uuid.Parse(r.Values["product_id"]); err == nil {
			productIDs = append(productIDs, r.Values["product_id"])
		}
	}
	targets, err := k.Catalog.VendorTargets(ctx, vendor, skus, []string{}, productIDs)
	if err != nil {
		return nil, err
	}
	bySKU, byProduct := map[string]domain.CatalogTarget{}, map[string]domain.CatalogTarget{}
	variantIDs, plainIDs := []string{}, []string{}
	for _, t := range targets {
		if t.VariantID != "" {
			bySKU[t.SKU] = t
			variantIDs = append(variantIDs, t.VariantID)
		} else {
			byProduct[t.ProductID] = t
			plainIDs = append(plainIDs, t.ProductID)
		}
	}
	items, err := k.Items.ForTargets(ctx, vendor, plainIDs, variantIDs)
	if err != nil {
		return nil, err
	}
	byVariant, byPlain := map[string]domain.InventoryItem{}, map[string]domain.InventoryItem{}
	for _, i := range items {
		if i.VariantID != nil {
			byVariant[*i.VariantID] = i
		} else {
			byPlain[i.ProductID] = i
		}
	}
	policy, err := k.Stock.Store.Policy(ctx, vendor)
	if err != nil {
		return nil, err
	}
	plans := make([]bulkimport.Plan, len(records))
	for n, r := range records {
		plans[n] = planReceipt(r.Values, bySKU, byProduct, byVariant, byPlain, policy.Limits.Receipt)
	}
	return plans, nil
}

func receiptError(code, field, message string) bulkimport.Plan {
	return bulkimport.Plan{Error: &bulkimport.RowError{Code: code, Field: field, Message: message}}
}

func planReceipt(v map[string]string, bySKU, byProduct map[string]domain.CatalogTarget, byVariant, byPlain map[string]domain.InventoryItem, limit int64) bulkimport.Plan {
	q, err := strconv.ParseInt(v["quantity"], 10, 64)
	if !receiptQuantity.MatchString(v["quantity"]) || err != nil || q < 1 {
		return receiptError("invalid_quantity", "quantity", "quantity must be a positive whole number")
	}
	var t domain.CatalogTarget
	var item domain.InventoryItem
	var ok bool
	if sku := v["sku"]; sku != "" {
		if t, ok = bySKU[sku]; !ok {
			return receiptError("target_not_found", "sku", "SKU not found in this shop")
		}
		if id := v["product_id"]; id != "" && id != t.ProductID {
			return receiptError("target_mismatch", "product_id", "product_id does not match the SKU's product")
		}
		item, ok = byVariant[t.VariantID]
	} else {
		if t, ok = byProduct[v["product_id"]]; !ok {
			return receiptError("target_not_found", "product_id", "Give a SKU, or the id of a product of this shop")
		}
		if t.HasVariants {
			return receiptError("use_sku", "sku", "This product keeps stock per SKU; add one row per SKU")
		}
		item, ok = byPlain[t.ProductID]
	}
	if !ok {
		return receiptError("no_stock_item", "sku", "This product has no stock record yet; create its stock in the product page first")
	}
	a := domain.StockAdjustment{ItemID: item.ID, Kind: "receipt", QuantityDelta: q, Reason: v["reason"], EvidenceRef: v["evidence_ref"], ExpectedVersion: item.Version}
	if err := a.Normalize(); err != nil {
		field := "reason"
		if l := len(a.Reason); l >= 3 && l <= 500 {
			field = "evidence_ref"
		}
		return receiptError("invalid_value", field, err.Error())
	}
	if _, err := domain.PlanAdjustment(a, item.AvailableQuantity, item.ReservedQuantity); err != nil {
		return receiptError("invalid_quantity", "quantity", err.Error())
	}
	version := item.Version
	p := bulkimport.Plan{TargetKey: "item:" + item.ID, ExpectedVersion: &version, Payload: a,
		Old: map[string]any{"available": item.AvailableQuantity, "reserved": item.ReservedQuantity, "product_name": t.ProductName, "sku": t.SKU},
		New: map[string]any{"available": item.AvailableQuantity + q}}
	if q > limit {
		p.New["needs_approval"] = true
	}
	return p
}

func (k StockReceiptImport) Apply(ctx context.Context, actor, vendor string, row bulkimport.Row) (bulkimport.Result, error) {
	var a domain.StockAdjustment
	if err := json.Unmarshal(row.Payload, &a); err != nil {
		return bulkimport.Result{}, err
	}
	// The row's operation reference is the AF-13 idempotency key: a replay
	// after a crash returns the first receipt instead of adding stock again.
	out, err := k.Stock.Adjust(ctx, actor, row.OperationRef, a)
	if err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Code == "version_conflict" {
			return bulkimport.Result{}, bulkimport.Problem(http.StatusConflict, "preview_stale", "Stock changed after the preview (for example a new order); upload this row again")
		}
		return bulkimport.Result{}, err
	}
	switch out.Status {
	case "applied":
		return bulkimport.Result{Ref: "movement:" + out.MovementID}, nil
	case "approval_required":
		return bulkimport.Result{Ref: "operation:" + out.OperationID, NeedsApproval: true}, nil
	default:
		return bulkimport.Result{}, bulkimport.Problem(http.StatusConflict, out.Status, fmt.Sprintf("Stock operation ended as %s", out.Status))
	}
}

func (k StockReceiptImport) Export(ctx context.Context, vendor string, write func([]string) error) error {
	after := ""
	for {
		items, err := k.Items.After(ctx, vendor, after, 500)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		productIDs, variantIDs := []string{}, []string{}
		for _, i := range items {
			if i.VariantID != nil {
				variantIDs = append(variantIDs, *i.VariantID)
			} else {
				productIDs = append(productIDs, i.ProductID)
			}
		}
		targets, err := k.Catalog.VendorTargets(ctx, vendor, []string{}, variantIDs, productIDs)
		if err != nil {
			return err
		}
		byVariant, byProduct := map[string]domain.CatalogTarget{}, map[string]domain.CatalogTarget{}
		for _, t := range targets {
			if t.VariantID != "" {
				byVariant[t.VariantID] = t
			} else {
				byProduct[t.ProductID] = t
			}
		}
		for _, i := range items {
			t := byProduct[i.ProductID]
			if i.VariantID != nil {
				t = byVariant[*i.VariantID]
			}
			if err = write([]string{t.SKU, i.ProductID, "", "", "", t.ProductName,
				strconv.FormatInt(i.AvailableQuantity, 10), strconv.FormatInt(i.ReservedQuantity, 10)}); err != nil {
				return err
			}
			after = i.ID
		}
		if len(items) < 500 {
			return nil
		}
	}
}
