"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function formatMoney(amount: number, currency: string) {
  return `${amount.toLocaleString("vi-VN")} ${currency}`;
}

export default function VendorOrdersPage() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();

  const ordersQuery = useQuery({
    queryKey: ["vendor-orders"],
    queryFn: () => callWithAuth((token) => api.listVendorOrders(token)),
  });

  const shipmentsQuery = useQuery({
    queryKey: ["vendor-shipments"],
    queryFn: () => callWithAuth((token) => api.listMyShipments(token, { limit: 100 })),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["vendor-orders"] });
    await queryClient.invalidateQueries({ queryKey: ["vendor-shipments"] });
  }

  return (
    <main className="mx-auto max-w-2xl px-6 py-12">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-slate-900">My vendor orders</h1>
        <ExportCsvButton />
      </div>

      <SummarySection />

      {ordersQuery.isPending && <p className="mt-6 text-sm text-slate-500">Loading…</p>}
      {ordersQuery.data?.length === 0 && (
        <p className="mt-6 text-sm text-slate-500">No orders yet.</p>
      )}

      <ul className="mt-6 space-y-3">
        {ordersQuery.data?.map((vo) => (
          <li key={vo.id} className="rounded border border-slate-200 bg-white p-4">
            <div className="flex items-center justify-between">
              <div>
                <p className="font-medium text-slate-900">Order #{vo.order_id.slice(0, 8)}</p>
                <p className="text-sm text-slate-600">{new Date(vo.created_at).toLocaleString()}</p>
              </div>
              <div className="text-right">
                <p className="text-sm font-medium capitalize text-slate-900">
                  {vo.status.replace("_", " ")}
                </p>
                <p className="text-sm text-slate-600">
                  {formatMoney(vo.subtotal_amount, vo.currency)}
                  {vo.shipping_fee_amount > 0 && ` + ${formatMoney(vo.shipping_fee_amount, vo.currency)} ship`}
                </p>
              </div>
            </div>
            {vo.items && vo.items.length > 0 && (
              <ul className="mt-3 space-y-1 border-t border-slate-100 pt-3">
                {vo.items.map((item, idx) => (
                  <li key={idx} className="flex items-center justify-between text-sm text-slate-700">
                    <span>
                      {item.product_name}
                      {item.variant_label ? ` — ${item.variant_label}` : ""}
                      {item.variant_sku ? ` (${item.variant_sku})` : ""}
                      {` × ${item.quantity}`}
                    </span>
                    <span className="text-slate-600">
                      {formatMoney(item.subtotal_amount, vo.currency)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            <VendorOrderActions
              vendorOrder={vo}
              shipment={shipmentsQuery.data?.find((s) => s.vendor_order_id === vo.id)}
              onChanged={refresh}
            />
          </li>
        ))}
      </ul>
    </main>
  );
}

function SummarySection() {
  const { callWithAuth } = useAuth();
  const summaryQuery = useQuery({
    queryKey: ["vendor-summary"],
    queryFn: () => callWithAuth((token) => api.getVendorSummary(token)),
  });

  if (summaryQuery.isPending || !summaryQuery.data) return null;
  const s = summaryQuery.data;

  return (
    <div className="mt-4 rounded border border-slate-200 bg-white p-4">
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Orders" value={String(s.total_orders)} />
        <Stat label="Revenue" value={formatMoney(s.total_revenue, "VND")} />
        <Stat label="Commission" value={formatMoney(s.total_commission, "VND")} />
        <Stat label="Net" value={formatMoney(s.total_net, "VND")} />
      </div>

      {s.top_products.length > 0 && (
        <div className="mt-4 border-t border-slate-100 pt-3">
          <p className="text-sm font-medium text-slate-700">Best sellers</p>
          <ul className="mt-1 space-y-1">
            {s.top_products.map((p) => (
              <li key={p.product_id} className="text-sm text-slate-600">
                {p.product_name} — {p.quantity_sold} sold ({formatMoney(p.revenue_amount, "VND")})
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-xs uppercase tracking-wide text-slate-400">{label}</p>
      <p className="text-sm font-semibold text-slate-900">{value}</p>
    </div>
  );
}

function ExportCsvButton() {
  const { callWithAuth } = useAuth();
  const [isBusy, setIsBusy] = useState(false);

  async function handleExport() {
    setIsBusy(true);
    try {
      const csv = await callWithAuth((token) => api.exportVendorOrdersCSV(token));
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
    <button
      onClick={handleExport}
      disabled={isBusy}
      className="rounded border border-slate-300 px-3 py-1.5 text-sm text-slate-700 disabled:opacity-50"
    >
      {isBusy ? "Exporting…" : "Export CSV"}
    </button>
  );
}

// VendorOrderActions lets a vendor advance their own sub-order one step at a
// time. Marking a package "shipped" also opens (or reuses) its Shipment
// record with the carrier/tracking number the buyer will see — Order owns
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
      await callWithAuth((token) =>
        api.updateVendorOrderStatus(token, vendorOrder.id, "processing"),
      );
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update this order.");
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
        // checkout (with its carrier and fee already set) — createOrGet
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
      setError(err instanceof api.ApiError ? err.message : "Could not mark this order shipped.");
    } finally {
      setIsBusy(false);
    }
  }

  async function handleMarkCompleted() {
    setError(null);
    setIsBusy(true);
    try {
      await callWithAuth((token) =>
        api.updateVendorOrderStatus(token, vendorOrder.id, "completed"),
      );
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update this order.");
    } finally {
      setIsBusy(false);
    }
  }

  return (
    <div className="mt-3 border-t border-slate-100 pt-3">
      {vendorOrder.status === "paid" && (
        <button
          onClick={handleStartProcessing}
          disabled={isBusy}
          className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
        >
          Start processing
        </button>
      )}

      {vendorOrder.status === "processing" && (
        <form onSubmit={handleMarkShipped} className="flex flex-wrap items-end gap-2">
          <label className="flex flex-col gap-1 text-xs text-slate-600">
            Tracking number
            <input
              required
              value={trackingNumber}
              onChange={(e) => setTrackingNumber(e.target.value)}
              className="rounded border border-slate-300 px-2 py-1 text-sm"
            />
          </label>
          <button
            type="submit"
            disabled={isBusy}
            className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
          >
            Mark shipped
          </button>
        </form>
      )}

      {vendorOrder.status === "shipped" && (
        <button
          onClick={handleMarkCompleted}
          disabled={isBusy}
          className="rounded bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
        >
          Mark completed
        </button>
      )}

      {shipment?.status === "interception_requested" && (
        <CarrierInterceptionActions shipmentId={shipment.id} onChanged={onChanged} />
      )}

      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}
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
      setError(err instanceof api.ApiError ? err.message : "Could not record the carrier's decision.");
    } finally {
      setIsBusy(false);
    }
  }

  return (
    <div className="mt-3 rounded border border-amber-200 bg-amber-50 p-3">
      <p className="text-sm text-amber-800">
        The buyer cancelled this order after it shipped. We asked the carrier to intercept it —
        record their decision once you hear back:
      </p>
      <div className="mt-2 flex gap-2">
        <button
          onClick={() => handleDecision(true)}
          disabled={isBusy}
          className="rounded bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
        >
          Carrier stopped it
        </button>
        <button
          onClick={() => handleDecision(false)}
          disabled={isBusy}
          className="rounded border border-slate-300 px-3 py-1.5 text-sm text-slate-700 disabled:opacity-50"
        >
          Carrier couldn&apos;t stop it
        </button>
      </div>
      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}
    </div>
  );
}
