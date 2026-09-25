"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function FieldGroup({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-3">
      <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{title}</p>
      {children}
    </div>
  );
}

// The category's own detail fields (e.g. "Màu sắc", "Chất liệu") -- the set
// of fields, their data types and whether they're required all come from
// the category's attribute template, resolved server-side from its
// inheritance chain.
function AttributeField({
  field,
  value,
  onChange,
  onToggleMulti,
}: {
  field: api.AttributeTemplateField;
  value: string | string[] | undefined;
  onChange: (value: string) => void;
  onToggleMulti: (optionId: string, checked: boolean) => void;
}) {
  return (
    <Label className="flex flex-col items-start gap-1.5 text-sm">
      <span>
        {field.name}
        {field.required ? " *" : ""}
        {field.unit ? ` (${field.unit})` : ""}
      </span>
      {field.data_type === "text" && (
        <Input value={(value as string) ?? ""} onChange={(e) => onChange(e.target.value)} required={field.required} />
      )}
      {field.data_type === "number" && (
        <Input
          type="number"
          value={(value as string) ?? ""}
          onChange={(e) => onChange(e.target.value)}
          required={field.required}
          className="w-32"
        />
      )}
      {field.data_type === "boolean" && (
        <Checkbox checked={value === "true"} onCheckedChange={(c) => onChange(c === true ? "true" : "")} />
      )}
      {field.data_type === "select" && (
        <Select value={(value as string) ?? ""} onValueChange={onChange}>
          <SelectTrigger className="w-full max-w-xs">
            <SelectValue placeholder="Chọn…" />
          </SelectTrigger>
          <SelectContent>
            {field.options.map((o) => (
              <SelectItem key={o.id} value={o.id}>
                {o.value}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
      {field.data_type === "multi_select" && (
        <div className="flex flex-col gap-1">
          {field.options.map((o) => {
            const selected = Array.isArray(value) ? value.includes(o.id) : false;
            return (
              <Label key={o.id} className="text-sm font-normal">
                <Checkbox
                  checked={selected}
                  onCheckedChange={(checked) => onToggleMulti(o.id, checked === true)}
                />
                {o.value}
              </Label>
            );
          })}
        </div>
      )}
    </Label>
  );
}

// ProductCreateForm groups the create-product fields into the sections the
// spec asks for (category / basic info / attributes) inside one Card -- the
// media/stock/review-submission groups happen after creation, in
// ProductChecklistPanel, since those fields don't exist until the product
// itself does.
export function ProductCreateForm({
  vendorId,
  categories,
  onCreated,
}: {
  vendorId: string;
  categories: api.Category[];
  onCreated: (product: api.Product) => void;
}) {
  const { callWithAuth } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [mainId, setMainId] = useState("");
  const [midId, setMidId] = useState("");
  const [subId, setSubId] = useState("");
  const [attrValues, setAttrValues] = useState<Record<string, string | string[]>>({});

  const mains = categories.filter((c) => c.level === 1);
  const midOptions = mainId ? categories.filter((c) => c.level === 2 && c.parent_id === mainId) : [];
  const subOptions = midId ? categories.filter((c) => c.level === 3 && c.parent_id === midId) : [];
  const categoryId = subId || midId || mainId;

  const templateQuery = useQuery({
    queryKey: ["attribute-template", categoryId],
    queryFn: () => api.getAttributeTemplate(categoryId),
    enabled: !!categoryId,
  });
  const templateFields = templateQuery.data?.attributes ?? [];

  function setAttrValue(attributeId: string, value: string | string[]) {
    setAttrValues((prev) => ({ ...prev, [attributeId]: value }));
  }

  function toggleMultiSelectOption(attributeId: string, optionId: string, checked: boolean) {
    setAttrValues((prev) => {
      const current = Array.isArray(prev[attributeId]) ? (prev[attributeId] as string[]) : [];
      const next = checked ? [...current, optionId] : current.filter((id) => id !== optionId);
      return { ...prev, [attributeId]: next };
    });
  }

  async function handleCreate(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    // The category selects are shadcn Selects (not native <select>), so
    // there's no HTML5 `required` to lean on the way the original raw form
    // markup did -- check explicitly instead of letting this reach the API.
    if (!categoryId) {
      setError("Vui lòng chọn danh mục.");
      return;
    }
    // Captured before the `await` below -- React nulls out
    // SyntheticEvent.currentTarget once the handler's synchronous portion
    // finishes, so reading e.currentTarget again afterward (to call
    // .reset()) throws. This bit the original inline version of this form
    // too, just never surfaced because nothing exercised the happy path
    // with dev tools open; caught live while testing this pass.
    const formEl = e.currentTarget;
    const form = new FormData(formEl);
    const name = String(form.get("name") ?? "");
    const description = String(form.get("description") ?? "");
    const priceAmount = Number(form.get("price_amount") ?? 0);

    const attributes = templateFields
      .map((field) => {
        const val = attrValues[field.attribute_id];
        if (val === undefined || val === "" || (Array.isArray(val) && val.length === 0)) return null;
        if (field.data_type === "select") return { attributeId: field.attribute_id, optionIds: [val as string] };
        if (field.data_type === "multi_select")
          return { attributeId: field.attribute_id, optionIds: val as string[] };
        return { attributeId: field.attribute_id, value: val as string };
      })
      .filter((v): v is NonNullable<typeof v> => v !== null);

    setIsCreating(true);
    try {
      const created = await callWithAuth((token) =>
        api.createProduct(token, { vendorId, categoryId, name, description, priceAmount, attributes }),
      );
      formEl.reset();
      setMainId("");
      setMidId("");
      setSubId("");
      setAttrValues({});
      onCreated(created);
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể tạo sản phẩm.");
    } finally {
      setIsCreating(false);
    }
  }

  return (
    <Card>
      <CardContent>
        <form onSubmit={handleCreate} className="flex flex-col gap-6">
          <FieldGroup title="Danh mục">
            <div className="flex flex-wrap gap-3">
              <Label className="flex flex-col items-start gap-1.5 text-sm">
                Danh mục
                <Select
                  value={mainId}
                  onValueChange={(v) => {
                    setMainId(v);
                    setMidId("");
                    setSubId("");
                  }}
                >
                  <SelectTrigger className="w-48">
                    <SelectValue placeholder="Chọn danh mục" />
                  </SelectTrigger>
                  <SelectContent>
                    {mains.map((c) => (
                      <SelectItem key={c.id} value={c.id}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Label>
              {midOptions.length > 0 && (
                <Label className="flex flex-col items-start gap-1.5 text-sm">
                  Danh mục con
                  <Select
                    value={midId}
                    onValueChange={(v) => {
                      setMidId(v);
                      setSubId("");
                    }}
                  >
                    <SelectTrigger className="w-48">
                      <SelectValue placeholder="Tất cả" />
                    </SelectTrigger>
                    <SelectContent>
                      {midOptions.map((c) => (
                        <SelectItem key={c.id} value={c.id}>
                          {c.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Label>
              )}
              {subOptions.length > 0 && (
                <Label className="flex flex-col items-start gap-1.5 text-sm">
                  Danh mục chi tiết
                  <Select value={subId} onValueChange={setSubId}>
                    <SelectTrigger className="w-48">
                      <SelectValue placeholder="Tất cả" />
                    </SelectTrigger>
                    <SelectContent>
                      {subOptions.map((c) => (
                        <SelectItem key={c.id} value={c.id}>
                          {c.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Label>
              )}
            </div>
            {categories.length === 0 && (
              <p className="text-sm text-warning-foreground">
                Chưa có danh mục nào — liên hệ admin để tạo danh mục trước khi thêm sản phẩm.
              </p>
            )}
          </FieldGroup>

          <Separator />

          <FieldGroup title="Thông tin cơ bản">
            <div className="flex flex-wrap gap-3">
              <Label className="flex flex-col items-start gap-1.5 text-sm">
                Tên sản phẩm
                <Input name="name" required className="w-64" />
              </Label>
              <Label className="flex flex-col items-start gap-1.5 text-sm">
                Giá (VND)
                <Input name="price_amount" type="number" min={1} required className="w-32" />
              </Label>
            </div>
            <Label className="flex flex-col items-start gap-1.5 text-sm">
              Mô tả
              <Textarea name="description" className="w-full" />
            </Label>
          </FieldGroup>

          {templateFields.length > 0 && (
            <>
              <Separator />
              <FieldGroup title="Thuộc tính">
                <div className="flex flex-wrap gap-4">
                  {templateFields.map((field) => (
                    <AttributeField
                      key={field.attribute_id}
                      field={field}
                      value={attrValues[field.attribute_id]}
                      onChange={(v) => setAttrValue(field.attribute_id, v)}
                      onToggleMulti={(optionId, checked) =>
                        toggleMultiSelectOption(field.attribute_id, optionId, checked)
                      }
                    />
                  ))}
                </div>
              </FieldGroup>
            </>
          )}

          {error && <p className="text-sm text-destructive">{error}</p>}

          <Button type="submit" disabled={isCreating} className="self-start">
            {isCreating ? "Đang tạo…" : "Thêm sản phẩm"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
