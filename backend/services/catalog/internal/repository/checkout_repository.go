package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"shopee/backend/services/catalog/internal/domain"
)

// ReadCheckout reads products, packaging, variants and labels in ONE SQL
// statement/MVCC snapshot. A concurrent product edit cannot mix versions
// between cart lines or between product and variant facts. Existing primary
// keys and product_variants(product_id) indexes cover the bounded lookups.
func (r *ProductRepository) ReadCheckout(ctx context.Context, productIDs, variantIDs []string) (*domain.CheckoutSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var products, variants []byte
	err := connection(ctx, r.pool).QueryRow(ctx, `SELECT
 COALESCE((SELECT jsonb_agg(jsonb_build_object(
  'id', p.id, 'vendor_id', p.vendor_id, 'name', p.name,
  'price_amount', p.price_amount, 'currency', p.currency, 'status', p.status,
  'is_active', p.is_active, 'version', p.version, 'enforced_version', p.enforced_version,
  'has_variants', EXISTS(SELECT 1 FROM product_variants v WHERE v.product_id=p.id),
  'package_weight_grams', packaging.weight_grams) ORDER BY p.id)
 FROM products p LEFT JOIN product_packaging packaging ON packaging.product_id=p.id
 WHERE p.id=ANY($1::uuid[])), '[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object(
  'id', v.id, 'product_id', v.product_id, 'vendor_id', p.vendor_id, 'sku', v.sku,
  'options', COALESCE((SELECT jsonb_agg(jsonb_build_object(
   'attribute_id', sel.attribute_id, 'attribute_name', COALESCE(a.name, sel.attribute_id::text),
   'option_id', sel.option_id, 'option_value', COALESCE(o.value, sel.option_id::text)) ORDER BY sel.attribute_id)
   FROM product_variant_options sel
   LEFT JOIN attributes a ON a.id=sel.attribute_id
   LEFT JOIN attribute_options o ON o.id=sel.option_id
   WHERE sel.variant_id=v.id), '[]'::jsonb)) ORDER BY v.id)
 FROM product_variants v JOIN products p ON p.id=v.product_id
 WHERE v.id=ANY($2::uuid[])), '[]'::jsonb)`, productIDs, variantIDs).Scan(&products, &variants)
	if err != nil {
		return nil, fmt.Errorf("read catalog checkout snapshot: %w", err)
	}
	out := &domain.CheckoutSnapshot{}
	if err := json.Unmarshal(products, &out.Products); err != nil {
		return nil, fmt.Errorf("decode checkout products: %w", err)
	}
	if err := json.Unmarshal(variants, &out.Variants); err != nil {
		return nil, fmt.Errorf("decode checkout variants: %w", err)
	}
	return out, nil
}
