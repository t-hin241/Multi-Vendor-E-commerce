"use client";

import { Trash2 } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { PageShell } from "@/components/page-shell";
import { QuantityStepper } from "@/components/quantity-stepper";
import { SectionHeader } from "@/components/section-header";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import { useAddresses } from "@/lib/hooks/use-addresses";
import { useCart, useSetCartItemQuantity } from "@/lib/hooks/use-cart";
import { useCheckout } from "@/lib/hooks/use-checkout";

export default function CartPage() {
  const { user, isReady } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  const enabled = Boolean(user && user.role === "buyer");
  const cartQuery = useCart(enabled);
  const addressesQuery = useAddresses(enabled);
  const setQuantity = useSetCartItemQuantity();
  const checkout = useCheckout();
  const [selectedAddressId, setSelectedAddressId] = useState("");

  const addresses = addressesQuery.data ?? [];
  const defaultAddressId = addresses.find((a) => a.is_default)?.id ?? addresses[0]?.id ?? "";
  const effectiveAddressId = selectedAddressId || defaultAddressId;
  const selectedAddress = addresses.find((a) => a.id === effectiveAddressId);

  if (!user || user.role !== "buyer") return null;

  return (
    <PageShell maxWidth="lg">
      <SectionHeader as="h1" title="Giỏ hàng của bạn" />

      {cartQuery.isPending && <LoadingState className="mt-6" rows={3} />}
      {cartQuery.error && (
        <ErrorState message="Không thể tải giỏ hàng của bạn." onRetry={cartQuery.refetch} />
      )}

      {cartQuery.data && cartQuery.data.items.length === 0 && (
        <EmptyState
          className="mt-6"
          title="Giỏ hàng của bạn đang trống"
          description="Khám phá sản phẩm ở trang chủ và thêm vào giỏ nhé."
          action={
            <Button asChild>
              <Link href="/">Tiếp tục mua sắm</Link>
            </Button>
          }
        />
      )}

      {cartQuery.data && cartQuery.data.items.length > 0 && (
        <div className="mt-6 grid gap-6 lg:grid-cols-3">
          <div className="flex flex-col gap-4 lg:col-span-2">
            {/* No product image or vendor grouping here yet: CartLine has no
                image URL or vendor_id field at all -- needs a backend
                cart-response change, not a client-side cache-join. */}
            <Card className="gap-0 py-0">
              {cartQuery.data.items.map((line, i) => (
                <div
                  key={line.variant_id ? `${line.product_id}:${line.variant_id}` : line.product_id}
                >
                  {i > 0 && <Separator />}
                  <div className="flex flex-wrap items-center justify-between gap-4 p-4">
                    <div>
                      <p className="font-medium">{line.product_name ?? line.product_id}</p>
                      {line.variant_label && (
                        <p className="text-sm text-muted-foreground">{line.variant_label}</p>
                      )}
                      <p className="text-sm text-muted-foreground">
                        {formatMoney(line.price_amount, line.currency)} / sản phẩm
                      </p>
                      {!line.available && (
                        <Badge variant="destructive" className="mt-1">
                          Không còn khả dụng
                        </Badge>
                      )}
                    </div>
                    <div className="flex items-center gap-3">
                      <QuantityStepper
                        value={line.quantity}
                        min={1}
                        size="sm"
                        onChange={(next) =>
                          setQuantity.mutate({
                            productId: line.product_id,
                            quantity: next,
                            variantId: line.variant_id,
                          })
                        }
                        ariaLabel={`số lượng cho ${line.product_name ?? "sản phẩm"}`}
                      />
                      <span className="w-28 text-right text-sm font-medium">
                        {formatMoney(line.subtotal, line.currency)}
                      </span>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="text-destructive hover:text-destructive"
                        aria-label="Xóa sản phẩm"
                        onClick={() =>
                          setQuantity.mutate({
                            productId: line.product_id,
                            quantity: 0,
                            variantId: line.variant_id,
                          })
                        }
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                  </div>
                </div>
              ))}
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="text-base">Địa chỉ giao hàng</CardTitle>
              </CardHeader>
              <CardContent>
                {addresses.length > 0 ? (
                  <>
                    {selectedAddress?.is_default && (
                      <Badge variant="success" className="mb-2">
                        Mặc định
                      </Badge>
                    )}
                    <Select value={effectiveAddressId} onValueChange={setSelectedAddressId}>
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {addresses.map((a) => (
                          <SelectItem key={a.id} value={a.id}>
                            {a.recipient_name} — {a.street_address}, {a.ward}, {a.district},{" "}
                            {a.province}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </>
                ) : (
                  <p className="text-sm text-muted-foreground">
                    Bạn chưa có địa chỉ nào.{" "}
                    <Link href="/addresses" className="text-primary underline">
                      Thêm địa chỉ
                    </Link>{" "}
                    trước khi đặt hàng.
                  </p>
                )}
                <Link
                  href="/addresses"
                  className="mt-2 inline-block text-xs text-muted-foreground underline"
                >
                  Quản lý địa chỉ
                </Link>
              </CardContent>
            </Card>
          </div>

          <Card className="h-fit lg:sticky lg:top-20">
            <CardHeader>
              <CardTitle className="text-base">Tóm tắt đơn hàng</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              <div className="flex justify-between text-lg font-semibold">
                <span>Tổng cộng</span>
                <span>{formatMoney(cartQuery.data.total)}</span>
              </div>
              <Button
                size="lg"
                onClick={() => checkout.mutate(effectiveAddressId)}
                disabled={checkout.isPending || !effectiveAddressId}
              >
                {!effectiveAddressId
                  ? "Cần địa chỉ giao hàng"
                  : checkout.isPending
                    ? "Đang tạo đơn…"
                    : "Đặt hàng"}
              </Button>
            </CardContent>
          </Card>
        </div>
      )}
    </PageShell>
  );
}
