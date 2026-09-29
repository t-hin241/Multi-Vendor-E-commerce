-- Read-only checks against catalog_db before migration 000012 and constraint validation.
SELECT 'duplicate_trimmed_sku' AS check_name,count(*) AS violations FROM (
 SELECT btrim(sku) FROM product_variants GROUP BY btrim(sku) HAVING count(*)>1
) duplicates
UNION ALL SELECT 'invalid_sku',count(*) FROM product_variants WHERE length(btrim(sku)) NOT BETWEEN 1 AND 100
UNION ALL SELECT 'orphan_variant',count(*) FROM product_variants v LEFT JOIN products p ON p.id=v.product_id WHERE p.id IS NULL
UNION ALL SELECT 'variant_option_mismatch',count(*) FROM product_variant_options v LEFT JOIN attribute_options o ON o.id=v.option_id AND o.attribute_id=v.attribute_id WHERE o.id IS NULL
UNION ALL SELECT 'attribute_option_mismatch',count(*) FROM product_attribute_values v LEFT JOIN attribute_options o ON o.id=v.option_id AND o.attribute_id=v.attribute_id WHERE v.option_id IS NOT NULL AND o.id IS NULL
UNION ALL SELECT 'ambiguous_attribute_value',count(*) FROM product_attribute_values WHERE num_nonnulls(option_id,value_text,value_number,value_boolean)<>1
UNION ALL SELECT 'missing_rule_reference',count(*) FROM product_attribute_values WHERE rule_id IS NULL;
