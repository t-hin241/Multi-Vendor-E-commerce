"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function formatMoney(amount: number, currency: string) {
  return `${amount.toLocaleString("vi-VN")} ${currency}`;
}

export default function OrderDetailPage() {
  const params = useParams<{ id: string }>();
  const { user, isReady, callWithAuth } = useAuth();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const [isCancelling, setIsCancelling] = useState(false);

  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  const orderQuery = useQuery({
    queryKey: ["order", params.id],
    queryFn: () => callWithAuth((token) => api.getOrder(token, params.id)),
    enabled: Boolean(user && user.role === "buyer"),
  });

  const shipmentsQuery = useQuery({
    queryKey: ["my-shipments"],
    queryFn: () => callWithAuth((token) => api.listMyShipments(token, { limit: 100 })),
    enabled: Boolean(user && user.role === "buyer"),
  });

  async function refreshOrder() {
    await queryClient.invalidateQueries({ queryKey: ["order", params.id] });
  }

  async function handleCancel() {
    setError(null);
    setIsCancelling(true);
    try {
      await callWithAuth((token) => api.cancelOrder(token, params.id));
      await refreshOrder();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not cancel order.");
    } finally {
      setIsCancelling(false);
    }
  }

  if (!user || user.role !== "buyer") return null;
  if (orderQuery.isPending)
    return <main className="mx-auto max-w-2xl px-6 py-10 text-sm text-slate-500">Loading…</main>;
  if (orderQuery.error || !orderQuery.data)
    return (
      <main className="mx-auto max-w-2xl px-6 py-10 text-sm text-red-600">Order not found.</main>
    );

  const order = orderQuery.data;

  return (
    <main className="mx-auto max-w-2xl px-6 py-10">
      <h1 className="text-2xl font-semibold text-slate-900">Order #{order.id.slice(0, 8)}</h1>
      <p className="mt-1 text-sm font-medium capitalize text-slate-700">
        {order.status.replace("_", " ")}
      </p>
      {order.cancellation_reason && (
        <p className="mt-1 text-sm text-red-600">Reason: {order.cancellation_reason}</p>
      )}

      <ul className="mt-6 divide-y divide-slate-200 rounded border border-slate-200 bg-white">
        {order.items?.map((item) => (
          <li key={item.product_id} className="flex items-center justify-between p-4">
            <div>
              <p className="font-medium text-slate-900">{item.product_name}</p>
              <p className="text-sm text-slate-600">
                {item.quantity} × {formatMoney(item.price_amount, order.currency)}
              </p>
            </div>
            <p className="text-sm text-slate-700">
              {formatMoney(item.subtotal_amount, order.currency)}
            </p>
          </li>
        ))}
      </ul>

      <p className="mt-4 text-lg font-semibold text-slate-900">
        Total: {formatMoney(order.total_amount, order.currency)}
      </p>

      <div className="mt-4 rounded border border-slate-200 bg-white p-4">
        <p className="text-sm font-medium text-slate-700">Shipping to</p>
        <p className="mt-1 text-sm text-slate-600">
          {order.recipient_name} — {order.phone}
        </p>
        <p className="text-sm text-slate-600">
          {order.street_address}, {order.ward}, {order.district}, {order.province}
        </p>
      </div>

      {order.vendor_orders && order.vendor_orders.length > 0 && (
        <div className="mt-4">
          <p className="text-sm font-medium text-slate-700">Package progress</p>
          <ul className="mt-1 space-y-3">
            {order.vendor_orders.map((vo) => {
              const shipment = shipmentsQuery.data?.find((s) => s.vendor_order_id === vo.id);
              return (
                <li key={vo.id} className="rounded border border-slate-200 bg-white p-3">
                  <p className="text-sm capitalize text-slate-700">
                    {formatMoney(vo.subtotal_amount, vo.currency)}
                    {vo.shipping_fee_amount > 0 &&
                      ` + ${formatMoney(vo.shipping_fee_amount, vo.currency)} shipping`}{" "}
                    — {vo.status.replace("_", " ")}
                  </p>
                  {shipment && (
                    <div className="mt-1 text-xs text-slate-500">
                      <p className="capitalize">
                        Shipment: {shipment.status.replace("_", " ")}
                        {shipment.tracking_number && ` (tracking: ${shipment.tracking_number})`}
                      </p>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}

      {error && <p className="mt-3 text-sm text-red-600">{error}</p>}

      {order.status === "pending_payment" && (
        <div className="mt-6 flex flex-wrap items-center gap-3">
          <PaymentSection orderId={order.id} onSettled={refreshOrder} />
          <button
            onClick={handleCancel}
            disabled={isCancelling}
            className="rounded border border-red-300 px-4 py-2 text-sm font-medium text-red-600 disabled:opacity-50"
          >
            {isCancelling ? "Cancelling…" : "Cancel order"}
          </button>
        </div>
      )}
    </main>
  );
}

// PaymentSection stands in for a real hosted checkout page: this deployment
// runs the mock payment provider (no real Stripe/PayPal account configured
// yet), so paying "for real" means starting a payment intent and then
// telling it how the (fictitious) payment went — which exercises the same
// signature-verified webhook path a real provider's callback would.
function PaymentSection({ orderId, onSettled }: { orderId: string; onSettled: () => void }) {
  const { callWithAuth } = useAuth();
  const [intent, setIntent] = useState<api.PaymentIntent | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isBusy, setIsBusy] = useState(false);

  async function handleStartPayment() {
    setError(null);
    setIsBusy(true);
    try {
      const created = await callWithAuth((token) => api.createPaymentIntent(token, orderId));
      setIntent(created);
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not start payment.");
    } finally {
      setIsBusy(false);
    }
  }

  async function handleSimulate(outcome: "succeeded" | "failed") {
    if (!intent) return;
    setError(null);
    setIsBusy(true);
    try {
      await callWithAuth((token) =>
        api.simulatePaymentOutcome(
          token,
          intent.id,
          outcome,
          outcome === "failed" ? "insufficient_funds" : undefined,
        ),
      );
      onSettled();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not process payment.");
    } finally {
      setIsBusy(false);
    }
  }

  if (!intent) {
    return (
      <div className="flex flex-col gap-1">
        <button
          onClick={handleStartPayment}
          disabled={isBusy}
          className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
        >
          {isBusy ? "Starting…" : "Pay now"}
        </button>
        {error && <p className="text-sm text-red-600">{error}</p>}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2 rounded border border-slate-200 bg-white p-4">
      <p className="text-sm text-slate-600">
        Payment started (mock provider). This stands in for a hosted checkout page — choose an
        outcome to simulate it:
      </p>
      <div className="flex gap-2">
        <button
          onClick={() => handleSimulate("succeeded")}
          disabled={isBusy}
          className="rounded bg-emerald-600 px-3 py-1.5 text-sm text-white disabled:opacity-50"
        >
          Simulate success
        </button>
        <button
          onClick={() => handleSimulate("failed")}
          disabled={isBusy}
          className="rounded bg-red-600 px-3 py-1.5 text-sm text-white disabled:opacity-50"
        >
          Simulate failure
        </button>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
    </div>
  );
}
