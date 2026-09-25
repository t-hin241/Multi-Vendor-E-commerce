"use client";

import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { OrderStatusBadge } from "@/components/order-status-badge";
import { PageShell } from "@/components/page-shell";
import { PaymentSection } from "@/components/payment-section";
import { ShipmentTimeline } from "@/components/shipment-timeline";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import { useCancelOrder, useOrder } from "@/lib/hooks/use-orders";
import { useMyShipments } from "@/lib/hooks/use-shipments";

export default function OrderDetailPage() {
  const params = useParams<{ id: string }>();
  const { user, isReady } = useAuth();
  const router = useRouter();
  const [showAllPackages, setShowAllPackages] = useState(false);

  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  const enabled = Boolean(user && user.role === "buyer");
  const orderQuery = useOrder(params.id, enabled);
  const shipmentsQuery = useMyShipments(enabled);
  const cancelOrder = useCancelOrder(params.id);

  if (!user || user.role !== "buyer") return null;

  if (orderQuery.isPending) {
    return (
      <PageShell maxWidth="sm">
        <LoadingState rows={4} />
      </PageShell>
    );
  }

  if (orderQuery.error || !orderQuery.data) {
    return (
      <PageShell maxWidth="sm">
        {orderQuery.error ? (
          <ErrorState message="Không thể tải đơn hàng này." onRetry={orderQuery.refetch} />
        ) : (
          <EmptyState title="Không tìm thấy đơn hàng" />
        )}
      </PageShell>
    );
  }

  const order = orderQuery.data;
  const packages = order.vendor_orders ?? [];
  const visiblePackages = showAllPackages ? packages : packages.slice(0, 1);

  return (
    <PageShell maxWidth="sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-2xl font-semibold tracking-tight">Đơn hàng #{order.id.slice(0, 8)}</h1>
        <OrderStatusBadge status={order.status} />
      </div>
      {order.cancellation_reason && (
        <p className="mt-1 text-sm text-destructive">Lý do: {order.cancellation_reason}</p>
      )}

      {/* The actual purchased items (name/qty/price) only ever come back on
          the flat order.items list -- VendorOrder's `items` field exists in
          the type but the live API never populates it, confirmed against a
          real order, so it can't be used to show items grouped by package. */}
      <Card className="mt-6 gap-0 py-0">
        {order.items?.map((item, i) => (
          <div key={item.product_id}>
            {i > 0 && <Separator />}
            <CardContent className="flex items-center justify-between p-4">
              <div>
                <p className="font-medium">{item.product_name}</p>
                <p className="text-sm text-muted-foreground">
                  {item.quantity} × {formatMoney(item.price_amount, order.currency)}
                </p>
              </div>
              <p className="text-sm font-medium">
                {formatMoney(item.subtotal_amount, order.currency)}
              </p>
            </CardContent>
          </div>
        ))}
      </Card>

      <Card className="mt-4">
        <CardHeader>
          <CardTitle className="text-base">Tổng cộng</CardTitle>
        </CardHeader>
        <CardContent className="text-lg font-semibold">
          {formatMoney(order.total_amount, order.currency)}
        </CardContent>
      </Card>

      <Card className="mt-4">
        <CardHeader>
          <CardTitle className="text-base">Giao đến</CardTitle>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          <p>
            {order.recipient_name} — {order.phone}
          </p>
          <p>
            {order.street_address}, {order.ward}, {order.district}, {order.province}
          </p>
        </CardContent>
      </Card>

      {/* Per-package status only -- no items here, see note above. Each
          package is generic ("Gói hàng N") since VendorOrder carries no
          vendor name/id either. */}
      {packages.length > 0 && (
        <div className="mt-4">
          <p className="text-sm font-medium">Vận chuyển</p>
          <div className="mt-2 flex flex-col gap-2">
            {visiblePackages.map((vo, idx) => {
              const shipment = shipmentsQuery.data?.find((s) => s.vendor_order_id === vo.id);
              return (
                <Card key={vo.id}>
                  <CardContent className="text-sm">
                    <div className="flex items-center justify-between gap-2">
                      <p className="font-medium">Gói hàng {idx + 1}</p>
                      <OrderStatusBadge status={vo.status} />
                    </div>
                    <p className="mt-1 text-muted-foreground">
                      {formatMoney(vo.subtotal_amount, vo.currency)}
                      {vo.shipping_fee_amount > 0 &&
                        ` + phí vận chuyển ${formatMoney(vo.shipping_fee_amount, vo.currency)}`}
                    </p>
                    {shipment && (
                      <>
                        <p className="mt-1 text-xs text-muted-foreground capitalize">
                          Vận chuyển: {shipment.status.replace(/_/g, " ")}
                          {shipment.tracking_number && ` (mã vận đơn: ${shipment.tracking_number})`}
                        </p>
                        <ShipmentTimeline shipmentId={shipment.id} />
                      </>
                    )}
                  </CardContent>
                </Card>
              );
            })}
          </div>
          {packages.length > 1 && (
            <Button
              variant="link"
              size="sm"
              className="mt-1 h-auto p-0"
              onClick={() => setShowAllPackages((v) => !v)}
            >
              {showAllPackages ? "Thu gọn" : `Xem tất cả ${packages.length} gói hàng`}
            </Button>
          )}
        </div>
      )}

      {order.status === "pending_payment" && (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle className="text-base">Thanh toán</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-wrap items-start gap-3">
            <PaymentSection orderId={order.id} />
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button
                  variant="outline"
                  className="text-destructive"
                  disabled={cancelOrder.isPending}
                >
                  {cancelOrder.isPending ? "Đang hủy…" : "Hủy đơn hàng"}
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>Hủy đơn hàng này?</AlertDialogTitle>
                  <AlertDialogDescription>
                    Không thể hoàn tác. Nếu bạn đã bắt đầu thanh toán, hãy tự mô phỏng lại trạng
                    thái đó.
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>Giữ đơn hàng</AlertDialogCancel>
                  <AlertDialogAction onClick={() => cancelOrder.mutate()}>
                    Hủy đơn hàng
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </CardContent>
        </Card>
      )}
    </PageShell>
  );
}
