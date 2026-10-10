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
import { ADMIN_CANCELLATION_LABELS } from "@/lib/cancellations";

const STATUS_OPTIONS = [
  "open",
  "needs_review",
  "refund_pending",
  "resolved",
  "rejected",
  "transferred",
  "",
] as const;

// CancellationQueue is where admins decide paid-package cancellations
// (AF-03). Approving stops the shipment, puts the stock back (when it
// never left) and asks Payment for the refund; Order does each step and
// shows where it stands.
export function CancellationQueue({ focusID }: { focusID?: string }) {
  const { callWithAuth } = useAuth();
  const [status, setStatus] = useState("open");
  const requests = useQuery({
    queryKey: ["admin-cancellations", status],
    queryFn: () => callWithAuth((token) => api.listCancellations(token, status)),
  });
  const items = (requests.data ?? []).filter((r) => !focusID || r.id === focusID);

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Paid cancellations"
        subtitle="Buyers asking to cancel a paid package before handover, and shops that cannot fulfil one."
        action={<StatusFilter status={status} onChange={setStatus} options={STATUS_OPTIONS} />}
      />
      <ActionError error={requests.error} />
      {requests.data && items.length === 0 && (
        <p className="text-sm text-muted-foreground">No requests for this filter.</p>
      )}
      {items.map((r) => (
        <CancellationCard key={r.id} request={r} />
      ))}
    </div>
  );
}

function CancellationCard({ request: r }: { request: api.CancellationRequest }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [error, setError] = useState<unknown>(null);
  const [restock, setRestock] = useState(r.origin === "buyer");

  async function decide(
    decision: "approve" | "reject" | "retry_refund" | "intercept",
    reason: string,
  ) {
    setError(null);
    try {
      await callWithAuth((t) =>
        api.decideCancellation(t, r.id, {
          decision,
          reason,
          expected_version: r.version,
          restock: decision === "approve" ? restock : undefined,
        }),
      );
    } catch (err) {
      setError(err);
    }
    await queryClient.invalidateQueries({ queryKey: ["admin-cancellations"] });
  }

  return (
    <Card>
      <CardContent className="flex flex-col gap-2 text-sm">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="font-medium">
            {ADMIN_CANCELLATION_LABELS[r.status] ?? r.status} · {r.origin} · {r.reason_code}
          </span>
          <Link className="text-primary underline" href={`/admin/orders/${r.order_id}`}>
            Order {r.order_id.slice(0, 8)}
          </Link>
        </div>
        <p className="whitespace-pre-wrap text-muted-foreground">{r.reason}</p>
        <p className="text-xs text-muted-foreground">
          package {r.vendor_order_id.slice(0, 8)} · vendor {r.vendor_id.slice(0, 8)} · opened{" "}
          {new Date(r.created_at).toLocaleString("vi-VN")}
          {r.action_due_at && ` · due ${new Date(r.action_due_at).toLocaleString("vi-VN")}`}
        </p>
        {r.hold_status && (
          <p
            className={
              r.hold_status === "needs_review" ? "text-destructive" : "text-muted-foreground"
            }
          >
            Payout hold: {r.hold_status}
            {r.hold_note && ` — ${r.hold_note}`}
          </p>
        )}
        {r.stop_result && <p>Shipment: {r.stop_result.replace(/_/g, " ")}</p>}
        {r.review_reason && <p className="text-destructive">{r.review_reason}</p>}
        {r.delivery_exception_id && (
          <p>
            Continued as{" "}
            <a
              className="underline"
              href={`/admin/delivery-exceptions?exception_id=${r.delivery_exception_id}`}
            >
              failed-delivery case {r.delivery_exception_id.slice(0, 8)}
            </a>
          </p>
        )}
        {r.refund_id && <p className="font-mono text-xs">refund {r.refund_id.slice(0, 8)}</p>}
        {r.decision_reason && (
          <p className="text-muted-foreground">Decision: {r.decision_reason}</p>
        )}

        <div className="flex flex-wrap items-center gap-2">
          {r.status === "requested" && (
            <>
              <label className="flex items-center gap-1 text-xs">
                <input
                  type="checkbox"
                  checked={restock}
                  onChange={(e) => setRestock(e.target.checked)}
                />
                Units never left the warehouse: put them back in stock
              </label>
              <ReasonDialog
                variant="default"
                trigger={<Button size="sm">Approve</Button>}
                title="Approve this cancellation?"
                description="Order stops the shipment first; if the package already left, the request comes back for review instead."
                confirmLabel="Approve"
                onConfirm={(reason) => decide("approve", reason)}
              />
            </>
          )}
          {r.status === "preparing" && (
            <span className="text-xs text-muted-foreground">
              Waiting for Payment to confirm the payout hold…
            </span>
          )}
          {(r.status === "preparing" ||
            r.status === "requested" ||
            (r.status === "needs_review" && !r.refund_id)) && (
            <ReasonDialog
              trigger={
                <Button size="sm" variant="outline" className="text-destructive">
                  Reject
                </Button>
              }
              title="Reject this cancellation?"
              description="The buyer reads the reason; the package may ship again."
              confirmLabel="Reject"
              onConfirm={(reason) => decide("reject", reason)}
            />
          )}
          {r.status === "needs_review" &&
            r.stop_result === "handed_over" &&
            !r.refund_id &&
            !r.interception_requested_at && (
              <ReasonDialog
                variant="default"
                trigger={<Button size="sm">Intercept parcel</Button>}
                title="Ask the carrier to stop this package?"
                description="If the carrier stops it, the package comes back as a failed-delivery case: the shop records the goods and the refund is decided there."
                confirmLabel="Intercept"
                onConfirm={(reason) => decide("intercept", reason)}
              />
            )}
          {r.status === "needs_review" && r.interception_requested_at && (
            <span className="text-xs text-muted-foreground">
              Interception requested; waiting for the carrier.
            </span>
          )}
          {r.status === "needs_review" && r.refund_id && (
            <ReasonDialog
              variant="default"
              trigger={<Button size="sm">Retry refund</Button>}
              title="Request the refund again?"
              description="The stock already went back; the buyer is owed the refund."
              confirmLabel="Retry"
              onConfirm={(reason) => decide("retry_refund", reason)}
            />
          )}
        </div>
        <ActionError error={error} />
      </CardContent>
    </Card>
  );
}
