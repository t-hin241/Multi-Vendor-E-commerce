"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { PageShell } from "@/components/page-shell";
import { EmptyState, LoadingState } from "@/components/states/query-state";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import { useAddresses } from "@/lib/hooks/use-addresses";
import { useCart } from "@/lib/hooks/use-cart";
import { useCheckout } from "@/lib/hooks/use-checkout";

export default function CheckoutPage() {
  const { user, isReady } = useAuth();
  const router = useRouter();
  const enabled = user?.role === "buyer";
  const cart = useCart(Boolean(enabled));
  const addressesQuery = useAddresses(Boolean(enabled));
  const checkout = useCheckout();
  const [selectedAddressId, setSelectedAddressId] = useState("");
  const [paymentMethod, setPaymentMethod] = useState("online");

  useEffect(() => {
    if (isReady && !enabled) router.replace("/login");
  }, [enabled, isReady, router]);

  if (!enabled) return null;
  if (cart.isPending || addressesQuery.isPending) return <PageShell maxWidth="lg"><LoadingState rows={4} /></PageShell>;
  if (!cart.data || cart.data.items.length === 0) {
    return <PageShell maxWidth="lg"><EmptyState title="Chưa có sản phẩm để mua" action={<Button asChild><Link href="/">Tiếp tục mua sắm</Link></Button>} /></PageShell>;
  }

  const addresses = addressesQuery.data ?? [];
  const defaultAddressId = addresses.find((address) => address.is_default)?.id ?? addresses[0]?.id ?? "";
  const addressId = selectedAddressId || defaultAddressId;

  return (
    <PageShell maxWidth="lg">
      <h1 className="text-2xl font-semibold tracking-tight">Xác nhận mua hàng</h1>
      <div className="mt-6 grid gap-6 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <Card>
            <CardHeader><CardTitle className="text-base">Địa chỉ giao hàng</CardTitle></CardHeader>
            <CardContent>
              {addresses.length ? <Select value={addressId} onValueChange={setSelectedAddressId}><SelectTrigger><SelectValue placeholder="Chọn địa chỉ" /></SelectTrigger><SelectContent>{addresses.map((address) => <SelectItem key={address.id} value={address.id}>{address.recipient_name} — {address.street_address}, {address.ward}, {address.district}, {address.province}</SelectItem>)}</SelectContent></Select> : <p className="text-sm text-muted-foreground">Bạn chưa có địa chỉ. <Link className="text-primary underline" href="/addresses">Thêm địa chỉ giao hàng</Link></p>}
              <Link href="/addresses" className="mt-3 inline-block text-xs underline text-muted-foreground">Quản lý địa chỉ</Link>
            </CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle className="text-base">Hình thức thanh toán</CardTitle></CardHeader>
            <CardContent>
              <RadioGroup value={paymentMethod} onValueChange={setPaymentMethod}>
                <label className="flex cursor-pointer items-center gap-3 rounded-md border p-3"><RadioGroupItem value="online" /><span><span className="block font-medium">Thanh toán trực tuyến</span><span className="text-sm text-muted-foreground">Thanh toán an toàn qua cổng được cấu hình.</span></span></label>
              </RadioGroup>
            </CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle className="text-base">Voucher</CardTitle></CardHeader>
            <CardContent><Button variant="outline" disabled>Chọn voucher (sắp có)</Button></CardContent>
          </Card>
        </div>
        <Card className="h-fit lg:sticky lg:top-20">
          <CardHeader><CardTitle className="text-base">Đơn hàng</CardTitle></CardHeader>
          <CardContent className="space-y-3">
            {cart.data.items.map((item) => <div key={item.variant_id ? `${item.product_id}:${item.variant_id}` : item.product_id} className="text-sm"><p className="font-medium">{item.product_name ?? item.product_id}</p><p className="text-muted-foreground">{item.quantity} × {formatMoney(item.price_amount, item.currency)}</p></div>)}
            <Separator />
            <div className="flex justify-between text-lg font-semibold"><span>Tổng cộng</span><span>{formatMoney(cart.data.total)}</span></div>
            <Button className="w-full" size="lg" disabled={!addressId || checkout.isPending || paymentMethod !== "online"} onClick={() => checkout.mutate(addressId)}>{!addressId ? "Cần địa chỉ giao hàng" : checkout.isPending ? "Đang tạo đơn…" : "Đặt hàng và thanh toán"}</Button>
          </CardContent>
        </Card>
      </div>
    </PageShell>
  );
}
