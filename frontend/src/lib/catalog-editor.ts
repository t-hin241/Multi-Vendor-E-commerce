import type { AttributeTemplateField, AttributeValue } from "@/lib/api-client";

export function catalogEditorValues(
  fields: AttributeTemplateField[],
  values: AttributeValue[],
  packaging: Record<string, number | null>,
): Record<string, string | string[]> {
  const result: Record<string, string | string[]> = {};
  for (const field of fields) {
    const rows = values.filter((value) => value.attribute_id === field.attribute_id);
    if (packaging[field.code] != null) result[field.attribute_id] = String(packaging[field.code]);
    else if (field.data_type === "multi_select")
      result[field.attribute_id] = rows.flatMap((v) => (v.option_id ? [v.option_id] : []));
    else if (rows[0]) {
      const value =
        rows[0].option_id ?? rows[0].value_text ?? rows[0].value_number ?? rows[0].value_boolean;
      if (value != null) result[field.attribute_id] = String(value);
    }
  }
  return result;
}

export function catalogEditorAttributes(
  fields: AttributeTemplateField[],
  values: Record<string, string | string[]>,
) {
  return fields
    .filter((field) => !field.is_variant_defining)
    .flatMap((field) => {
      const value = values[field.attribute_id];
      if (value === undefined || value === "" || (Array.isArray(value) && value.length === 0))
        return [];
      return [
        {
          attribute_id: field.attribute_id,
          ...(field.data_type === "select" || field.data_type === "multi_select"
            ? { option_ids: Array.isArray(value) ? value : [value] }
            : { value: String(value) }),
        },
      ];
    });
}
