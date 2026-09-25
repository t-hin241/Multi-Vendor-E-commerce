"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RestockStatusBadge } from "@/components/vendor/status-badges";
import { VariantGenerator } from "@/components/vendor/variant-generator";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// ProductStockManager is the vendor's stock-input control for one product.
// A category with no variant-defining attributes gets the plain
// product-level Set-stock/+Add-stock control; a category with them shows
// the variant matrix (once generated) or the generator itself.
export function ProductStockManager({
  vendorId,
  productId,
  categoryId,
}: {
  vendorId: string;
  productId: string;
  categoryId: string;
}) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();

  const templateQuery = useQuery({
    queryKey: ["attribute-template", categoryId],
    queryFn: () => api.getAttributeTemplate(categoryId),
    enabled: !!categoryId,
  });
  const variantAxes = (templateQuery.data?.attributes ?? []).filter((f) => f.is_variant_defining);

  const variantsQuery = useQuery({
    queryKey: ["product-variants", productId],
    queryFn: () => callWithAuth((token) => api.listProductVariants(token, productId)),
    enabled: variantAxes.length > 0,
  });

  // Shared across every product row on this page -- React Query dedupes
  // identical keys, so this is one fetch, not one per row.
  const inventoryQuery = useQuery({
    queryKey: ["vendor-inventory", vendorId],
    queryFn: () => callWithAuth((token) => api.listMyInventory(token, vendorId)),
  });

  // Also shared across every row: a vendor's stock-increase requests no
  // longer change available_quantity by themselves, so this is what makes
  // a pending/rejected request visible at all.
  const restockRequestsQuery = useQuery({
    queryKey: ["vendor-restock-requests", vendorId],
    queryFn: () => callWithAuth((token) => api.listMyRestockRequests(token, vendorId)),
  });

  function invalidateInventory() {
    queryClient.invalidateQueries({ queryKey: ["vendor-inventory"] });
    queryClient.invalidateQueries({ queryKey: ["vendor-restock-requests"] });
  }

  if (variantAxes.length === 0) {
    const item = inventoryQuery.data?.find((i) => i.product_id === productId && !i.variant_id);
    const pendingRequests = (restockRequestsQuery.data ?? []).filter(
      (r) => r.product_id === productId && !r.variant_id,
    );
    return (
      <PlainStockEditor
        productId={productId}
        item={item}
        pendingRequests={pendingRequests}
        onChanged={invalidateInventory}
      />
    );
  }

  if (!variantsQuery.data || variantsQuery.data.length === 0) {
    return (
      <VariantGenerator
        productId={productId}
        axes={variantAxes}
        onCreated={() => {
          queryClient.invalidateQueries({ queryKey: ["product-variants", productId] });
          invalidateInventory();
        }}
      />
    );
  }

  const inventoryByVariant = new Map(
    (inventoryQuery.data ?? []).filter((i) => i.variant_id).map((i) => [i.variant_id as string, i]),
  );

  return (
    <div className="mt-1">
      <p className="text-sm font-medium">Phiên bản</p>
      <ul className="mt-1 space-y-1.5">
        {variantsQuery.data.map((v) => (
          <VariantStockRow
            key={v.id}
            variant={v}
            item={inventoryByVariant.get(v.id)}
            pendingRequests={(restockRequestsQuery.data ?? []).filter((r) => r.variant_id === v.id)}
            onChanged={invalidateInventory}
          />
        ))}
      </ul>
    </div>
  );
}

// Exposes whether this product still needs its stock set up at all, so the
// post-create checklist panel can mark that item done/pending without
// duplicating the query logic above.
export function useHasStock(vendorId: string, productId: string, categoryId: string) {
  const { callWithAuth } = useAuth();
  const templateQuery = useQuery({
    queryKey: ["attribute-template", categoryId],
    queryFn: () => api.getAttributeTemplate(categoryId),
    enabled: !!categoryId,
  });
  const variantAxes = (templateQuery.data?.attributes ?? []).filter((f) => f.is_variant_defining);
  const inventoryQuery = useQuery({
    queryKey: ["vendor-inventory", vendorId],
    queryFn: () => callWithAuth((token) => api.listMyInventory(token, vendorId)),
  });
  const variantsQuery = useQuery({
    queryKey: ["product-variants", productId],
    queryFn: () => callWithAuth((token) => api.listProductVariants(token, productId)),
    enabled: variantAxes.length > 0,
  });

  if (variantAxes.length === 0) {
    return (inventoryQuery.data ?? []).some((i) => i.product_id === productId && !i.variant_id);
  }
  return (variantsQuery.data ?? []).length > 0;
}

