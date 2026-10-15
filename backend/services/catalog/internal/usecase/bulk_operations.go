package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/bulkimport"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/repository"
)

// BulkStore is what AF-16 imports read and record in Catalog.
type BulkStore interface {
	Targets(ctx context.Context, vendor string, skus, variantIDs, productIDs []string) ([]domain.BulkTarget, error)
	PriceRows(ctx context.Context, vendor, afterProduct, afterVariant string, limit int) ([]domain.BulkTarget, error)
	ImportRefs(ctx context.Context, vendor string, ids []string) (map[string]string, error)
	SaveImportRef(ctx context.Context, vendor, clientRowID, productID, jobID string, rowNo int) error
}

// BulkAuthorizer checks products.read/products.write for import jobs.
func (uc *ProductUseCase) BulkAuthorizer() bulkimport.Authorizer {
	return func(ctx context.Context, actor, vendor string, write bool) error {
		if actor == "" {
			return apperror.Unauthorized("Authentication required")
		}
		permission := shopaccess.ProductsRead
		if write {
			permission = shopaccess.ProductsWrite
		}
		_, err := uc.vendors.GetApprovedVendorID(ctx, actor, vendor, permission)
		return err
	}
}

func rowError(code, field, message string) *bulkimport.RowError {
	return &bulkimport.RowError{Code: code, Field: field, Message: message}
}

// staleRow turns the owner's version conflicts into the import's code.
func staleRow(err error) error {
	var app *apperror.Error
	if errors.As(err, &app) && app.Status == http.StatusConflict {
		return bulkimport.Problem(http.StatusConflict, "preview_stale", "The product or SKU changed after the preview; export again and create a new job ("+app.Message+")")
	}
	return err
}

