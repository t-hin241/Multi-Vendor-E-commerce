"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { AttributeField } from "@/components/vendor/product-create-form";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { catalogEditorAttributes, catalogEditorValues } from "@/lib/catalog-editor";
import { describeApiError } from "@/lib/errors";

export function ProductContentEditor({ product }: { product: api.Product }) {
  const [open, setOpen] = useState(false);
  const { callWithAuth } = useAuth();
  const editor = useQuery({
    queryKey: ["product-editor", product.id, product.version],
    queryFn: () => callWithAuth((token) => api.getProductEditor(token, product.id)),
    enabled: open,
  });
  const template = useQuery({
    queryKey: ["attribute-template", product.category_id],
    queryFn: () => api.getAttributeTemplate(product.category_id),
    enabled: open,
  });
  return (
    <div className="space-y-3">
      <Button variant="outline" onClick={() => setOpen(!open)}>
        {open ? "Đóng chỉnh sửa" : "Sửa nội dung"}
      </Button>
      {open && (editor.isPending || template.isPending) && <p role="status">Đang tải thông tin…</p>}
      {open && (editor.isError || template.isError) && (
        <p role="alert" className="text-destructive">
          Không thể tải thông tin sản phẩm.
        </p>
      )}
      {open && editor.data && template.data && (
        <EditorForm
          key={`${product.id}:${editor.data.product.version}`}
          data={editor.data}
          fields={template.data.attributes}
          onSaved={() => setOpen(false)}
        />
      )}
    </div>
  );
}

function EditorForm({
  data,
  fields,
  onSaved,
}: {
  data: api.ProductEditor;
  fields: api.AttributeTemplateField[];
  onSaved: () => void;
}) {
  const { callWithAuth } = useAuth();
  const client = useQueryClient();
  const [values, setValues] = useState(() =>
    catalogEditorValues(fields, data.attributes, data.packaging),
  );
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError(null);
    try {
      await callWithAuth((token) =>
        api.updateProductContent(token, data.product.id, {
          version: data.product.version!,
          name: String(form.get("name")),
          description: String(form.get("description")),
          price_amount: Number(form.get("price")),
          attributes: catalogEditorAttributes(fields, values),
        }),
      );
      await client.invalidateQueries({ queryKey: ["vendor-products"] });
      await client.invalidateQueries({ queryKey: ["product-editor", data.product.id] });
      onSaved();
    } catch (err) {
      setError(describeApiError(err, "Không thể lưu sản phẩm."));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={save} className="space-y-3 rounded border p-4">
      <p className="text-sm text-muted-foreground">
        Lưu thay đổi sẽ đưa sản phẩm về nháp và ẩn khỏi cửa hàng. Bạn cần gửi duyệt lại để tiếp tục
        bán.
      </p>
      <label className="block text-sm">
        Tên sản phẩm
        <Input name="name" defaultValue={data.product.name} required maxLength={300} />
      </label>
      <label className="block text-sm">
        Giá (VND)
        <Input
          name="price"
          type="number"
          min={1}
          step={1}
          defaultValue={data.product.price_amount}
          required
        />
      </label>
      <label className="block text-sm">
        Mô tả
        <Textarea name="description" defaultValue={data.product.description} maxLength={50000} />
      </label>
      <div className="flex flex-wrap gap-4">
        {fields
          .filter((field) => !field.is_variant_defining)
          .map((field) => (
            <AttributeField
              key={field.attribute_id}
              field={field}
              value={values[field.attribute_id]}
              onChange={(value) => setValues((prev) => ({ ...prev, [field.attribute_id]: value }))}
              onToggleMulti={(id, checked) =>
                setValues((prev) => {
                  const old = Array.isArray(prev[field.attribute_id])
                    ? (prev[field.attribute_id] as string[])
                    : [];
                  return {
                    ...prev,
                    [field.attribute_id]: checked
                      ? [...old, id]
                      : old.filter((value) => value !== id),
                  };
                })
              }
            />
          ))}
      </div>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <Button disabled={busy || !data.product.version}>
        {busy ? "Đang lưu…" : "Lưu bản nháp"}
      </Button>
    </form>
  );
}
