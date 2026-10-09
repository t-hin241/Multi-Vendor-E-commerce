"use client";

import { useState } from "react";

import { ActionError } from "@/components/admin/action-error";
import { ConfirmDialog, ReasonDialog } from "@/components/admin/confirm-dialogs";
import { ReturnReceiptForm } from "@/components/orders/return-receipt-form";
import { Button } from "@/components/ui/button";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { adminCanDecide } from "@/lib/order-workflow";
import {
  ADMIN_SHIPPING_LABELS,
  canAuthorizeShipping,
  canRecordReceipt,
} from "@/lib/return-shipping";

// ReturnShippingInfo is the way back at a glance (AF-05).
export function ReturnShippingInfo({ returnRequest: r }: { returnRequest: api.ReturnRequest }) {
  if (!r.shipping_status) return null;
  return (
    <span className="block text-xs text-muted-foreground">
      {ADMIN_SHIPPING_LABELS[r.shipping_status] ?? r.shipping_status}
      {r.fee_payer && ` · fee: ${r.fee_payer}`}
      {r.destination_province && ` · to ${r.destination_province}`}
      {r.dispatch_deadline &&
        r.shipping_status === "awaiting_dispatch" &&
        ` · due ${new Date(r.dispatch_deadline).toLocaleDateString("vi-VN")}`}
      {r.dispatch_overdue && <span className="text-destructive"> · overdue</span>}
      {r.dispatch_tracking && ` · ${r.dispatch_carrier} ${r.dispatch_tracking}`}
      {r.inspection_disputed && <span className="text-destructive"> · damaged/missing goods</span>}
    </span>
  );
}

// ReturnShippingActions: approve with who pays the way back, (re)authorize
// the instructions before dispatch, record the goods, refund a disputed
// inspection or mark a parcel lost.
export function ReturnShippingActions({
  returnRequest: r,
  onDone,
}: {
  returnRequest: api.ReturnRequest;
  onDone: () => Promise<void>;
}) {
  const { callWithAuth } = useAuth();
  const [feePayer, setFeePayer] = useState<"seller" | "buyer">(r.fee_payer ?? "seller");
  const [error, setError] = useState<unknown>(null);

  async function run(fn: (token: string) => Promise<unknown>) {
    setError(null);
    try {
      await callWithAuth(fn);
    } catch (err) {
      setError(err);
    }
    await onDone();
  }

  const payer = (adminCanDecide(r) || canAuthorizeShipping(r)) && (
    <select
      aria-label="Return fee payer"
      className="h-8 rounded-md border bg-background px-2 text-xs"
      value={feePayer}
      onChange={(e) => setFeePayer(e.target.value as "seller" | "buyer")}
    >
      <option value="seller">Seller pays return shipping</option>
      <option value="buyer">Buyer pays (change of mind)</option>
    </select>
  );

  return (
    <>
      {payer}
      {adminCanDecide(r) && (
        <ConfirmDialog
          trigger={<Button size="sm">Approve</Button>}
          title="Approve this return?"
          description="The buyer gets the shop's verified return address and a deadline. No money moves until the goods are received."
          confirmLabel="Approve"
          onConfirm={() =>
            run((token) => api.decideReturn(token, r.id, true, "", { fee_payer: feePayer }))
          }
        />
      )}
      {canAuthorizeShipping(r) && (
        <ReasonDialog
          variant="default"
          trigger={
            <Button size="sm" variant="outline">
              {r.authorization_version > 0 ? "Re-issue instructions" : "Issue instructions"}
            </Button>
          }
          title="Issue the return instructions?"
          description="Uses the shop's current verified return address. Only possible before the buyer sends the parcel."
          confirmLabel="Issue"
          onConfirm={(reason) =>
            run((token) =>
              api.authorizeReturnShipping(token, r.id, {
                fee_payer: feePayer,
                reason,
                expected_version: r.version,
              }),
            )
          }
        />
      )}
      {canRecordReceipt(r) && <ReturnReceiptForm returnRequest={r} scope="admin" onDone={onDone} />}
      {r.status === "received" && r.inspection_disputed && (
        <ReasonDialog
          variant="default"
          trigger={<Button size="sm">Refund in full</Button>}
          title="Refund this return in full?"
          description="Some goods came back damaged or missing. Refund anyway, or leave it and handle the dispute in a support case."
          confirmLabel="Refund"
          onConfirm={(reason) =>
            run((token) =>
              api.decideReturnShipping(token, r.id, {
                action: "refund",
                reason,
                expected_version: r.version,
              }),
            )
          }
        />
      )}
      {r.status === "approved" && r.shipping_status === "awaiting_verification" && (
        <ReasonDialog
          trigger={
            <Button size="sm" variant="outline" className="text-destructive">
              Parcel lost
            </Button>
          }
          title="Mark the return parcel lost?"
          description="After checking with the carrier (note its reference). The refund then goes through a support case."
          confirmLabel="Mark lost"
          onConfirm={(reason) =>
            run((token) =>
              api.decideReturnShipping(token, r.id, {
                action: "mark_lost",
                reason,
                expected_version: r.version,
              }),
            )
          }
        />
      )}
      <ActionError error={error} />
    </>
  );
}