// VendorTargets is the internal lookup Inventory uses to resolve SKUs and
// products of one shop for stock imports.
func (uc *ProductUseCase) VendorTargets(ctx context.Context, store BulkStore, vendor string, skus, variantIDs, productIDs []string) ([]domain.BulkTarget, error) {
	if _, err := uuid.Parse(vendor); err != nil {
		return nil, apperror.Validation("vendor_id must be a UUID")
	}
	if len(skus) > 1000 || len(variantIDs) > 1000 || len(productIDs) > 1000 {
		return nil, apperror.Validation("At most 1000 values per list")
	}
	for _, id := range append(append([]string{}, variantIDs...), productIDs...) {
		if _, err := uuid.Parse(id); err != nil {
			return nil, apperror.Validation("variant_ids and product_ids must be UUIDs")
		}
	}
	out, err := store.Targets(ctx, vendor, nonNil(skus), nonNil(variantIDs), nonNil(productIDs))
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return out, nil
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// ProductImport creates draft products (template products v1). Content
// edits of existing products stay in the editor.
type ProductImport struct {
	UC    *ProductUseCase
	Store BulkStore
}

type productImportPayload struct {
	ClientRowID string                `json:"client_row_id"`
	CategoryID  string                `json:"category_id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	PriceAmount int64                 `json:"price_amount"`
	Attributes  []AttributeValueInput `json:"attributes"`
}

func (ProductImport) Template() bulkimport.Template {
	return bulkimport.Template{Kind: "products", Version: 1, DynamicPrefix: "attr.", Columns: []bulkimport.Column{
		{Name: "client_row_id", Required: true, Description: "Your stable id for the row (letters, digits, . _ -, max 64). One id creates at most one product in the shop.", Example: "SP-0001"},
		{Name: "category_id", Required: true, Description: "Category id from the product editor.", Example: "00000000-0000-0000-0000-000000000000"},
		{Name: "name", Required: true, Description: "Product name, max 300 bytes, plain text.", Example: "Áo thun cotton"},
		{Name: "description", Description: "Plain text; blank lines start a new paragraph. No HTML or links.", Example: "Chất liệu cotton 100%"},
		{Name: "price_amount", Required: true, Description: "Price in whole VND, digits only.", Example: "199000"},
	}, Notes: []string{
		"Products are created as drafts; add images, SKUs and stock in the editor, then submit for review.",
		"Category attributes go in columns attr.<attribute code>; choose options by their exact value, several options separated by |.",
		"A client_row_id that already created a product is refused, so corrected rows can be uploaded again safely.",
	}}
}

func (ProductImport) Available(context.Context, string) (bool, string, error) { return true, "", nil }

func (k ProductImport) Plan(ctx context.Context, vendor string, records []bulkimport.Record) ([]bulkimport.Plan, error) {
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.Values["client_row_id"])
	}
	refs, err := k.Store.ImportRefs(ctx, vendor, ids)
	if err != nil {
		return nil, err
	}
	templates := map[string][]domain.ResolvedAttribute{}
	categoryErrors := map[string]*bulkimport.RowError{}
	template := func(category string) ([]domain.ResolvedAttribute, *bulkimport.RowError, error) {
		if e, ok := categoryErrors[category]; ok {
			return nil, e, nil
		}
		if t, ok := templates[category]; ok {
			return t, nil, nil
		}
		if _, err := uuid.Parse(category); err != nil {
			categoryErrors[category] = rowError("invalid_category", "category_id", "category_id must be a category UUID")
			return nil, categoryErrors[category], nil
		}
		if err := k.UC.validateCategory(ctx, category); err != nil {
			var app *apperror.Error
			if errors.As(err, &app) && app.Status < 500 {
				categoryErrors[category] = rowError("invalid_category", "category_id", app.Message)
				return nil, categoryErrors[category], nil
			}
			return nil, nil, err
		}
		t, err := k.UC.attributeTemplate.ResolveTemplate(ctx, category)
		if err != nil {
			return nil, nil, err
		}
		templates[category] = t
		return t, nil, nil
	}
	plans := make([]bulkimport.Plan, len(records))
	for i, r := range records {
		v := r.Values
		id := v["client_row_id"]
		if !domain.ValidClientRowID(id) {
			plans[i] = bulkimport.Plan{Error: rowError("invalid_value", "client_row_id", "client_row_id must be 1-64 letters, digits, '.', '_' or '-'")}
			continue
		}
		plans[i].TargetKey = "client:" + id
		if product, ok := refs[id]; ok {
			plans[i].Error = rowError("already_imported", "client_row_id", "This client_row_id already created product "+product)
			plans[i].Old = map[string]any{"product_id": product}
			continue
		}
		resolved, rowErr, err := template(v["category_id"])
		if err != nil {
			return nil, err
		}
		if rowErr != nil {
			plans[i].Error = rowErr
			continue
		}
		price, ok := domain.ParseImportAmount(v["price_amount"])
		if !ok {
			plans[i].Error = rowError("invalid_amount", "price_amount", "price_amount must be a positive whole number of VND, digits only")
			continue
		}
		description := domain.PlainTextDescription(v["description"])
		if err := domain.ValidateProductInput(v["name"], description, price); err != nil {
			field := "description"
			if strings.TrimSpace(v["name"]) == "" || len(v["name"]) > 300 {
				field = "name"
			}
			plans[i].Error = rowError("invalid_value", field, err.Error())
			continue
		}
		attrs, field, err := importAttributes(resolved, v)
		if err == nil {
			_, _, err = buildAttributeValues(resolved, attrs)
		}
		if err != nil {
			var app *apperror.Error
			if !errors.As(err, &app) || app.Status >= 500 {
				return nil, err
			}
			plans[i].Error = rowError("invalid_attribute", field, app.Message)
			continue
		}
		plans[i].Payload = productImportPayload{ClientRowID: id, CategoryID: v["category_id"], Name: v["name"], Description: description, PriceAmount: price, Attributes: attrs}
		plans[i].New = map[string]any{"name": v["name"], "category_id": v["category_id"], "price_amount": price, "status": string(domain.StatusDraft)}
	}
	return plans, nil
}

// importAttributes maps attr.<code> columns onto the category template:
// option columns name options by value, several separated by "|".
func importAttributes(resolved []domain.ResolvedAttribute, values map[string]string) ([]AttributeValueInput, string, error) {
	byCode := map[string]domain.ResolvedAttribute{}
	for _, r := range resolved {
		byCode[r.Attribute.Code] = r
	}
	var out []AttributeValueInput
	for column, value := range values {
		code, ok := strings.CutPrefix(column, "attr.")
		if !ok || value == "" {
			continue
		}
		r, ok := byCode[code]
		if !ok {
			return nil, column, apperror.Validation("Attribute " + code + " does not apply to this category")
		}
		if r.Attribute.IsVariantDefining {
			return nil, column, apperror.Validation(r.Attribute.Name + " defines SKUs; add SKUs in the product editor")
		}
		in := AttributeValueInput{AttributeID: r.Attribute.ID}
		if r.Attribute.DataType.IsChoice() {
			for _, part := range strings.Split(value, "|") {
				part = strings.TrimSpace(part)
				found := ""
				for _, o := range r.Options {
					if strings.EqualFold(strings.TrimSpace(o.Value), part) {
						found = o.ID
					}
				}
				if found == "" {
					return nil, column, apperror.Validation(fmt.Sprintf("%q is not an option of %s", part, r.Attribute.Name))
				}
				in.OptionIDs = append(in.OptionIDs, found)
			}
		} else {
			value := value
			in.Value = &value
		}
		out = append(out, in)
	}
	return out, "", nil
}

func (k ProductImport) Apply(ctx context.Context, actor, vendor string, row bulkimport.Row) (bulkimport.Result, error) {
	var in productImportPayload
	if err := json.Unmarshal(row.Payload, &in); err != nil {
		return bulkimport.Result{}, err
	}
	if !repository.InTransaction(ctx) {
		return bulkimport.Result{}, errors.New("product import must run in the job transaction")
	}
	p, err := k.UC.Create(ctx, actor, vendor, in.CategoryID, in.Name, in.Description, in.PriceAmount, in.Attributes)
	if err != nil {
		return bulkimport.Result{}, err
	}
	if err = k.Store.SaveImportRef(ctx, vendor, in.ClientRowID, p.ID, row.JobID, row.RowNo); errors.Is(err, repository.ErrImportRefTaken) {
		return bulkimport.Result{}, bulkimport.Problem(http.StatusConflict, "already_imported", "This client_row_id already created a product")
	}
	return bulkimport.Result{Ref: "product:" + p.ID}, err
}

func (ProductImport) Export(context.Context, string, func([]string) error) error {
	return bulkimport.ErrNoExport
}

// PriceImport updates SKU prices and prices of products without SKUs
// (template prices v1), each through the editor's use case.
type PriceImport struct {
	UC    *ProductUseCase
	Store BulkStore
}

type priceImportPayload struct {
	ProductID       string `json:"product_id"`
	VariantID       string `json:"variant_id,omitempty"`
	PriceAmount     int64  `json:"price_amount"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (PriceImport) Template() bulkimport.Template {
	return bulkimport.Template{Kind: "prices", Version: 1, Exportable: true, Columns: []bulkimport.Column{
		{Name: "sku", Description: "SKU of the shop. Leave empty to price a product without SKUs.", Example: "AO-THUN-DO-L"},
		{Name: "product_id", Description: "Product id; required when sku is empty, must match the SKU's product otherwise.", Example: "00000000-0000-0000-0000-000000000000"},
		{Name: "price_amount", Required: true, Description: "New price in whole VND, digits only.", Example: "189000"},
		{Name: "expected_version", Description: "Version from the export (SKU price version, or product version). A changed price is refused instead of overwritten.", Example: "3"},
		{Name: "product_name", ReadOnly: true, Description: "For reference; ignored on import."},
	}, Notes: []string{
		"Export the current prices, edit price_amount and upload the same file; unchanged rows are skipped.",
		"A price change sends an approved product back to review, as in the editor.",
	}}
}

func (PriceImport) Available(context.Context, string) (bool, string, error) { return true, "", nil }

func (k PriceImport) Plan(ctx context.Context, vendor string, records []bulkimport.Record) ([]bulkimport.Plan, error) {
	var skus, productIDs []string
	for _, r := range records {
		if sku := r.Values["sku"]; sku != "" {
			skus = append(skus, sku)
		} else if _, err := uuid.Parse(r.Values["product_id"]); err == nil {
			productIDs = append(productIDs, r.Values["product_id"])
		}
	}
	targets, err := k.Store.Targets(ctx, vendor, nonNil(skus), []string{}, nonNil(productIDs))
	if err != nil {
		return nil, err
	}
	bySKU, byProduct := map[string]domain.BulkTarget{}, map[string]domain.BulkTarget{}
	for _, t := range targets {
		if t.VariantID != "" {
			bySKU[t.SKU] = t
		} else {
			byProduct[t.ProductID] = t
		}
	}
	plans := make([]bulkimport.Plan, len(records))
	for i, r := range records {
		plans[i] = k.planPrice(r.Values, bySKU, byProduct)
	}
	return plans, nil
}

func (k PriceImport) planPrice(v map[string]string, bySKU, byProduct map[string]domain.BulkTarget) bulkimport.Plan {
	amount, ok := domain.ParseImportAmount(v["price_amount"])
	if !ok {
		return bulkimport.Plan{Error: rowError("invalid_amount", "price_amount", "price_amount must be a positive whole number of VND, digits only")}
	}
	var expected *int64
	if raw := v["expected_version"]; raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 || raw[0] == '+' {
			return bulkimport.Plan{Error: rowError("invalid_value", "expected_version", "expected_version must be a positive whole number")}
		}
		expected = &n
	}
	var t domain.BulkTarget
	var current, version int64
	var p bulkimport.Plan
	if sku := v["sku"]; sku != "" {
		if t, ok = bySKU[sku]; !ok {
			return bulkimport.Plan{Error: rowError("target_not_found", "sku", "SKU not found in this shop")}
		}
		if id := v["product_id"]; id != "" && id != t.ProductID {
			return bulkimport.Plan{Error: rowError("target_mismatch", "product_id", "product_id does not match the SKU's product")}
		}
		if !k.UC.VariantPricingEnabled && amount != t.ProductPrice {
			return bulkimport.Plan{Error: rowError("variant_pricing_disabled", "price_amount", "Different prices per SKU are not enabled; change the product price instead")}
		}
		current, version, p.TargetKey = t.VariantPrice, t.PriceVersion, "variant:"+t.VariantID
	} else {
		if t, ok = byProduct[v["product_id"]]; !ok {
			return bulkimport.Plan{Error: rowError("target_not_found", "product_id", "Give a SKU, or the id of a product of this shop")}
		}
		if t.HasVariants {
			return bulkimport.Plan{Error: rowError("use_sku", "sku", "This product is priced per SKU; add one row per SKU")}
		}
		current, version, p.TargetKey = t.ProductPrice, t.ProductVersion, "product:"+t.ProductID
	}
	if expected != nil && *expected != version {
		return bulkimport.Plan{TargetKey: p.TargetKey, Error: rowError("preview_stale", "expected_version", fmt.Sprintf("The price changed since your export (now version %d); export again", version))}
	}
	p.Old = map[string]any{"price_amount": current, "status": string(t.ProductStatus), "product_name": t.ProductName}
	p.New = map[string]any{"price_amount": amount}
	if amount == current {
		p.Unchanged = true
		return p
	}
	if t.ProductStatus != domain.StatusDraft {
		p.New["status"] = string(domain.StatusDraft)
		p.New["requires_review"] = true
	}
	p.ExpectedVersion = &version
	p.Payload = priceImportPayload{ProductID: t.ProductID, VariantID: t.VariantID, PriceAmount: amount, ExpectedVersion: version}
	return p
}

func (k PriceImport) Apply(ctx context.Context, actor, _ string, row bulkimport.Row) (bulkimport.Result, error) {
	var in priceImportPayload
	if err := json.Unmarshal(row.Payload, &in); err != nil {
		return bulkimport.Result{}, err
	}
	if in.VariantID != "" {
		v, err := k.UC.UpdateVariantPrice(ctx, actor, in.ProductID, in.VariantID, in.PriceAmount, in.ExpectedVersion)
		if err != nil {
			return bulkimport.Result{}, staleRow(err)
		}
		return bulkimport.Result{Ref: fmt.Sprintf("variant:%s@%d", v.ID, v.PriceVersion)}, nil
	}
	p, err := k.UC.UpdatePrice(ctx, actor, in.ProductID, in.PriceAmount, in.ExpectedVersion)
	if err != nil {
		return bulkimport.Result{}, staleRow(err)
	}
	return bulkimport.Result{Ref: fmt.Sprintf("product:%s@%d", p.ID, p.Version)}, nil
}

func (k PriceImport) Export(ctx context.Context, vendor string, write func([]string) error) error {
	afterProduct, afterVariant := "", ""
	for {
		rows, err := k.Store.PriceRows(ctx, vendor, afterProduct, afterVariant, 500)
		if err != nil {
			return err
		}
		for _, t := range rows {
			price, version := t.ProductPrice, t.ProductVersion
			if t.VariantID != "" {
				price, version = t.VariantPrice, t.PriceVersion
			}
			if err = write([]string{t.SKU, t.ProductID, strconv.FormatInt(price, 10), strconv.FormatInt(version, 10), t.ProductName}); err != nil {
				return err
			}
			afterProduct, afterVariant = t.ProductID, t.VariantID
		}
		if len(rows) < 500 {
			return nil
		}
	}
}
