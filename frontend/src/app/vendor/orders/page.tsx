"use client";

import { ActionDeadline } from "@/components/support/action-deadline";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import { OrderStatusBadge } from "@/components/order-status-badge";
import { ReturnReceiptForm } from "@/components/orders/return-receipt-form";
import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { StatCard } from "@/components/vendor/stat-card";
import { VendorCancelForm } from "@/components/vendor/vendor-cancel-form";
import { VendorDeliveryExceptionsSection } from "@/components/vendor/delivery-exceptions-section";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import { canReceive, returnStatusLabel, vendorCanConfirm } from "@/lib/order-workflow";
import { queryKeys } from "@/lib/query-keys";

// A vendor order still needs a vendor action if it's paid (not started),
// processing (not shipped), or shipped but the carrier's interception
// decision is still pending -- everything else is resolved history.
function needsAction(vo: api.VendorOrder, shipment?: api.Shipment): boolean {
  if (vo.status === "paid" || vo.status === "processing") return true;
  return vo.status === "shipped" && shipment?.status === "interception_requested";
}

export default function VendorOrdersPage() {
  const { callWithAuth, selectedVendorId } = useAuth();
  const queryClient = useQueryClient();
  const [showAllHistory, setShowAllHistory] = useState(false);

  const ordersQuery = useQuery({
    queryKey: ["vendor-orders", selectedVendorId],
    queryFn: () => callWithAuth((token) => api.listVendorOrders(token, selectedVendorId!)),
    enabled: Boolean(selectedVendorId),
  });

  const shipmentsQuery = useQuery({
    queryKey: ["vendor-shipments", selectedVendorId],
    queryFn: () =>
      callWithAuth((token) =>
        api.listMyShipments(token, { vendorId: selectedVendorId!, limit: 100 }),
      ),
    enabled: Boolean(selectedVendorId),
  });

  // AF-03: open cancellation requests fence a package's handover.
  const cancellationsQuery = useQuery({
    queryKey: ["vendor-cancellations", selectedVendorId],
    queryFn: () =>
      callWithAuth((token) => api.listVendorCancellations(token, selectedVendorId!, "")),
    enabled: Boolean(selectedVendorId),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["vendor-orders"] });
    await queryClient.invalidateQueries({ queryKey: ["vendor-shipments"] });
    await queryClient.invalidateQueries({ queryKey: ["vendor-cancellations"] });
  }

  if (!selectedVendorId) {
    return (
      <p className="text-sm text-muted-foreground">
        Bạn chưa có cửa hàng nào.{" "}
        <Link href="/vendor/shops" className="text-primary underline">
          Quản lý cửa hàng
        </Link>
      </p>
    );
  }

  const shipmentFor = (vo: api.VendorOrder) =>
    shipmentsQuery.data?.find((s) => s.vendor_order_id === vo.id);
  const cancellationFor = (vo: api.VendorOrder) =>
    cancellationsQuery.data?.find(
      (c) => c.vendor_order_id === vo.id && c.status !== "rejected" && c.status !== "resolved",
    );

  const orders = ordersQuery.data ?? [];
  const queue = orders.filter((vo) => needsAction(vo, shipmentFor(vo)));
  const history = orders.filter((vo) => !needsAction(vo, shipmentFor(vo)));
  const visibleHistory = showAllHistory ? history : history.slice(0, 5);

  return (
    <div className="flex flex-col gap-6">
      <SectionHeader
        as="h1"
        title="Đơn hàng của tôi"
        action={<ExportCsvButton vendorId={selectedVendorId} />}
      />

      <SummarySection vendorId={selectedVendorId} />

      <VendorReturnsSection vendorId={selectedVendorId} />

      <VendorDeliveryExceptionsSection vendorId={selectedVendorId} vendorOrders={ordersQuery.data ?? []} />

      {ordersQuery.isPending && <p className="text-sm text-muted-foreground">Đang tải…</p>}
      {ordersQuery.data?.length === 0 && (
        <p className="text-sm text-muted-foreground">Chưa có đơn hàng nào.</p>
      )}

      {queue.length > 0 && (
        <div>
          <p className="text-sm font-medium">Cần xử lý ({queue.length})</p>
          <ul className="mt-2 flex flex-col gap-3">
            {queue.map((vo) => (
              <VendorOrderCard
                key={vo.id}
                vendorOrder={vo}
                shipment={shipmentFor(vo)}
                cancellation={cancellationFor(vo)}
                onChanged={refresh}
              />
            ))}
          </ul>
        </div>
      )}

      {history.length > 0 && (
        <div>
          <p className="text-sm font-medium">Lịch sử</p>
          <ul className="mt-2 flex flex-col gap-3">
            {visibleHistory.map((vo) => (
              <VendorOrderCard
                key={vo.id}
                vendorOrder={vo}
                shipment={shipmentFor(vo)}
                cancellation={cancellationFor(vo)}
                onChanged={refresh}
              />
            ))}
          </ul>
          {history.length > 5 && (
            <Button
              variant="link"
              size="sm"
              className="mt-1 h-auto p-0"
              onClick={() => setShowAllHistory((v) => !v)}
            >
              {showAllHistory ? "Thu gọn" : `Xem tất cả ${history.length} đơn`}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

function SummarySection({ vendorId }: { vendorId: string }) {
  const { callWithAuth } = useAuth();
  const summaryQuery = useQuery({
    queryKey: ["vendor-summary", vendorId],
    queryFn: () => callWithAuth((token) => api.getVendorSummary(token, vendorId)),
  });

  if (summaryQuery.isPending || !summaryQuery.data) return null;
  const s = summaryQuery.data;

  return (
    <div>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
        <StatCard label="Đơn hàng" value={s.total_orders} />
        <StatCard label="Doanh thu" value={formatMoney(s.total_revenue)} />
        <StatCard label="Hoa hồng" value={formatMoney(s.total_commission)} />
        <StatCard label="Thực nhận" value={formatMoney(s.total_net)} />
        <StatCard label="Đã hoàn tiền" value={formatMoney(s.total_refunded ?? 0)} />
      </div>

      {s.top_products.length > 0 && (
        <Card className="mt-3">
          <CardContent>
            <p className="text-sm font-medium">Bán chạy nhất</p>
            <ul className="mt-2 flex flex-col gap-1">
              {s.top_products.map((p) => (
                <li key={p.product_id} className="text-sm text-muted-foreground">
                  {p.product_name} — đã bán {p.quantity_sold} ({formatMoney(p.revenue_amount)})
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

// VendorReturnsSection: the vendor confirms a buyer's return request before
// admin decides, and records the goods when they come back, which starts
// the refund.
function VendorReturnsSection({ vendorId }: { vendorId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("");
  const returnsQuery = useQuery({
    queryKey: queryKeys.vendorReturns(vendorId, status),
    queryFn: () =>
      callWithAuth((token) =>
        api.listVendorReturns(token, vendorId, { status: status || undefined, limit: 50 }),
      ),
  });
  const open = (returnsQuery.data ?? []).filter(
    (r) => status !== "" || !["refunded", "rejected"].includes(r.status),
  );

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["vendor-returns", vendorId] });
  }

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm font-medium">Yêu cầu trả hàng</p>
        <div className="flex gap-1">
          {(
            [
              ["", "Đang xử lý"],
              ["refunded", "Đã hoàn"],
              ["rejected", "Từ chối"],
            ] as const
          ).map(([value, label]) => (
            <Button
              key={value}
              size="sm"
              variant={status === value ? "default" : "outline"}
              onClick={() => setStatus(value)}
            >
              {label}
            </Button>
          ))}
        </div>
      </div>
      {returnsQuery.error && (
        <p className="mt-2 text-sm text-destructive">Không thể tải yêu cầu trả hàng.</p>
      )}
      {returnsQuery.data && open.length === 0 && (
        <p className="mt-2 text-sm text-muted-foreground">Không có yêu cầu nào.</p>
      )}
      <ul className="mt-2 flex flex-col gap-2">
        {open.map((r) => (
          <li key={r.id}>
            <Card>
              <CardContent className="flex flex-col gap-1 text-sm">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="font-medium">
                    Đơn #{r.order_id.slice(0, 8)} · {r.quantity} sản phẩm ·{" "}
                    {formatMoney(r.refund_amount)}
                  </span>
                  <span className="text-xs font-medium">{returnStatusLabel(r.status)}</span>
                </div>
                <p className="text-muted-foreground">Lý do: {r.reason}</p>
                {r.evidence && <p className="text-muted-foreground">Bằng chứng: {r.evidence}</p>}
                {r.decision_note && <p className="text-muted-foreground">Sàn: {r.decision_note}</p>}
                {r.return_code && (
                  <p className="text-xs text-muted-foreground">
                    Mã trả hàng {r.return_code}
                    {r.dispatch_tracking
                      ? ` · người mua đã gửi qua ${r.dispatch_carrier}, mã vận đơn ${r.dispatch_tracking}`
                      : " · chờ người mua gửi hàng"}
                  </p>
                )}
                {r.shipping_status === "destination_missing" && (
                  <p className="text-xs text-destructive">
                    Chưa có địa chỉ nhận hàng trả đã xác minh: chọn ở trang Vận chuyển.
                  </p>
                )}
                <div className="mt-1 flex flex-wrap gap-2">
                  {vendorCanConfirm(r) && (
                    <Button
                      size="sm"
                      onClick={async () => {
                        const note = window.prompt("Ghi chú cho sàn (không bắt buộc):") ?? "";
                        await callWithAuth((token) => api.confirmReturnByVendor(token, r.id, note));
                        await refresh();
                      }}
                    >
                      Xác nhận yêu cầu
                    </Button>
                  )}
                  {canReceive(r) && (
                    <ReturnReceiptForm returnRequest={r} scope="vendor" onDone={refresh} />
                  )}
                </div>
              </CardContent>
            </Card>
          </li>
        ))}
      </ul>
    </div>
  );
}

function ExportCsvButton({ vendorId }: { vendorId: string }) {
  const { callWithAuth } = useAuth();
  const [isBusy, setIsBusy] = useState(false);

  async function handleExport() {
    setIsBusy(true);
    try {
      const csv = await callWithAuth((token) => api.exportVendorOrdersCSV(token, vendorId));
      const blob = new Blob([csv], { type: "text/csv" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "vendor-orders.csv";
      a.click();
      URL.revokeObjectURL(url);
    } finally {
      setIsBusy(false);
    }
  }

  return (
    <Button variant="outline" size="sm" onClick={handleExport} disabled={isBusy}>
      <Download className="size-4" />
      {isBusy ? "Đang xuất…" : "Xuất CSV"}
    </Button>
  );
}

function VendorOrderCard({
  vendorOrder,
  shipment,
  cancellation,
  onChanged,
}: {
  vendorOrder: api.VendorOrder;
  shipment?: api.Shipment;
  cancellation?: api.CancellationRequest;
  onChanged: () => void;
}) {
  return (
    <Card>
      <CardContent>
        <div className="flex items-center justify-between">
          <div>
            <p className="font-medium">Đơn #{vendorOrder.order_id.slice(0, 8)}</p>
            <p className="text-sm text-muted-foreground">
              {new Date(vendorOrder.created_at).toLocaleString("vi-VN")}
            </p>
          </div>
          <div className="text-right">
            <OrderStatusBadge status={vendorOrder.status} />
            <p className="mt-1 text-sm text-muted-foreground">
              {formatMoney(vendorOrder.subtotal_amount, vendorOrder.currency)}
              {vendorOrder.shipping_fee_amount > 0 &&
                ` + ${formatMoney(vendorOrder.shipping_fee_amount, vendorOrder.currency)} ship`}
            </p>
          </div>
        </div>
        {vendorOrder.items && vendorOrder.items.length > 0 && (
          <>
            <Separator className="my-3" />
            <ul className="flex flex-col gap-1">
              {vendorOrder.items.map((item, idx) => (
                <li key={idx} className="flex items-center justify-between text-sm">
                  <span>
                    {item.product_name}
                    {item.variant_label ? ` — ${item.variant_label}` : ""}
                    {item.variant_sku ? ` (${item.variant_sku})` : ""}
                    {` × ${item.quantity}`}
                  </span>
                  <span className="text-muted-foreground">
                    {formatMoney(item.subtotal_amount, vendorOrder.currency)}
                  </span>
                </li>
              ))}
            </ul>
          </>
        )}
        <VendorOrderActions
          vendorOrder={vendorOrder}
          shipment={shipment}
          cancellation={cancellation}
          onChanged={onChanged}
        />
      </CardContent>
    </Card>
  );
}

// VendorOrderActions: the vendor starts preparing the order (Order), then
// works on the shipment: hand it over with a tracking number, record
// delivery, a failed attempt or a return. Shipment reports shipped and
// delivered to Order, which updates the order status itself.
function VendorOrderActions({
  vendorOrder,
  shipment,
  cancellation,
  onChanged,
}: {
  vendorOrder: api.VendorOrder;
  shipment?: api.Shipment;
  cancellation?: api.CancellationRequest;
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [trackingNumber, setTrackingNumber] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isBusy, setIsBusy] = useState(false);

  async function run(fn: (token: string) => Promise<unknown>, fallback: string) {
    setError(null);
    setIsBusy(true);
    try {
      await callWithAuth(fn);
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : fallback);
    } finally {
      setIsBusy(false);
    }
  }

  function withReason(
    question: string,
    fn: (token: string, reason: string) => Promise<unknown>,
    fallback: string,
  ) {
    const reason = window.prompt(question)?.trim();
    if (reason) void run((token) => fn(token, reason), fallback);
  }

  const canShip =
    !cancellation &&
    (vendorOrder.status === "paid" || vendorOrder.status === "processing") &&
    (!shipment || shipment.status === "pending" || shipment.status === "ready_to_ship");
  const preHandover =
    (vendorOrder.status === "paid" || vendorOrder.status === "processing") &&
    (!shipment || shipment.status === "pending" || shipment.status === "ready_to_ship");

  return (
    <div className="mt-3">
      <Separator className="mb-3" />
      {vendorOrder.status === "paid" && (
        <Button
          size="sm"
          className="mb-2"
          disabled={isBusy}
          onClick={() =>
            run(
              (token) => api.updateVendorOrderStatus(token, vendorOrder.id, "processing"),
              "Không thể cập nhật đơn hàng.",
            )
          }
        >
          Bắt đầu xử lý
        </Button>
      )}

      {canShip && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void run(async (token) => {
              // Normally created automatically once paid; this is the fallback.
              const s = shipment ?? (await api.createOrGetShipment(token, vendorOrder.id));
              await api.shipShipment(token, s.id, trackingNumber.trim());
            }, "Không thể ghi nhận đã giao cho đơn vị vận chuyển.");
          }}
          className="flex flex-wrap items-end gap-2"
        >
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            Mã vận đơn
            <Input
              required
              minLength={3}
              maxLength={64}
              value={trackingNumber}
              onChange={(e) => setTrackingNumber(e.target.value)}
              className="w-44"
            />
          </label>
          <Button type="submit" size="sm" disabled={isBusy}>
            Đã giao cho vận chuyển
          </Button>
        </form>
      )}

      {preHandover && (
        <VendorCancelForm
          vendorOrder={vendorOrder}
          openRequest={cancellation}
          onChanged={onChanged}
        />
      )}
      <ActionDeadline dueAt={shipment?.action_due_at} waitingOn={shipment?.waiting_on} />
      {shipment?.status === "shipped" && (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-muted-foreground">
            Mã vận đơn {shipment.tracking_number}
            {shipment.failed_attempts > 0 &&
              ` · ${shipment.failed_attempts} lần giao thất bại (${shipment.last_attempt_reason ?? ""})`}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              disabled={isBusy}
              onClick={() =>
                run(
                  (token) => api.markShipmentDelivered(token, shipment.id),
                  "Không thể ghi nhận đã giao.",
                )
              }
            >
              Người mua đã nhận hàng
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={isBusy}
              onClick={() =>
                withReason(
                  "Lý do giao không thành công:",
                  (token, reason) => api.recordFailedDelivery(token, shipment.id, reason),
                  "Không thể ghi nhận lần giao thất bại.",
                )
              }
            >
              Giao thất bại
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="text-destructive"
              disabled={isBusy}
              onClick={() =>
                withReason(
                  "Lý do hàng bị hoàn về:",
                  (token, reason) => api.markShipmentReturned(token, shipment.id, reason),
                  "Không thể ghi nhận hàng hoàn về.",
                )
              }
            >
              Hàng hoàn về
            </Button>
          </div>
        </div>
      )}

      {shipment?.status === "returned" && (
        <p className="text-sm text-destructive">
          Hàng đã hoàn về. Sàn sẽ xử lý hoàn tiền nếu cần; kiểm tra hàng trước khi nhập lại kho.
        </p>
      )}

      {shipment?.status === "interception_requested" && (
        <CarrierInterceptionActions shipmentId={shipment.id} onChanged={onChanged} />
      )}

      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
    </div>
  );
}

// CarrierInterceptionActions: the order was cancelled after the package
// was handed over, so the carrier was asked to stop it. The vendor records
// the carrier's answer after contacting it (audited). Until then the
// package is not assumed stopped.
function CarrierInterceptionActions({
  shipmentId,
  onChanged,
}: {
  shipmentId: string;
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const [isBusy, setIsBusy] = useState(false);

  async function handleDecision(accepted: boolean) {
    setError(null);
    setIsBusy(true);
    try {
      const note = window
        .prompt("Ghi chú từ đơn vị vận chuyển (người liên hệ, mã yêu cầu):")
        ?.trim();
      if (!note) return;
      await callWithAuth((token) => api.resolveInterception(token, shipmentId, accepted, note));
      onChanged();
    } catch (err) {
      setError(
        err instanceof api.ApiError
          ? err.message
          : "Không thể ghi nhận quyết định của đơn vị vận chuyển.",
      );
    } finally {
      setIsBusy(false);
    }
  }

  return (
    <div className="mt-3 rounded-lg border border-warning/40 bg-warning/10 p-3">
      <p className="text-sm">
        Người mua đã hủy đơn này sau khi đã giao cho đơn vị vận chuyển — chúng ta đã yêu cầu họ chặn
        lại. Ghi nhận quyết định của họ khi có phản hồi:
      </p>
      <div className="mt-2 flex gap-2">
        <Button size="sm" onClick={() => handleDecision(true)} disabled={isBusy}>
          Đã chặn được
        </Button>
        <Button size="sm" variant="outline" onClick={() => handleDecision(false)} disabled={isBusy}>
          Không chặn được
        </Button>
      </div>
      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
    </div>
  );
}
