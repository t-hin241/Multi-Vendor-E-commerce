"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";

export function VariantPriceEditor({ variant }: { variant: api.ProductVariant }) {
  const { callWithAuth } = useAuth();
  const cache = useQueryClient();
  const capability = useQuery({
    queryKey: ["catalog-capabilities"],
    queryFn: api.catalogCapabilities,
  });
  const [amount, setAmount] = useState(String(variant.price_amount ?? ""));
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  async function save() {
    const price = Number(amount);
    if (!Number.isSafeInteger(price) || price <= 0) {
      setMessage("Giá phải là số nguyên dương.");
      return;
    }
    setSaving(true);
    setMessage("");
    try {
      await callWithAuth((token) =>
        api.updateVariantPrice(
          token,
          variant.product_id,
          variant.id,
          price,
          variant.pricing_version,
        ),
      );
      setMessage("Đã lưu giá. Sản phẩm cần gửi duyệt lại trước khi bán.");
      cache.invalidateQueries({ queryKey: ["product-variants", variant.product_id] });
      cache.invalidateQueries({ queryKey: ["vendor-products"] });
    } catch (err) {
      setMessage(err instanceof api.ApiError ? err.message : "Không thể lưu giá.");
      if (err instanceof api.ApiError && err.status === 409)
        cache.invalidateQueries({ queryKey: ["product-variants", variant.product_id] });
    } finally {
      setSaving(false);
    }
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span>Giá: {formatMoney(variant.price_amount, "VND")}</span>
      {capability.data?.variant_pricing_enabled && (
        <>
          <Input
            aria-label={`Giá SKU ${variant.sku}`}
            type="number"
            min={1}
            step={1}
            className="w-32"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
          />
          <Button
            size="sm"
            variant="outline"
            disabled={saving || Number(amount) === variant.price_amount}
            onClick={save}
          >
            {saving ? "Đang lưu…" : "Lưu giá"}
          </Button>
        </>
      )}
      {message && (
        <p role="status" className="w-full text-xs">
          {message}
        </p>
      )}
    </div>
  );
}
