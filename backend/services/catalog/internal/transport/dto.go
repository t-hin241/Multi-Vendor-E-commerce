package transport

import (
	"time"

	"shopee/backend/services/catalog/internal/domain"
	"shopee/backend/services/catalog/internal/usecase"
)

type createCategoryRequest struct {
	Name     string  `json:"name" binding:"required"`
	ParentID *string `json:"parent_id"`
}

type categoryResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	ParentID  *string   `json:"parent_id,omitempty"`
	Level     int       `json:"level"`
	CreatedAt time.Time `json:"created_at"`
}

func toCategoryResponse(c *domain.Category) categoryResponse {
	return categoryResponse{ID: c.ID, Name: c.Name, Slug: c.Slug, ParentID: c.ParentID, Level: c.Level, CreatedAt: c.CreatedAt}
}

func toCategoryResponseList(categories []*domain.Category) []categoryResponse {
	out := make([]categoryResponse, 0, len(categories))
	for _, c := range categories {
		out = append(out, toCategoryResponse(c))
	}
	return out
}

type createProductRequest struct {
	CategoryID  string                  `json:"category_id" binding:"required"`
	Name        string                  `json:"name" binding:"required"`
	Description string                  `json:"description"`
	PriceAmount int64                   `json:"price_amount" binding:"required"`
	Attributes  []attributeValueRequest `json:"attributes"`
}

// attributeValueRequest is one submitted attribute value: Value carries a
// text/number/boolean field, OptionIDs carries the chosen option id(s) for
// select (one) / multi_select (one or more) attributes — the same shape
// buildAttributeValues (usecase.AttributeValueInput) expects.
type attributeValueRequest struct {
	AttributeID string   `json:"attribute_id" binding:"required"`
	Value       *string  `json:"value"`
	OptionIDs   []string `json:"option_ids"`
}

func toAttributeValueInputs(reqs []attributeValueRequest) []usecase.AttributeValueInput {
	out := make([]usecase.AttributeValueInput, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, usecase.AttributeValueInput{AttributeID: r.AttributeID, Value: r.Value, OptionIDs: r.OptionIDs})
	}
	return out
}

type setActiveRequest struct {
	IsActive bool `json:"is_active"`
}

type rejectRequest struct {
	Reason string `json:"reason" binding:"required"`
}

type productImageResponse struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Position int    `json:"position"`
}

func toProductImageResponse(img *domain.ProductImage) productImageResponse {
	return productImageResponse{ID: img.ID, URL: img.URL, Position: img.Position}
}

func toProductImageResponseList(images []*domain.ProductImage) []productImageResponse {
	out := make([]productImageResponse, 0, len(images))
	for _, img := range images {
		out = append(out, toProductImageResponse(img))
	}
	return out
}

type productMediaResponse struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Position    int    `json:"position"`
}

func toProductMediaResponse(m *domain.ProductMedia) productMediaResponse {
	return productMediaResponse{ID: m.ID, Kind: string(m.Kind), URL: m.URL, ContentType: m.ContentType, Position: m.Position}
}

func toProductMediaResponseList(items []*domain.ProductMedia) []productMediaResponse {
	out := make([]productMediaResponse, 0, len(items))
	for _, m := range items {
		out = append(out, toProductMediaResponse(m))
	}
	return out
}

