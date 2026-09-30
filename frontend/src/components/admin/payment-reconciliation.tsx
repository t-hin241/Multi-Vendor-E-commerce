"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";

const COUNT_LABELS: Record<string, string> = {
  receipts_parked: "Webhooks with no matching payment",
  receipts_rejected: "Webhooks rejected (amount/currency mismatch)",
  receipts_retryable: "Webhooks waiting to be re-applied",
  receipts_unapplied: "Webhooks received but not applied",
  intents_stuck_creating: "Payment links stuck while creating",
  intents_expired_open: "Expired links not closed yet",
  order_sync_pending: "Outcomes waiting for Order",
  order_sync_review: "Outcomes Order refused",
};

// PaymentReconciliation lists what did not reconcile between the provider,
// Payment and Order, and lets an admin retry with a recorded reason.
export function PaymentReconciliation() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");

  const overview = useQuery({
    queryKey: ["payment-reconciliation"],
    queryFn: () => callWithAuth((token) => api.getPaymentReconciliation(token)),
    refetchInterval: 30_000,
  });
  const results = useQuery({
    queryKey: ["payment-search", search],
    queryFn: () => callWithAuth((token) => api.searchPayments(token, search)),
    enabled: search.length >= 3,
  });

  async function retry(fn: (token: string) => Promise<unknown>) {
    await callWithAuth(fn);
    await queryClient.invalidateQueries({ queryKey: ["payment-reconciliation"] });
    await queryClient.invalidateQueries({ queryKey: ["payment-search"] });
  }

  const retryButton = (
    label: string,
    title: string,
    fn: (token: string, reason: string) => Promise<unknown>,
  ) => (
    <ReasonDialog
      trigger={
        <Button size="sm" variant="outline">
          {label}
        </Button>
      }
      title={title}
      description="The reason is recorded in the payment audit log."
      confirmLabel={label}
      variant="default"
      onConfirm={(reason) => retry((token) => fn(token, reason))}
    />
  );

  const receiptRows = (list: api.PaymentReceipt[], retryable: boolean) =>
    list.map((r) => (
      <div
        key={r.id}
        className="flex flex-wrap items-center justify-between gap-2 border-t py-2 text-sm"
      >
        <span>
          {r.event_type} · {formatMoney(r.amount, r.currency || "VND")} · {r.status}
          {r.outcome && ` (${r.outcome.replace(/_/g, " ")})`}
          <span className="block text-xs text-muted-foreground">
            event {r.provider_event_id} · link {r.provider_intent_id ?? "—"} ·{" "}
            {new Date(r.received_at).toLocaleString()}
            {r.last_error && ` · ${r.last_error}`}
          </span>
        </span>
        {retryable &&
          retryButton("Retry", "Apply this webhook again?", (token, reason) =>
            api.retryPaymentReceipt(token, r.id, reason),
          )}
      </div>
    ));

  const o = overview.data;
  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Payment reconciliation"
        subtitle="Provider ↔ Payment ↔ Order. Retries need a reason and are audited."
      />

      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setSearch(q.trim());
        }}
      >
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Order id, payment id, provider link id, reference or event id"
        />
        <Button type="submit" disabled={q.trim().length < 3}>
          Search
        </Button>
      </form>

      {results.data && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Results for {search}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-2 text-sm">
            {results.data.intents.map((i) => (
              <div
                key={i.id}
                className="flex flex-wrap items-center justify-between gap-2 border-t py-2"
              >
                <span>
                  Payment {i.id.slice(0, 8)} · order{" "}
                  <Link className="underline" href={`/admin/orders/${i.order_id}`}>
                    #{i.order_id.slice(0, 8)}
                  </Link>{" "}
                  · {formatMoney(i.amount, i.currency)} · <b>{i.status}</b>
                  <span className="block text-xs text-muted-foreground">
                    ref {i.provider_reference ?? "—"} · link {i.provider_intent_id || "—"}
                    {i.closed_reason && ` · closed: ${i.closed_reason}`}
                    {i.last_error && ` · ${i.last_error}`}
                  </span>
                </span>
                <span className="flex gap-2">
                  {(i.status === "creating" || i.status === "pending") &&
                    retryButton(
                      "Check with provider",
                      "Query the provider for this payment now?",
                      (token, reason) => api.reconcilePaymentIntent(token, i.id, reason),
                    )}
                  {(i.status === "captured" || i.status === "failed") &&
                    retryButton(
                      "Resend to Order",
                      "Send this outcome to Order again?",
                      (token, reason) => api.retryOrderSync(token, i.id, reason),
                    )}
                </span>
              </div>
            ))}
            {receiptRows(results.data.receipts, false)}
            {results.data.refunds.map((r) => (
              <p key={r.id} className="border-t py-2">
                Refund {r.id.slice(0, 8)} · {formatMoney(r.amount, r.currency)} · {r.status}
              </p>
            ))}
            {results.data.intents.length +
              results.data.receipts.length +
              results.data.refunds.length ===
              0 && <p>Nothing found.</p>}
          </CardContent>
        </Card>
      )}

      {overview.error && <p className="text-sm text-destructive">Could not load reconciliation.</p>}
      {o && (
        <>
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            {Object.entries(COUNT_LABELS).map(([key, label]) => (
              <Card key={key}>
                <CardContent className="py-3">
                  <p className="text-xs text-muted-foreground">{label}</p>
                  <p
                    className={`text-xl font-semibold ${(o.counts[key] ?? 0) > 0 && key !== "order_sync_pending" ? "text-destructive" : ""}`}
                  >
                    {o.counts[key] ?? 0}
                  </p>
                </CardContent>
              </Card>
            ))}
          </div>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Webhooks needing attention</CardTitle>
            </CardHeader>
            <CardContent>
              {receiptRows(o.parked_receipts, true)}
              {receiptRows(o.retryable_receipts, true)}
              {receiptRows(o.rejected_receipts, false)}
              {o.parked_receipts.length +
                o.retryable_receipts.length +
                o.rejected_receipts.length ===
                0 && <p className="text-sm text-muted-foreground">None.</p>}
              {o.rejected_receipts.length > 0 && (
                <p className="mt-2 text-xs text-muted-foreground">
                  Rejected webhooks did not change any payment. Check the amount with the provider;
                  if money arrived, refund it from the order.
                </p>
              )}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Outcomes not delivered to Order</CardTitle>
            </CardHeader>
            <CardContent>
              {[...o.order_sync, ...o.refund_sync].length === 0 && (
                <p className="text-sm text-muted-foreground">None.</p>
              )}
              {o.order_sync.map((s) => (
                <div
                  key={s.payment_intent_id}
                  className="flex flex-wrap items-center justify-between gap-2 border-t py-2 text-sm"
                >
                  <span>
                    Payment {s.payment_intent_id?.slice(0, 8)} {s.outcome} for order{" "}
                    <Link className="underline" href={`/admin/orders/${s.order_id}`}>
                      #{s.order_id.slice(0, 8)}
                    </Link>{" "}
                    · {s.attempts} attempt(s){s.requires_review && " · needs review"}
                    {s.last_error && (
                      <span className="block text-xs text-muted-foreground">{s.last_error}</span>
                    )}
                  </span>
                  {retryButton("Resend", "Send this outcome to Order again?", (token, reason) =>
                    api.retryOrderSync(token, s.payment_intent_id as string, reason),
                  )}
                </div>
              ))}
              {o.refund_sync.map((s) => (
                <div
                  key={s.payment_refund_id}
                  className="flex flex-wrap items-center justify-between gap-2 border-t py-2 text-sm"
                >
                  <span>
                    Refund {s.payment_refund_id?.slice(0, 8)} {s.status} for order #
                    {s.order_id.slice(0, 8)} · {s.attempts} attempt(s)
                    {s.requires_review && " · needs review"}
                  </span>
                  {retryButton(
                    "Resend",
                    "Send this refund outcome to Order again?",
                    (token, reason) =>
                      api.retryRefundSync(token, s.payment_refund_id as string, reason),
                  )}
                </div>
              ))}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