// PlainStockEditor handles product-level stock (no variants): a first
// "Set stock" input while no inventory item exists yet, then a running
// total plus a "+ Add stock" input once it does.
export function PlainStockEditor({
  productId,
  item,
  pendingRequests,
  onChanged,
}: {
  productId: string;
  item?: api.InventoryItem;
  pendingRequests: api.RestockRequest[];
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [quantity, setQuantity] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  async function handleSubmit() {
    setError(null);
    setMessage(null);
    setIsSaving(true);
    try {
      if (item) {
        await callWithAuth((token) => api.requestRestock(token, productId, Number(quantity)));
        setMessage("Đã gửi yêu cầu bổ sung kho — đang chờ admin duyệt.");
      } else {
        await callWithAuth((token) =>
          api.createInventoryItemForProduct(token, productId, Number(quantity)),
        );
      }
      setQuantity("");
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể cập nhật tồn kho.");
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <div className="flex flex-col items-start gap-1">
      <div className="flex items-center gap-2">
        {item && <span className="text-sm">Tồn kho: {item.available_quantity}</span>}
        <Input
          type="number"
          min={item ? 1 : 0}
          value={quantity}
          onChange={(e) => setQuantity(e.target.value)}
          placeholder={item ? "Số lượng" : "Nhập tồn kho"}
          className="w-28"
        />
        <Button size="sm" onClick={handleSubmit} disabled={quantity === "" || isSaving}>
          {isSaving ? "Đang lưu…" : item ? "Yêu cầu bổ sung" : "Thiết lập tồn kho"}
        </Button>
      </div>
      {message && <p className="text-xs text-success">{message}</p>}
      {error && <p className="text-xs text-destructive">{error}</p>}
      <RestockRequestStatusList requests={pendingRequests} />
    </div>
  );
}

// RestockRequestStatusList surfaces the outcome of a stock-increase
// request: it never changes available_quantity itself, so this is the only
// visible feedback a vendor gets until an admin decides it.
export function RestockRequestStatusList({ requests }: { requests: api.RestockRequest[] }) {
  const visible = requests.filter((r) => r.status !== "approved");
  if (visible.length === 0) return null;

  return (
    <ul className="flex flex-col gap-1">
      {visible.map((r) => (
        <li key={r.id} className="flex items-center gap-2 text-xs">
          <RestockStatusBadge status={r.status} />
          <span className="text-muted-foreground">
            Yêu cầu thêm {r.requested_quantity}
            {r.status === "rejected" && r.rejection_reason ? ` — ${r.rejection_reason}` : ""}
          </span>
        </li>
      ))}
    </ul>
  );
}

// VariantStockRow shows one already-created variant. It can still lack an
// inventory row (e.g. the create-variants sequence was interrupted before
// reaching it), so it falls back to a "Set stock" control in that case,
// mirroring PlainStockEditor.
export function VariantStockRow({
  variant,
  item,
  pendingRequests,
  onChanged,
}: {
  variant: api.ProductVariant;
  item?: api.InventoryItem;
  pendingRequests: api.RestockRequest[];
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [quantity, setQuantity] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  const label = variant.options.map((o) => `${o.attribute_name}: ${o.option_value}`).join(", ");

  async function handleSubmit() {
    setError(null);
    setMessage(null);
    setIsSaving(true);
    try {
      if (item) {
        await callWithAuth((token) =>
          api.requestRestockVariant(token, variant.id, Number(quantity)),
        );
        setMessage("Đã gửi yêu cầu bổ sung kho — đang chờ admin duyệt.");
      } else {
        await callWithAuth((token) =>
          api.createInventoryItemForVariant(token, variant.id, Number(quantity)),
        );
      }
      setQuantity("");
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể cập nhật tồn kho.");
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <li className="flex flex-col gap-1 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span>
          {variant.sku} — {label} — Tồn kho: {item?.available_quantity ?? 0}
        </span>
        <Input
          type="number"
          min={item ? 1 : 0}
          value={quantity}
          onChange={(e) => setQuantity(e.target.value)}
          className="w-20"
        />
        <Button size="sm" onClick={handleSubmit} disabled={quantity === "" || isSaving}>
          {isSaving ? "Đang lưu…" : item ? "Yêu cầu bổ sung" : "Thiết lập"}
        </Button>
        {message && <span className="text-xs text-success">{message}</span>}
        {error && <span className="text-xs text-destructive">{error}</span>}
      </div>
      <RestockRequestStatusList requests={pendingRequests} />
    </li>
  );
}