type productResponse struct {
	ID                string                   `json:"id"`
	VendorID          string                   `json:"vendor_id"`
	CategoryID        string                   `json:"category_id"`
	Name              string                   `json:"name"`
	Slug              string                   `json:"slug"`
	Description       string                   `json:"description"`
	PriceAmount       int64                    `json:"price_amount"`
	Currency          string                   `json:"currency"`
	Status            string                   `json:"status"`
	RejectionReason   *string                  `json:"rejection_reason,omitempty"`
	IsActive          bool                     `json:"is_active"`
	Images            []productImageResponse   `json:"images,omitempty"`
	Media             []productMediaResponse   `json:"media,omitempty"`
	Attributes        []attributeValueResponse `json:"attributes,omitempty"`
	Variants          []variantResponse        `json:"variants,omitempty"`
	StockInfoDegraded bool                     `json:"stock_info_degraded"`
	VendorName        string                   `json:"vendor_name,omitempty"`
	QuantitySold      int64                    `json:"quantity_sold"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// attributeValueResponse is a captured product attribute value, as raw
// ids/values — the frontend cross-references AttributeID/OptionID against
// the attribute-template it already fetched for the product's category to
// get display labels, so no attribute metadata is joined in here.
type attributeValueResponse struct {
	AttributeID  string   `json:"attribute_id"`
	OptionID     *string  `json:"option_id,omitempty"`
	ValueText    *string  `json:"value_text,omitempty"`
	ValueNumber  *float64 `json:"value_number,omitempty"`
	ValueBoolean *bool    `json:"value_boolean,omitempty"`
}

func toAttributeValueResponseList(values []*domain.ProductAttributeValue) []attributeValueResponse {
	out := make([]attributeValueResponse, 0, len(values))
	for _, v := range values {
		out = append(out, attributeValueResponse{
			AttributeID:  v.AttributeID,
			OptionID:     v.OptionID,
			ValueText:    v.ValueText,
			ValueNumber:  v.ValueNumber,
			ValueBoolean: v.ValueBoolean,
		})
	}
	return out
}

func toProductResponse(p *domain.Product) productResponse {
	return productResponse{
		ID:              p.ID,
		VendorID:        p.VendorID,
		CategoryID:      p.CategoryID,
		Name:            p.Name,
		Slug:            p.Slug,
		Description:     p.Description,
		PriceAmount:     p.PriceAmount,
		Currency:        p.Currency,
		Status:          string(p.Status),
		RejectionReason: p.RejectionReason,
		IsActive:        p.IsActive,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
}

func toProductResponseWithImagesAndMedia(
	p *domain.Product,
	images []*domain.ProductImage,
	media []*domain.ProductMedia,
	attributeValues []*domain.ProductAttributeValue,
	variants []domain.VariantView,
	stockInfoDegraded bool,
) productResponse {
	resp := toProductResponse(p)
	resp.Images = make([]productImageResponse, 0, len(images))
	for _, img := range images {
		resp.Images = append(resp.Images, toProductImageResponse(img))
	}
	resp.Media = toProductMediaResponseList(media)
	resp.Attributes = toAttributeValueResponseList(attributeValues)
	resp.Variants = toPublicVariantResponseList(variants)
	resp.StockInfoDegraded = stockInfoDegraded
	return resp
}

func toProductResponseList(products []*domain.Product) []productResponse {
	out := make([]productResponse, 0, len(products))
	for _, p := range products {
		out = append(out, toProductResponse(p))
	}
	return out
}

// storefrontListResponse is the public listing's response shape: the
// products themselves (each enriched with its main image, vendor name and
// units sold) plus flags telling the client when the vendor-name or
// sold-count enrichment fell back to Catalog's local cache because Vendor
// or Order was unreachable — so the frontend can show a non-blocking
// "may be outdated" notice without ever hiding the products themselves.
type storefrontListResponse struct {
	Products           []productResponse `json:"products"`
	VendorInfoDegraded bool              `json:"vendor_info_degraded"`
	SalesInfoDegraded  bool              `json:"sales_info_degraded"`
}

type createAttributeRequest struct {
	Code              string  `json:"code" binding:"required"`
	Name              string  `json:"name" binding:"required"`
	DataType          string  `json:"data_type" binding:"required"`
	Unit              *string `json:"unit"`
	IsVariantDefining bool    `json:"is_variant_defining"`
}

type addAttributeOptionRequest struct {
	Value string `json:"value" binding:"required"`
}

type setCategoryAttributeRuleRequest struct {
	AttributeID string `json:"attribute_id" binding:"required"`
	IsRequired  bool   `json:"is_required"`
	IsExcluded  bool   `json:"is_excluded"`
	Position    int    `json:"position"`
}

type attributeOptionResponse struct {
	ID       string `json:"id"`
	Value    string `json:"value"`
	Position int    `json:"position"`
}

func toAttributeOptionResponse(o *domain.AttributeOption) attributeOptionResponse {
	return attributeOptionResponse{ID: o.ID, Value: o.Value, Position: o.Position}
}

func toAttributeOptionResponseList(options []*domain.AttributeOption) []attributeOptionResponse {
	out := make([]attributeOptionResponse, 0, len(options))
	for _, o := range options {
		out = append(out, toAttributeOptionResponse(o))
	}
	return out
}

type attributeResponse struct {
	ID                string                    `json:"id"`
	Code              string                    `json:"code"`
	Name              string                    `json:"name"`
	DataType          string                    `json:"data_type"`
	Unit              *string                   `json:"unit,omitempty"`
	IsActive          bool                      `json:"is_active"`
	IsVariantDefining bool                      `json:"is_variant_defining"`
	Options           []attributeOptionResponse `json:"options,omitempty"`
}

func toAttributeResponse(a *domain.Attribute, options []*domain.AttributeOption) attributeResponse {
	return attributeResponse{
		ID: a.ID, Code: a.Code, Name: a.Name, DataType: string(a.DataType), Unit: a.Unit, IsActive: a.IsActive,
		IsVariantDefining: a.IsVariantDefining,
		Options:           toAttributeOptionResponseList(options),
	}
}

func toAttributeResponseList(attributes []*domain.Attribute, optionsByAttribute map[string][]*domain.AttributeOption) []attributeResponse {
	out := make([]attributeResponse, 0, len(attributes))
	for _, a := range attributes {
		out = append(out, toAttributeResponse(a, optionsByAttribute[a.ID]))
	}
	return out
}

type categoryAttributeRuleResponse struct {
	ID          string `json:"id"`
	CategoryID  string `json:"category_id"`
	AttributeID string `json:"attribute_id"`
	Version     int    `json:"version"`
	IsRequired  bool   `json:"is_required"`
	IsExcluded  bool   `json:"is_excluded"`
	Position    int    `json:"position"`
}

func toCategoryAttributeRuleResponse(r *domain.CategoryAttributeRule) categoryAttributeRuleResponse {
	return categoryAttributeRuleResponse{
		ID: r.ID, CategoryID: r.CategoryID, AttributeID: r.AttributeID, Version: r.Version,
		IsRequired: r.IsRequired, IsExcluded: r.IsExcluded, Position: r.Position,
	}
}

// attributeTemplateFieldResponse is one field of a category's effective,
// inheritance-merged attribute template — what the frontend renders a form
// field from.
type attributeTemplateFieldResponse struct {
	AttributeID       string                    `json:"attribute_id"`
	Code              string                    `json:"code"`
	Name              string                    `json:"name"`
	DataType          string                    `json:"data_type"`
	Unit              *string                   `json:"unit,omitempty"`
	Required          bool                      `json:"required"`
	Position          int                       `json:"position"`
	IsVariantDefining bool                      `json:"is_variant_defining"`
	Options           []attributeOptionResponse `json:"options,omitempty"`
}

type attributeTemplateResponse struct {
	Attributes []attributeTemplateFieldResponse `json:"attributes"`
}

func toAttributeTemplateResponse(resolved []domain.ResolvedAttribute) attributeTemplateResponse {
	out := make([]attributeTemplateFieldResponse, 0, len(resolved))
	for _, r := range resolved {
		out = append(out, attributeTemplateFieldResponse{
			AttributeID:       r.Attribute.ID,
			Code:              r.Attribute.Code,
			Name:              r.Attribute.Name,
			DataType:          string(r.Attribute.DataType),
			Unit:              r.Attribute.Unit,
			Required:          r.Required,
			Position:          r.Position,
			IsVariantDefining: r.Attribute.IsVariantDefining,
			Options:           toAttributeOptionResponseListFromValues(r.Options),
		})
	}
	return attributeTemplateResponse{Attributes: out}
}

type createVariantRequest struct {
	SKU       string   `json:"sku" binding:"required"`
	OptionIDs []string `json:"option_ids" binding:"required,min=1"`
}

type variantOptionResponse struct {
	AttributeID   string `json:"attribute_id"`
	AttributeName string `json:"attribute_name"`
	OptionID      string `json:"option_id"`
	OptionValue   string `json:"option_value"`
}

type variantResponse struct {
	ID        string                  `json:"id"`
	ProductID string                  `json:"product_id"`
	SKU       string                  `json:"sku"`
	Options   []variantOptionResponse `json:"options"`
	// Pointer, not a plain int64: a genuinely out-of-stock variant (0) must
	// stay distinguishable from "not populated" (vendor-facing variant
	// endpoints never set this) — omitempty on a value type would drop a
	// real zero from the JSON.
	AvailableQuantity *int64    `json:"available_quantity,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

func toVariantOptionResponseListFromDetails(details []domain.VariantOptionDetail) []variantOptionResponse {
	out := make([]variantOptionResponse, 0, len(details))
	for _, d := range details {
		out = append(out, variantOptionResponse{
			AttributeID: d.AttributeID, AttributeName: d.AttributeName,
			OptionID: d.OptionID, OptionValue: d.OptionValue,
		})
	}
	return out
}

func toVariantResponse(v *domain.ProductVariant, details []domain.VariantOptionDetail) variantResponse {
	return variantResponse{ID: v.ID, ProductID: v.ProductID, SKU: v.SKU, Options: toVariantOptionResponseListFromDetails(details), CreatedAt: v.CreatedAt}
}

// toPublicVariantResponse is the public product-detail page's builder —
// the only one that populates AvailableQuantity (vendor-facing variant
// endpoints have their own separate inventory view via the vendor console).
func toPublicVariantResponse(view domain.VariantView) variantResponse {
	resp := toVariantResponse(&view.Variant, view.Options)
	qty := view.AvailableQuantity
	resp.AvailableQuantity = &qty
	return resp
}

func toPublicVariantResponseList(views []domain.VariantView) []variantResponse {
	out := make([]variantResponse, 0, len(views))
	for _, v := range views {
		out = append(out, toPublicVariantResponse(v))
	}
	return out
}

func toVariantResponseList(variants []*domain.ProductVariant, detailsByVariant map[string][]domain.VariantOptionDetail) []variantResponse {
	out := make([]variantResponse, 0, len(variants))
	for _, v := range variants {
		out = append(out, toVariantResponse(v, detailsByVariant[v.ID]))
	}
	return out
}

func toAttributeOptionResponseListFromValues(options []domain.AttributeOption) []attributeOptionResponse {
	out := make([]attributeOptionResponse, 0, len(options))
	for _, o := range options {
		out = append(out, attributeOptionResponse{ID: o.ID, Value: o.Value, Position: o.Position})
	}
	return out
}

func toStorefrontListResponse(
	products []*domain.Product,
	images map[string]*domain.ProductImage,
	vendorNames map[string]string,
	quantitySold map[string]int64,
	vendorInfoDegraded bool,
	salesInfoDegraded bool,
) storefrontListResponse {
	out := make([]productResponse, 0, len(products))
	for _, p := range products {
		resp := toProductResponse(p)
		if img, ok := images[p.ID]; ok {
			resp.Images = []productImageResponse{toProductImageResponse(img)}
		}
		resp.VendorName = vendorNames[p.VendorID]
		resp.QuantitySold = quantitySold[p.ID]
		out = append(out, resp)
	}
	return storefrontListResponse{
		Products:           out,
		VendorInfoDegraded: vendorInfoDegraded,
		SalesInfoDegraded:  salesInfoDegraded,
	}
}
