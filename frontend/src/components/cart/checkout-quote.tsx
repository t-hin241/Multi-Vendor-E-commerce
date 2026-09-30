"use client";

import { Separator } from "@/components/ui/separator";
import type { CheckoutPreview } from "@/lib/api-client";
import { formatMoney } from "@/lib/format";

// CheckoutQuote shows Order's quote: each shop's items and shipping fee,
// then the total the buyer confirms. A shop Shipment cannot serve is shown
// as such, never as free shipping.
export function CheckoutQuote({ preview }: { preview: CheckoutPreview }) {
  const money = (amount: number) => formatMoney(amount, preview.currency);
  return (
    <div className="flex flex-col gap-2 text-sm">
      {preview.vendors.map((vendor, index) => (
        <div key={vendor.vendor_id} className="flex justify-between gap-4">
          <span className="text-muted-foreground">
            Shop {index + 1} · {vendor.item_count} sản phẩm
          </span>
          <span className="text-right">
            {vendor.shipping_fee_amount === null ? (
              <span className="text-destructive">
                {vendor.shipping_error || "Chưa giao được đến địa chỉ này"}
              </span>
            ) : (
              <>
                {money(vendor.subtotal_amount)}
                <span className="block text-xs text-muted-foreground">
                  Phí vận chuyển {money(vendor.shipping_fee_amount)}
                </span>
              </>
            )}
          </span>
        </div>
      ))}
      <Separator />
      <div className="flex justify-between">
        <span>Tạm tính</span>
        <span>{money(preview.subtotal_amount)}</span>
      </div>
      <div className="flex justify-between">
        <span>Phí vận chuyển</span>
        <span>{preview.shipping_amount === null ? "—" : money(preview.shipping_amount)}</span>
      </div>
      <div className="flex justify-between text-lg font-semibold">
        <span>Tổng cộng</span>
        <span>{preview.total_amount === null ? "—" : money(preview.total_amount)}</span>
      </div>
      <p className="text-xs text-muted-foreground">
        Đây là số tiền bạn sẽ thanh toán. Nếu giá hoặc phí vận chuyển đổi trước khi đặt, hệ thống sẽ
        yêu cầu bạn xác nhận lại.
      </p>
    </div>
  );
}
