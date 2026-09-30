"use client";

import { AlertTriangle, RefreshCw } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { Cart, CartLine } from "@/lib/api-client";
import {
  checkoutBlockers,
  lineNotice,
  priceChangeText,
  priceConfirmations,
} from "@/lib/cart-status";
import { formatMoney } from "@/lib/format";

// A line's price as Cart reports it right now; "—" when Catalog could not be
// reached (never a stale guess).
export function linePrice(line: CartLine): string {
  return line.price_amount === null ? "—" : formatMoney(line.price_amount, line.currency);
}

export function CartLineStatus({ line }: { line: CartLine }) {
  const notice = lineNotice(line);
  const priceChange = priceChangeText(line);
  if (!notice && !priceChange && line.stock_status !== "unknown") return null;
  return (
    <div className="mt-1 flex flex-col gap-1">
      {notice && (
        <div className="flex flex-col gap-0.5">
          <Badge variant={notice.tone}>{notice.label}</Badge>
          <p className="text-xs text-muted-foreground">{notice.hint}</p>
        </div>
      )}
      {priceChange && (
        <Badge variant="warning" className="w-fit">
          {priceChange}
        </Badge>
      )}
      {!notice && line.stock_status === "unknown" && (
        <p className="text-xs text-muted-foreground">
          Chưa xác định được tồn kho; số lượng sẽ được kiểm tra khi đặt hàng.
        </p>
      )}
    </div>
  );
}

// Tells the buyer why they cannot place the order yet and what to do. Never
// fixes anything on its own: prices are only accepted and lines only removed
// by an explicit buyer action.
export function CartReadiness({
  cart,
  onRefresh,
  refreshing,
  onConfirmPrices,
  confirming,
}: {
  cart: Cart;
  onRefresh: () => void;
  refreshing: boolean;
  onConfirmPrices: (vars: {
    version: number;
    lines: ReturnType<typeof priceConfirmations>;
  }) => void;
  confirming: boolean;
}) {
  const blockers = checkoutBlockers(cart);
  const confirmations = priceConfirmations(cart);
  if (blockers.length === 0 && !cart.degraded.inventory) return null;

  return (
    <Alert variant={blockers.length > 0 ? "warning" : "default"}>
      <AlertTriangle />
      <AlertTitle>
        {blockers.length > 0 ? "Chưa thể đặt hàng" : "Tồn kho đang được cập nhật"}
      </AlertTitle>
      <AlertDescription>
        <ul className="list-disc pl-4">
          {blockers.map((reason) => (
            <li key={reason}>{reason}</li>
          ))}
          {blockers.length === 0 && cart.degraded.inventory && (
            <li>Không lấy được tồn kho hiện tại; số lượng sẽ được giữ chỗ khi đặt hàng.</li>
          )}
        </ul>
        <div className="mt-2 flex flex-wrap gap-2">
          {confirmations.length > 0 && (
            <Button
              size="sm"
              disabled={confirming}
              onClick={() => onConfirmPrices({ version: cart.version, lines: confirmations })}
            >
              {confirming ? "Đang xác nhận…" : "Xác nhận giá mới"}
            </Button>
          )}
          <Button size="sm" variant="outline" disabled={refreshing} onClick={onRefresh}>
            <RefreshCw className="size-4" />
            {refreshing ? "Đang tải lại…" : "Tải lại giỏ hàng"}
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  );
}

// No shipping quote API exists before the order is created, so the fee is
// never shown as 0: the buyer is told it is quoted when the order is placed.
export function ShippingFeeNote() {
  return (
    <div className="flex justify-between gap-4 text-sm text-muted-foreground">
      <span>Phí vận chuyển</span>
      <span className="text-right">Được báo giá khi tạo đơn, trước khi thanh toán</span>
    </div>
  );
}

// The estimate shown before checkout. The final amount is computed by Order.
export function CartSubtotal({ cart }: { cart: Cart }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex justify-between text-lg font-semibold">
        <span>Tạm tính</span>
        <span>
          {cart.subtotal ? formatMoney(cart.subtotal.amount, cart.subtotal.currency) : "—"}
        </span>
      </div>
      <p className="text-xs text-muted-foreground">
        {cart.subtotal
          ? `Theo giá hiện tại của ${cart.line_count - cart.unavailable_lines} sản phẩm có thể mua. Tổng cuối cùng được chốt khi tạo đơn.`
          : "Chưa tính được tạm tính cho giỏ hàng này."}
      </p>
    </div>
  );
}
