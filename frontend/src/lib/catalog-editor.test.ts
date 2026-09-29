import { describe, expect, it } from "vitest";
import { catalogEditorAttributes, catalogEditorValues } from "./catalog-editor";
import type { AttributeTemplateField } from "./api-client";

describe("catalog content editor", () => {
  const fields = [
    { attribute_id: "bool", code: "warranty", data_type: "boolean" },
    { attribute_id: "weight", code: "pkg_weight", data_type: "number" },
    { attribute_id: "tags", code: "tags", data_type: "multi_select" },
    { attribute_id: "size", code: "size", data_type: "select", is_variant_defining: true },
  ] as AttributeTemplateField[];

  it("preserves false, packaging and multiple options when editing existing data", () => {
    const values = catalogEditorValues(
      fields,
      [
        { attribute_id: "bool", value_boolean: false },
        { attribute_id: "tags", option_id: "one" },
        { attribute_id: "tags", option_id: "two" },
      ],
      { pkg_weight: 500 },
    );
    expect(catalogEditorAttributes(fields, { ...values, size: "large" })).toEqual([
      { attribute_id: "bool", value: "false" },
      { attribute_id: "weight", value: "500" },
      { attribute_id: "tags", option_ids: ["one", "two"] },
    ]);
  });
});
