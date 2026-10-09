"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { ActionError } from "@/components/admin/action-error";
import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { StatusFilter } from "@/components/admin/status-filter";
import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import {
  ADMIN_DELIVERY_LABELS,
  canClose,
  canRedeliver,
  canRefund,
  canRetryRefund,
} from "@/lib/delivery-exceptions";

const STATUS_OPTIONS = [
  "open",
  "investigating",
  "awaiting_goods",
  "awaiting_buyer",
  "needs_review",
  "refund_pending",
  "resolved",
  "",
] as const;

// DeliveryExceptionQueue is where admins resolve failed deliveries
// (AF-04): check the carrier's evidence, then offer a redelivery (the
// buyer confirms the address; no new charge) or refund. Order does each
// step: the shop records what came back, only sellable units are restocked
// after a refund, and the payout stays held until the case ends.
export function DeliveryExceptionQueue({ focusID }: { focusID?: string }) {
  const { callWithAuth } = useAuth();
  const [status, setStatus] = useState("open");
  const cases = useQuery({
    queryKey: ["admin-delivery-exceptions", status],
    queryFn: () => callWithAuth((token) => api.listDeliveryExceptions(token, status)),
  });
  const items = (cases.data ?? []).filter((d) => !focusID || d.id === focusID);

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Failed deliveries"
        subtitle="Packages that ran out of attempts, came back to the shop or were lost by the carrier."
        action={<StatusFilter status={status} onChange={setStatus} options={STATUS_OPTIONS} />}
      />
      <ActionError error={cases.error} />
      {cases.data && items.length === 0 && (
        <p className="text-sm text-muted-foreground">No cases for this filter.</p>
      )}
      {items.map((d) => (
        <DeliveryExceptionCard key={d.id} exception={d} />
      ))}
    </div>
  );
}

function DeliveryExceptionCard({ exception: listed }: { exception: api.DeliveryException }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [error, setError] = useState<unknown>(null);
  // The list omits the receipt: read the case for the decision.
  const detail = useQuery({
    queryKey: ["admin-delivery-exception", listed.id, listed.version],
    queryFn: () => callWithAuth((token) => api.getDeliveryException(token, "admin", listed.id)),
  });
  const d = detail.data?.exception ?? listed;

  async function decide(resolution: "redeliver" | "refund" | "close" | "retry_refund", reason: string) {
    setError(null);
    try {
      await callWithAuth((t) =>
        api.decideDeliveryException(t, d.id, { resolution, reason, expected_version: d.version }),
      );
    } catch (err) {
      setError(err);
    }
    await queryClient.invalidateQueries({ queryKey: ["admin-delivery-exceptions"] });
    await queryClient.invalidateQueries({ queryKey: ["admin-delivery-exception", d.id] });
  }

  return (
    <Card>
      <CardContent className="flex flex-col gap-2 text-sm">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="font-medium">
            {ADMIN_DELIVERY_LABELS[d.status] ?? d.status} · {d.exception_type.replace(/_/g, " ")}
            {d.carrier_outcome !== d.exception_type && ` → ${d.carrier_outcome.replace(/_/g, " ")}`}
          </span>
          <Link className="text-primary underline" href={`/admin/orders/${d.order_id}`}>
            Order {d.order_id.slice(0, 8)}
          </Link>
        </div>
        {d.detection_reason && <p className="text-muted-foreground">{d.detection_reason}</p>}
        <p className="text-xs text-muted-foreground">
          package {d.vendor_order_id.slice(0, 8)} · shipment {d.current_shipment_id.slice(0, 8)} (attempt{" "}
          {d.attempt_no}) · {d.failed_attempts} failed attempt(s) · opened{" "}
          {new Date(d.created_at).toLocaleString("vi-VN")}
          {d.action_due_at && ` · due ${new Date(d.action_due_at).toLocaleString("vi-VN")}`}
          {d.waiting_on && ` · waiting on ${d.waiting_on}`}
        </p>
        {d.hold_status && (
          <p className={d.hold_status === "needs_review" ? "text-destructive" : "text-muted-foreground"}>
            Payout hold: {d.hold_status}
            {d.hold_note && ` — ${d.hold_note}`}
          </p>
        )}
        {d.receipt ? (
          <p>
            Goods received (v{d.receipt.version}, {d.receipt.actor_role}):{" "}
            {d.receipt.lines.map((l) => `${l.quantity} ${l.condition}`).join(", ")}
            {d.receipt.note && ` — ${d.receipt.note}`}
          </p>
        ) : (
          d.carrier_outcome === "returned" && (
            <p className="text-muted-foreground">Waiting for the shop to record what came back.</p>
          )
        )}
        {d.redelivery_address && (
          <p className="text-xs">
            Redelivery to {d.redelivery_address.recipient_name}, {d.redelivery_address.street_address},{" "}
            {d.redelivery_address.district}, {d.redelivery_address.province}
            {d.replacement_shipment_id && ` · shipment ${d.replacement_shipment_id.slice(0, 8)}`}
          </p>
        )}
        {d.late_delivery_at && (
          <p className="text-destructive">
            Carrier reported delivered on {new Date(d.late_delivery_at).toLocaleString("vi-VN")}
          </p>
        )}
        {d.review_reason && <p className="text-destructive">{d.review_reason}</p>}
        {d.refund_id && <p className="font-mono text-xs">refund {d.refund_id.slice(0, 8)}</p>}
        {d.stock_recovered && <p className="text-xs">Sellable units go back to stock.</p>}
        {d.decision_reason && <p className="text-muted-foreground">Decision: {d.decision_reason}</p>}

        <div className="flex flex-wrap items-center gap-2">
          {d.hold_status === "preparing" && (
            <span className="text-xs text-muted-foreground">
              Waiting for Payment to confirm the payout hold…
            </span>
          )}
          {canRedeliver(d) && (
            <ReasonDialog
              variant="default"
              trigger={<Button size="sm">Offer redelivery</Button>}
              title="Offer a redelivery?"
              description="The buyer confirms the address first; no new charge. Only goods recorded sellable are sent again."
              confirmLabel="Offer"
              onConfirm={(reason) => decide("redeliver", reason)}
            />
          )}
          {canRefund(d) && (
            <ReasonDialog
              variant="default"
              trigger={<Button size="sm" variant="outline">Refund buyer</Button>}
              title="Refund this package?"
              description="Merchandise and the shipping fee paid. After a refund the package cannot be redelivered; sellable units go back to stock once the goods are recorded."
              confirmLabel="Refund"
              onConfirm={(reason) => decide("refund", reason)}
            />
          )}
          {canClose(d) && (
            <ReasonDialog
              trigger={<Button size="sm" variant="outline">Close (delivered)</Button>}
              title="Close this case?"
              description="Only when the buyer did receive the package. The payout hold is released."
              confirmLabel="Close"
              onConfirm={(reason) => decide("close", reason)}
            />
          )}
          {canRetryRefund(d) && (
            <ReasonDialog
              variant="default"
              trigger={<Button size="sm">Retry refund</Button>}
              title="Request the refund again?"
              description="The previous refund failed; the buyer is still owed it."
              confirmLabel="Retry"
              onConfirm={(reason) => decide("retry_refund", reason)}
            />
          )}
        </div>
        <ActionError error={error ?? detail.error} />
      </CardContent>
    </Card>
  );
}
