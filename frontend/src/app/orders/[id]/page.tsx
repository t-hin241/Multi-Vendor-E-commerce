"use client";

import { ActionDeadline } from "@/components/support/action-deadline";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { OrderStatusBadge } from "@/components/order-status-badge";
import { RefundStatusPanel } from "@/components/orders/refund-status-panel";
import { ReturnRequestDialog } from "@/components/orders/return-request-dialog";
import { PageShell } from "@/components/page-shell";
import { PaymentSection } from "@/components/payment-section";
import { OrderPolicyCard } from "@/components/policies/order-policy-card";
import { ShipmentTimeline, shipmentStatusLabel } from "@/components/shipment-timeline";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { OpenSupportCaseDialog } from "@/components/support/open-support-case-dialog";
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
import { useCancelOrder, useCreateReturnRequest, useOrder } from "@/lib/hooks/use-orders";
import { useMyShipments } from "@/lib/hooks/use-shipments";
import { useSupportCapability } from "@/lib/hooks/use-support-cases";
import {
  orderRefundStatusLabels,
  returnStatusLabel,
  returnableQuantity,
} from "@/lib/order-workflow";

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
  const createReturn = useCreateReturnRequest(params.id);
  const supportCapability = useSupportCapability(enabled);

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
          <div key={item.id}>
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

      {order.status === "completed" && order.items && order.items.length > 0 && (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle className="text-base">Trả hàng / hoàn tiền</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-2">
            {order.items.map((item) => (
              <ReturnRequestDialog
                key={item.id}
                item={item}
                currency={order.currency}
                maxQuantity={returnableQuantity(item, order.returns)}
                pending={createReturn.isPending}
                onSubmit={(input) => createReturn.mutateAsync({ orderItemId: item.id, ...input })}
              />
            ))}
          </CardContent>
        </Card>
      )}

      {order.returns && order.returns.length > 0 && (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle className="text-base">Yêu cầu trả hàng</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3 text-sm">
            {order.returns.map((r) => {
              const item = order.items?.find((i) => i.id === r.order_item_id);
              return (
                <div key={r.id} className="rounded-md border p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <p className="font-medium">
                      {item?.product_name ?? "Sản phẩm"} × {r.quantity}
                    </p>
                    <span className="text-xs font-medium">{returnStatusLabel(r.status)}</span>
                  </div>
                  <p className="text-muted-foreground">
                    Hoàn dự kiến {formatMoney(r.refund_amount, order.currency)}
                  </p>
                  <ActionDeadline dueAt={r.action_due_at} waitingOn={r.waiting_on} />
                  {r.vendor_note && (
                    <p className="text-muted-foreground">Người bán: {r.vendor_note}</p>
                  )}
                  {r.decision_note && (
                    <p className="text-muted-foreground">Sàn: {r.decision_note}</p>
                  )}
                </div>
              );
            })}
          </CardContent>
        </Card>
      )}

      <OrderPolicyCard orderId={order.id} scope="buyer" />

      {order.checkout_state !== "preparing" && (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle className="text-base">Hỗ trợ</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-wrap items-center gap-3 text-sm">
            {supportCapability.data?.enabled ? (
              <OpenSupportCaseDialog order={order} capability={supportCapability.data} />
            ) : (
              <p className="text-muted-foreground">
                Gặp vấn đề với đơn hàng? Liên hệ bộ phận hỗ trợ kèm mã đơn #{order.id.slice(0, 8)}.
              </p>
            )}
            <Link href="/support" className="text-primary underline">
              Yêu cầu hỗ trợ của tôi
            </Link>
          </CardContent>
        </Card>
      )}

      <Card className="mt-4">
        <CardHeader>
          <CardTitle className="text-base">Tổng cộng</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-1 text-sm">
          {order.subtotal_amount !== undefined && (
            <div className="flex justify-between">
              <span className="text-muted-foreground">Tiền hàng</span>
              <span>{formatMoney(order.subtotal_amount, order.currency)}</span>
            </div>
          )}
          {order.shipping_amount !== undefined && (
            <div className="flex justify-between">
              <span className="text-muted-foreground">Phí vận chuyển</span>
              <span>{formatMoney(order.shipping_amount, order.currency)}</span>
            </div>
          )}
          <div className="flex justify-between text-lg font-semibold">
            <span>Tổng thanh toán</span>
            <span>{formatMoney(order.total_amount, order.currency)}</span>
          </div>
          {Boolean(order.refunded_amount) && (
            <div className="flex justify-between text-success">
              <span>Đã hoàn tiền</span>
              <span>{formatMoney(order.refunded_amount ?? 0, order.currency)}</span>
            </div>
          )}
          {order.refunds?.map((refund) => (
            <p key={refund.id} className="text-xs text-muted-foreground">
              Hoàn {formatMoney(refund.amount, refund.currency)}:{" "}
              {orderRefundStatusLabels[refund.status] ?? refund.status}
            </p>
          ))}
        </CardContent>
      </Card>

      <RefundStatusPanel orderId={order.id} />

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
                        <p className="mt-1 text-xs text-muted-foreground">
                          Vận chuyển: {shipmentStatusLabel(shipment.status)}
                          {shipment.tracking_number && ` · mã vận đơn ${shipment.tracking_number}`}
                        </p>
                        <ActionDeadline
                          dueAt={shipment.action_due_at}
                          waitingOn={shipment.waiting_on}
                        />
                        {shipment.shipped_at && (
                          <p className="text-xs text-muted-foreground">
                            Giao cho vận chuyển:{" "}
                            {new Date(shipment.shipped_at).toLocaleString("vi-VN")}
                          </p>
                        )}
                        {shipment.delivered_at && (
                          <p className="text-xs text-muted-foreground">
                            Đã nhận hàng: {new Date(shipment.delivered_at).toLocaleString("vi-VN")}
                          </p>
                        )}
                        {shipment.failed_attempts > 0 && shipment.status === "shipped" && (
                          <p className="text-xs text-warning">
                            Giao không thành công {shipment.failed_attempts} lần
                            {shipment.last_attempt_reason && `: ${shipment.last_attempt_reason}`}
                          </p>
                        )}
                        {shipment.status === "interception_requested" && (
                          <p className="text-xs text-warning">
                            Đơn đã hủy; đang chờ đơn vị vận chuyển xác nhận có chặn được hàng không.
                          </p>
                        )}
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

      {order.checkout_state === "preparing" && (
        <p className="mt-4 text-sm text-muted-foreground">
          Đơn hàng đang được giữ hàng. Bạn có thể thanh toán sau vài giây.
        </p>
      )}

      {order.status === "pending_payment" && order.checkout_state !== "preparing" && (
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
                    Không thể hoàn tác. Nếu bạn vừa thanh toán xong, hãy đợi vài phút để đơn cập
                    nhật trước khi hủy.
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
