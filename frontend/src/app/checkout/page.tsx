"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import {
  CartLineStatus,
  CartReadiness,
  CartSubtotal,
  linePrice,
} from "@/components/cart/cart-notices";
import { CheckoutQuote } from "@/components/cart/checkout-quote";
import { PageShell } from "@/components/page-shell";
import { CheckoutPolicyNotice } from "@/components/policies/checkout-policy-notice";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { isOutcomeUnknown } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { loadAttempt } from "@/lib/checkout-attempt";
import { useAddresses } from "@/lib/hooks/use-addresses";
import { useCart, useConfirmCartPrices } from "@/lib/hooks/use-cart";
import { describeApiError } from "@/lib/errors";
import { formatMoney } from "@/lib/format";
import { useCheckout, useCheckoutPreview } from "@/lib/hooks/use-checkout";
import { canPlaceOrder, checkoutErrorKind } from "@/lib/order-workflow";

export default function CheckoutPage() {
  const { user, isReady } = useAuth();
  const router = useRouter();
  const enabled = user?.role === "buyer";
  const cart = useCart(Boolean(enabled));
  const addressesQuery = useAddresses(Boolean(enabled));
  const confirmPrices = useConfirmCartPrices();
  const checkout = useCheckout();
  const [selectedAddressId, setSelectedAddressId] = useState("");
  const [paymentMethod, setPaymentMethod] = useState("online");
  // The total the buyer confirmed when Order answered that it changed: the
  // new quote is shown next to it and must be confirmed again.
  const [changedFrom, setChangedFrom] = useState<number | null>(null);
  const addresses = addressesQuery.data ?? [];
  const addressId =
    selectedAddressId ||
    (addresses.find((address) => address.is_default)?.id ?? addresses[0]?.id ?? "");
  const cartVersion = cart.data?.version ?? 0;
  const preview = useCheckoutPreview(
    addressId,
    cartVersion,
    Boolean(enabled && cart.data?.checkout_ready && cart.data.items.length > 0),
  );

  useEffect(() => {
    if (isReady && !enabled) router.replace("/login");
  }, [enabled, isReady, router]);

  if (!enabled) return null;
  if (cart.isPending || addressesQuery.isPending)
    return (
      <PageShell maxWidth="lg">
        <LoadingState rows={4} />
      </PageShell>
    );
  if (cart.error) {
    return (
      <PageShell maxWidth="lg">
        <ErrorState message="Không thể tải giỏ hàng của bạn." onRetry={cart.refetch} />
      </PageShell>
    );
  }
  if (!cart.data || cart.data.items.length === 0) {
    // An attempt whose answer was lost may have created an order that
    // emptied the cart: point to it instead of a blank checkout.
    const pending = user ? loadAttempt(user.id) : null;
    return (
      <PageShell maxWidth="lg">
        <EmptyState
          title={pending ? "Đơn hàng của bạn có thể đã được tạo" : "Chưa có sản phẩm để mua"}
          description={
            pending ? "Kiểm tra Đơn hàng của tôi trước khi mua lại để tránh trùng đơn." : undefined
          }
          action={
            pending ? (
              <Button asChild>
                <Link href="/orders">Đơn hàng của tôi</Link>
              </Button>
            ) : (
              <Button asChild>
                <Link href="/">Tiếp tục mua sắm</Link>
              </Button>
            )
          }
        />
      </PageShell>
    );
  }

  const data = cart.data;
  const quote = preview.data;
  const placeable = canPlaceOrder(quote, data.version);
  const blocked =
    !data.checkout_ready || cart.isFetching || confirmPrices.isPending || preview.isFetching;

  return (
    <PageShell maxWidth="lg">
      <h1 className="text-2xl font-semibold tracking-tight">Xác nhận mua hàng</h1>
      <div className="mt-6 grid gap-6 lg:grid-cols-3">
        <div className="min-w-0 space-y-4 lg:col-span-2">
          <CartReadiness
            cart={data}
            onRefresh={() => cart.refetch()}
            refreshing={cart.isFetching}
            onConfirmPrices={(vars) => confirmPrices.mutate(vars)}
            confirming={confirmPrices.isPending}
          />
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Địa chỉ giao hàng</CardTitle>
            </CardHeader>
            <CardContent>
              {addresses.length ? (
                <Select value={addressId} onValueChange={setSelectedAddressId}>
                  <SelectTrigger className="w-full min-w-0" aria-label="Địa chỉ giao hàng">
                    <span className="truncate">
                      <SelectValue placeholder="Chọn địa chỉ" />
                    </span>
                  </SelectTrigger>
                  <SelectContent>
                    {addresses.map((address) => (
                      <SelectItem key={address.id} value={address.id}>
                        {address.recipient_name} — {address.street_address}, {address.ward},{" "}
                        {address.district}, {address.province}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <p className="text-sm text-muted-foreground">
                  Bạn chưa có địa chỉ.{" "}
                  <Link className="text-primary underline" href="/addresses">
                    Thêm địa chỉ giao hàng
                  </Link>
                </p>
              )}
              <Link
                href="/addresses"
                className="mt-3 inline-block text-xs underline text-muted-foreground"
              >
                Quản lý địa chỉ
              </Link>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Hình thức thanh toán</CardTitle>
            </CardHeader>
            <CardContent>
              <RadioGroup value={paymentMethod} onValueChange={setPaymentMethod}>
                <label className="flex cursor-pointer items-center gap-3 rounded-md border p-3">
                  <RadioGroupItem value="online" />
                  <span>
                    <span className="block font-medium">Thanh toán trực tuyến</span>
                    <span className="text-sm text-muted-foreground">
                      Thanh toán an toàn qua cổng được cấu hình.
                    </span>
                  </span>
                </label>
              </RadioGroup>
            </CardContent>
          </Card>
        </div>
        <Card className="h-fit lg:sticky lg:top-20">
          <CardHeader>
            <CardTitle className="text-base">Đơn hàng</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {data.items.map((item) => (
              <div key={item.line_id} className="text-sm">
                <p className="font-medium">{item.product_name ?? "Sản phẩm"}</p>
                {item.variant_label && (
                  <p className="text-muted-foreground">{item.variant_label}</p>
                )}
                <p className="text-muted-foreground">
                  {item.quantity} × {linePrice(item)}
                </p>
                <CartLineStatus line={item} />
              </div>
            ))}
            <Separator />
            {quote && quote.cart_version === data.version ? (
              <CheckoutQuote preview={quote} />
            ) : (
              <CartSubtotal cart={data} />
            )}
            {changedFrom !== null && placeable && quote.total_amount !== changedFrom && (
              <Alert>
                <AlertDescription>
                  Tổng tiền đã đổi từ {formatMoney(changedFrom, quote.currency)} thành{" "}
                  <strong>{formatMoney(quote.total_amount, quote.currency)}</strong>. Kiểm tra lại
                  rồi bấm Đặt hàng để xác nhận.
                </AlertDescription>
              </Alert>
            )}
            {isOutcomeUnknown(checkout.error) && (
              <Alert>
                <AlertDescription>
                  Chưa rõ đơn hàng đã được tạo chưa. Bấm Đặt hàng lần nữa sẽ không tạo đơn trùng,
                  hoặc xem{" "}
                  <Link className="underline" href="/orders">
                    Đơn hàng của tôi
                  </Link>
                  .
                </AlertDescription>
              </Alert>
            )}
            {preview.isFetching && (
              <p className="text-xs text-muted-foreground">Đang tính phí vận chuyển…</p>
            )}
            {preview.error && (
              <div className="text-sm text-destructive">
                <p>{describeApiError(preview.error, "Chưa tính được phí vận chuyển.")}</p>
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto p-0"
                  onClick={() => preview.refetch()}
                >
                  Thử lại
                </Button>
              </div>
            )}
            {quote?.policies && <CheckoutPolicyNotice snapshot={quote.policies} />}
            <Button
              className="w-full"
              size="lg"
              disabled={
                !addressId ||
                checkout.isPending ||
                paymentMethod !== "online" ||
                blocked ||
                !placeable
              }
              onClick={() => {
                if (!canPlaceOrder(quote, data.version)) return;
                const confirmed = quote.total_amount;
                setChangedFrom(null);
                checkout.mutate(
                  {
                    addressId,
                    cartVersion: quote.cart_version,
                    expectedTotalAmount: confirmed,
                    acceptedPolicyVersions: quote.policy_versions,
                  },
                  {
                    onError: (err) => {
                      if (checkoutErrorKind(err) === "total_changed") setChangedFrom(confirmed);
                    },
                  },
                );
              }}
            >
              {!addressId
                ? "Cần địa chỉ giao hàng"
                : !data.checkout_ready
                  ? "Cần xử lý giỏ hàng"
                  : checkout.isPending
                    ? "Đang tạo đơn…"
                    : quote && !quote.ready
                      ? "Chưa giao được đến địa chỉ này"
                      : placeable
                        ? `Đặt hàng · ${formatMoney(quote.total_amount, quote.currency)}`
                        : "Đang báo giá…"}
            </Button>
          </CardContent>
        </Card>
      </div>
    </PageShell>
  );
}
