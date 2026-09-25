"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { AttributeTemplateField } from "@/lib/api-client";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function slugify(text: string): string {
  const withoutDiacritics = Array.from(text.normalize("NFD"))
    .filter((ch) => {
      const code = ch.codePointAt(0) ?? 0;
      return code < 0x300 || code > 0x36f;
    })
    .join("");
  return withoutDiacritics
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

// VariantGenerator lets the vendor pick which option values apply to this
// product for each variant-defining attribute (e.g. Size: S, M; Color: Red)
// and builds the Cartesian product into an editable SKU + initial-stock
// table, then creates each variant (and its stock) sequentially -- same
// rationale as ProductMediaUploader's sequential uploads: predictable
// order, no racing the backend's sku/combination uniqueness checks.
export function VariantGenerator({
  productId,
  axes,
  onCreated,
}: {
  productId: string;
  axes: AttributeTemplateField[];
  onCreated: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [selectedOptions, setSelectedOptions] = useState<Record<string, string[]>>({});
  const [rows, setRows] = useState<
    { optionIds: string[]; label: string; sku: string; quantity: string }[]
  >([]);
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  function toggleOption(attributeId: string, optionId: string, checked: boolean) {
    setSelectedOptions((prev) => {
      const current = prev[attributeId] ?? [];
      const next = checked ? [...current, optionId] : current.filter((id) => id !== optionId);
      return { ...prev, [attributeId]: next };
    });
  }

  function handleGenerate() {
    setError(null);
    const axisChoices = axes.map((axis) =>
      (selectedOptions[axis.attribute_id] ?? []).map((optionId) => ({
        optionId,
        value: axis.options.find((o) => o.id === optionId)?.value ?? optionId,
      })),
    );
    if (axisChoices.some((choices) => choices.length === 0)) {
      setError("Chọn ít nhất một tùy chọn cho mỗi thuộc tính phân loại.");
      return;
    }

    let combinations: { optionId: string; value: string }[][] = [[]];
    for (const choices of axisChoices) {
      const next: { optionId: string; value: string }[][] = [];
      for (const combo of combinations) {
        for (const choice of choices) {
          next.push([...combo, choice]);
        }
      }
      combinations = next;
    }

    setRows(
      combinations.map((combo) => ({
        optionIds: combo.map((c) => c.optionId),
        label: combo.map((c) => c.value).join(" / "),
        sku: slugify(combo.map((c) => c.value).join("-")),
        quantity: "",
      })),
    );
  }

  function updateRow(index: number, field: "sku" | "quantity", value: string) {
    setRows((prev) => prev.map((row, i) => (i === index ? { ...row, [field]: value } : row)));
  }

  async function handleCreateVariants() {
    setError(null);
    setIsSaving(true);
    try {
      for (const row of rows) {
        await callWithAuth(async (token) => {
          const variant = await api.createProductVariant(token, productId, row.sku, row.optionIds);
          await api.createInventoryItemForVariant(token, variant.id, Number(row.quantity || 0));
        });
      }
      setRows([]);
      setSelectedOptions({});
      onCreated();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể tạo các phiên bản.");
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <Card className="mt-1">
      <CardContent className="flex flex-col gap-3">
        <p className="text-sm font-medium">Thiết lập phiên bản</p>
        <div className="flex flex-wrap gap-4">
          {axes.map((axis) => (
            <div key={axis.attribute_id}>
              <p className="text-xs font-medium text-muted-foreground">{axis.name}</p>
              <div className="mt-1 flex flex-wrap gap-3">
                {axis.options.map((o) => (
                  <Label key={o.id} className="text-xs font-normal">
                    <Checkbox
                      checked={(selectedOptions[axis.attribute_id] ?? []).includes(o.id)}
                      onCheckedChange={(checked) =>
                        toggleOption(axis.attribute_id, o.id, checked === true)
                      }
                    />
                    {o.value}
                  </Label>
                ))}
              </div>
            </div>
          ))}
        </div>
        <Button type="button" variant="outline" size="sm" className="self-start" onClick={handleGenerate}>
          Tạo danh sách phiên bản
        </Button>

        {rows.length > 0 && (
          <div className="flex flex-col gap-3">
            {/* Desktop: a real table -- easy to scan. Mobile: stacked
                labeled inputs instead, so SKU/quantity fields never get
                squeezed into unusably narrow table cells. */}
            <Table className="hidden sm:table">
              <TableHeader>
                <TableRow>
                  <TableHead>Phiên bản</TableHead>
                  <TableHead>SKU</TableHead>
                  <TableHead>Tồn kho ban đầu</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row, i) => (
                  <TableRow key={row.optionIds.join(",")}>
                    <TableCell className="whitespace-normal">{row.label}</TableCell>
                    <TableCell>
                      <Input
                        value={row.sku}
                        onChange={(e) => updateRow(i, "sku", e.target.value)}
                        className="w-36"
                      />
                    </TableCell>
                    <TableCell>
                      <Input
                        type="number"
                        min={0}
                        value={row.quantity}
                        onChange={(e) => updateRow(i, "quantity", e.target.value)}
                        className="w-24"
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>

            <div className="flex flex-col gap-2 sm:hidden">
              {rows.map((row, i) => (
                <div key={row.optionIds.join(",")} className="rounded-lg border p-3 text-sm">
                  <p className="font-medium">{row.label}</p>
                  <div className="mt-2 flex flex-col gap-2">
                    <Label className="flex flex-col items-start gap-1 text-xs">
                      SKU
                      <Input
                        value={row.sku}
                        onChange={(e) => updateRow(i, "sku", e.target.value)}
                      />
                    </Label>
                    <Label className="flex flex-col items-start gap-1 text-xs">
                      Tồn kho ban đầu
                      <Input
                        type="number"
                        min={0}
                        value={row.quantity}
                        onChange={(e) => updateRow(i, "quantity", e.target.value)}
                      />
                    </Label>
                  </div>
                </div>
              ))}
            </div>

            <Button type="button" size="sm" className="self-start" onClick={handleCreateVariants} disabled={isSaving}>
              {isSaving ? "Đang tạo…" : "Tạo phiên bản"}
            </Button>
          </div>
        )}
        {error && <p className="text-xs text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
