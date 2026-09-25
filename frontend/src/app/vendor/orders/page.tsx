"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import { OrderStatusBadge } from "@/components/order-status-badge";
import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { StatCard } from "@/components/vendor/stat-card";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";

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
      callWithAuth((token) => api.listMyShipments(token, { vendorId: selectedVendorId!, limit: 100 })),
    enabled: Boolean(selectedVendorId),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["vendor-orders"] });
    await queryClient.invalidateQueries({ queryKey: ["vendor-shipments"] });
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

      {ordersQuery.isPending && <p className="text-sm text-muted-foreground">Đang tải…</p>}
      {ordersQuery.data?.length === 0 && (
        <p className="text-sm text-muted-foreground">Chưa có đơn hàng nào.</p>
      )}

      {queue.length > 0 && (
        <div>
          <p className="text-sm font-medium">Cần xử lý ({queue.length})</p>
          <ul className="mt-2 flex flex-col gap-3">
            {queue.map((vo) => (
              <VendorOrderCard key={vo.id} vendorOrder={vo} shipment={shipmentFor(vo)} onChanged={refresh} />
            ))}
          </ul>
        </div>
      )}

      {history.length > 0 && (
        <div>
          <p className="text-sm font-medium">Lịch sử</p>
          <ul className="mt-2 flex flex-col gap-3">
            {visibleHistory.map((vo) => (
              <VendorOrderCard key={vo.id} vendorOrder={vo} shipment={shipmentFor(vo)} onChanged={refresh} />
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
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="Đơn hàng" value={s.total_orders} />
        <StatCard label="Doanh thu" value={formatMoney(s.total_revenue)} />
        <StatCard label="Hoa hồng" value={formatMoney(s.total_commission)} />
        <StatCard label="Thực nhận" value={formatMoney(s.total_net)} />
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
  onChanged,
}: {
  vendorOrder: api.VendorOrder;
  shipment?: api.Shipment;
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
        <VendorOrderActions vendorOrder={vendorOrder} shipment={shipment} onChanged={onChanged} />
      </CardContent>
    </Card>
  );
}

// VendorOrderActions lets a vendor advance their own sub-order one step at a
// time. Marking a package "shipped" also opens (or reuses) its Shipment
// record with the carrier/tracking number the buyer will see -- Order owns
// the buyer-facing status, Shipment owns the tracking detail; this UI
// exercises both to keep them consistent.
function VendorOrderActions({
  vendorOrder,
  shipment,
  onChanged,
}: {
  vendorOrder: api.VendorOrder;
  shipment?: api.Shipment;
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [trackingNumber, setTrackingNumber] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isBusy, setIsBusy] = useState(false);

  async function handleStartProcessing() {
    setError(null);
    setIsBusy(true);
    try {
      await callWithAuth((token) => api.updateVendorOrderStatus(token, vendorOrder.id, "processing"));
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể cập nhật đơn hàng.");
    } finally {
      setIsBusy(false);
    }
  }

  async function handleMarkShipped(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsBusy(true);
    try {
      await callWithAuth(async (token) => {
        // The shipment is normally already created automatically at
        // checkout (with its carrier and fee already set) -- createOrGet
        // here is just the fallback for the rare case that call failed.
        const shipment = await api.createOrGetShipment(token, vendorOrder.id);
        if (shipment.status === "pending") {
          await api.advanceShipment(token, shipment.id, "ready_to_ship");
        }
        await api.advanceShipment(token, shipment.id, "shipped", trackingNumber);
        await api.updateVendorOrderStatus(token, vendorOrder.id, "shipped");
      });
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể đánh dấu đơn đã giao.");
    } finally {
      setIsBusy(false);
    }
  }

  async function handleMarkCompleted() {
    setError(null);
    setIsBusy(true);
    try {
      await callWithAuth((token) => api.updateVendorOrderStatus(token, vendorOrder.id, "completed"));
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể cập nhật đơn hàng.");
    } finally {
      setIsBusy(false);
    }
  }

  return (
    <div className="mt-3">
      <Separator className="mb-3" />
      {vendorOrder.status === "paid" && (
        <Button size="sm" onClick={handleStartProcessing} disabled={isBusy}>
          Bắt đầu xử lý
        </Button>
      )}

      {vendorOrder.status === "processing" && (
        <form onSubmit={handleMarkShipped} className="flex flex-wrap items-end gap-2">
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            Mã vận đơn
            <Input
              required
              value={trackingNumber}
              onChange={(e) => setTrackingNumber(e.target.value)}
              className="w-40"
            />
          </label>
          <Button type="submit" size="sm" disabled={isBusy}>
            Đánh dấu đã giao
          </Button>
        </form>
      )}

      {vendorOrder.status === "shipped" && (
        <Button size="sm" variant="secondary" onClick={handleMarkCompleted} disabled={isBusy}>
          Đánh dấu hoàn tất
        </Button>
      )}

      {shipment?.status === "interception_requested" && (
        <CarrierInterceptionActions shipmentId={shipment.id} onChanged={onChanged} />
      )}

      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
    </div>
  );
}

// CarrierInterceptionActions stands in for the carrier's own callback: the
// buyer cancelled this order after the package had already shipped, so
// Shipment asked the carrier (the mock adapter, until a real one is
// integrated) whether it could still be pulled back. In production this
// resolves on its own via the carrier's webhook; these buttons exist so the
// mock adapter's decision can be exercised in local/dev environments.
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
      await callWithAuth((token) => api.simulateCarrierDecision(token, shipmentId, accepted));
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể ghi nhận quyết định của đơn vị vận chuyển.");
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
