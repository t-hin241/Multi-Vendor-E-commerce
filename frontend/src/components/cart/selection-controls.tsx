"use client";

import type { Cart } from "@/lib/api-client";
import { Checkbox } from "@/components/ui/checkbox";

export function SelectionControls({
  cart,
  ids,
  toggle,
  disabled,
  showLines = false,
}: {
  cart: Cart;
  ids: string[];
  toggle: (ids: string[], checked: boolean) => void;
  disabled?: boolean;
  showLines?: boolean;
}) {
  const groups = new Map<string, typeof cart.items>();
  for (const line of cart.items) {
    const key = line.vendor_id ?? "unknown";
    groups.set(key, [...(groups.get(key) ?? []), line]);
  }
  const control = (lineIDs: string[], label: string) => {
    const count = lineIDs.filter((id) => ids.includes(id)).length;
    return (
      <label className="flex items-center gap-2 text-sm">
        <Checkbox
          disabled={disabled}
          checked={count === lineIDs.length ? true : count ? "indeterminate" : false}
          onCheckedChange={(checked) => toggle(lineIDs, checked === true)}
        />
        {label}
      </label>
    );
  };
  return (
    <div className="space-y-3 rounded-lg border p-4" aria-label="Chọn sản phẩm đặt hàng">
      {control(
        cart.items.map((line) => line.line_id),
        "Chọn tất cả",
      )}
      <p className="text-sm text-muted-foreground">
        Đã chọn {ids.length} sản phẩm · Còn {cart.items.length - ids.length} sản phẩm để mua sau.
      </p>
      {[...groups.entries()].map(([vendor, lines], index) => (
        <div key={vendor} className="space-y-2">
          {vendor !== "unknown" &&
            control(
              lines.map((line) => line.line_id),
              `Chọn shop ${index + 1}`,
            )}
          {showLines &&
            lines.map((line) => (
              <div key={line.line_id} className="pl-4">
                {control(
                  [line.line_id],
                  `${line.product_name ?? "Sản phẩm"}${line.variant_label ? ` · ${line.variant_label}` : ""} × ${line.quantity}`,
                )}
              </div>
            ))}
        </div>
      ))}
    </div>
  );
}
