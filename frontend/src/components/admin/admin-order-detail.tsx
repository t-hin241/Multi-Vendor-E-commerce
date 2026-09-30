"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { OrderStatusBadge } from "@/components/admin/status-badges";
import { SectionHeader } from "@/components/section-header";
import {
  AlertDialog,
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import { returnStatusLabel } from "@/lib/order-workflow";

// AdminOrderDetail shows everything Order recorded for one order: package
// snapshots (shipping quote, commission rule), captures Payment reported,
// refunds, returns and pending side effects. Refunds for disputes and for
// rejected (late or duplicate) captures are requested from here.
export function AdminOrderDetail({ orderId }: { orderId: string }) {
  const { callWithAuth } = useAuth();
  const orderQuery = useQuery({
    queryKey: ["admin-order", orderId],
    queryFn: () => callWithAuth((token) => api.getAdminOrder(token, orderId)),
  });

  if (orderQuery.isPending) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (orderQuery.error || !orderQuery.data) {
    return (
      <p className="text-sm text-destructive">
        {orderQuery.error instanceof api.ApiError
          ? orderQuery.error.message
          : "Could not load order."}
      </p>
    );
  }
  const order = orderQuery.data;
  const money = (amount: number) => formatMoney(amount, order.currency);

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title={`Order #${order.id.slice(0, 8)}`}
        badge={<OrderStatusBadge status={order.status} />}
        action={
          <Button variant="outline" size="sm" asChild>
            <Link href="/admin/orders">Back to orders</Link>
          </Button>
        }
      />

      <Card>
        <CardContent className="grid gap-2 text-sm sm:grid-cols-2">
          <p>Items: {money(order.subtotal_amount ?? order.total_amount)}</p>
          <p>Shipping: {money(order.shipping_amount ?? 0)}</p>
          <p className="font-medium">Total: {money(order.total_amount)}</p>
          <p>Refunded: {money(order.refunded_amount ?? 0)}</p>
          <p>Checkout: {order.checkout_state ?? "ready"}</p>
          <p>Paid at: {order.paid_at ? new Date(order.paid_at).toLocaleString() : "—"}</p>
          {order.cancellation_reason && (
            <p className="sm:col-span-2 text-destructive">Cancelled: {order.cancellation_reason}</p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Packages</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3 text-sm">
          {order.vendor_orders?.map((vo) => {
            const refundable =
              vo.subtotal_amount + vo.shipping_fee_amount - (vo.refunded_amount ?? 0);
            return (
              <div key={vo.id} className="rounded-md border p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="font-medium">
                    Package #{vo.id.slice(0, 8)} · vendor {vo.vendor_id?.slice(0, 8) ?? "—"}
                  </p>
                  <OrderStatusBadge status={vo.status} />
                </div>
                <p>
                  {money(vo.subtotal_amount)} + shipping {money(vo.shipping_fee_amount)}
                  {vo.refunded_amount ? `, refunded ${money(vo.refunded_amount)}` : ""}
                </p>
                {vo.shipping && (
                  <p className="text-xs text-muted-foreground">
                    Shipping quote: rule {vo.shipping.fee_rule_id.slice(0, 8)} v
                    {vo.shipping.fee_rule_version}, {vo.shipping.package_weight_grams} g
                  </p>
                )}
                {vo.commission ? (
                  <p className="text-xs text-muted-foreground">
                    Commission {(vo.commission.rate_bps / 100).toFixed(2)}% of{" "}
                    {money(vo.commission.base_amount)} = {money(vo.commission.amount)}
                    {vo.commission.rule_version !== undefined &&
                      ` (rule v${vo.commission.rule_version})`}
                    {vo.commission.source === "payment_time_legacy" && " · legacy, set at payment"}
                  </p>
                ) : (
                  <p className="text-xs text-muted-foreground">No commission snapshot.</p>
                )}
                {["paid", "processing", "shipped", "completed"].includes(vo.status) &&
                  refundable > 0 && (
                    <RefundDialog
                      orderId={order.id}
                      currency={order.currency}
                      max={refundable}
                      label="Refund (dispute)"
                      base={{ reason_code: "dispute", vendor_order_id: vo.id }}
                    />
                  )}
              </div>
            );
          })}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Payments reported by Payment</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-2 text-sm">
          {(order.payments ?? []).length === 0 && <p className="text-muted-foreground">None.</p>}
          {order.payments?.map((p) => (
            <div key={p.payment_id} className="flex flex-wrap items-center justify-between gap-2">
              <span>
                {money(p.amount)} · {p.outcome}
                {p.rejection_reason && ` (${p.rejection_reason.replace(/_/g, " ")})`} ·{" "}
                {new Date(p.received_at).toLocaleString()}
              </span>
              {p.outcome === "rejected" && (
                <RefundDialog
                  orderId={order.id}
                  currency={p.currency}
                  max={p.amount}
                  label="Refund this payment"
                  base={{
                    reason_code:
                      p.rejection_reason === "duplicate_payment"
                        ? "duplicate_payment"
                        : "late_payment",
                    payment_id: p.payment_id,
                  }}
                />
              )}
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Refunds</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-1 text-sm">
          {(order.refunds ?? []).length === 0 && <p className="text-muted-foreground">None.</p>}
          {order.refunds?.map((r) => (
            <p key={r.id}>
              {money(r.amount)} · {r.reason_code.replace(/_/g, " ")} · <b>{r.status}</b>
              {r.failure_reason && ` · ${r.failure_reason}`} — {r.reason}
            </p>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Returns</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-1 text-sm">
          {(order.returns ?? []).length === 0 && <p className="text-muted-foreground">None.</p>}
          {order.returns?.map((r) => (
            <p key={r.id}>
              {r.quantity} unit(s) · {money(r.refund_amount)} · {returnStatusLabel(r.status)} ·{" "}
              <Link className="underline" href="/admin/returns">
                manage
              </Link>
            </p>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Side effects</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-1 text-sm">
          {(order.effects ?? []).length === 0 && <p className="text-muted-foreground">None.</p>}
          {order.effects?.map((e) => (
            <p key={e.id}>
              {e.kind.replace(/_/g, " ")} · {e.status} · {e.attempts} attempt(s)
              {e.last_error && <span className="text-muted-foreground"> · {e.last_error}</span>}
            </p>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

function RefundDialog({
  orderId,
  currency,
  max,
  label,
  base,
}: {
  orderId: string;
  currency: string;
  max: number;
  label: string;
  base: Pick<api.OrderRefundInput, "reason_code" | "vendor_order_id" | "payment_id">;
}) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [amount, setAmount] = useState(String(max));
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const value = Math.floor(Number(amount));
  const valid = value >= 1 && value <= max && reason.trim().length > 0;

  async function submit() {
    setError(null);
    setBusy(true);
    try {
      await callWithAuth((token) =>
        api.requestOrderRefund(token, orderId, { ...base, amount: value, reason: reason.trim() }),
      );
      await queryClient.invalidateQueries({ queryKey: ["admin-order", orderId] });
      setOpen(false);
      setReason("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not request the refund.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>
        <Button size="sm" variant="outline" className="mt-2 text-destructive">
          {label}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{label}</AlertDialogTitle>
          <AlertDialogDescription>
            Payment checks the amount against the capture. Money counts as returned only after an
            operator records the provider or bank reference in Payment refunds.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex flex-col gap-2">
          <Label htmlFor="refund-amount">
            Amount ({currency}, at most {formatMoney(max, currency)})
          </Label>
          <Input
            id="refund-amount"
            type="number"
            min={1}
            max={max}
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
          />
          <Label htmlFor="refund-reason">Reason</Label>
          <Textarea
            id="refund-reason"
            rows={3}
            maxLength={500}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button variant="destructive" disabled={!valid || busy} onClick={submit}>
            {busy ? "Working…" : "Request refund"}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
