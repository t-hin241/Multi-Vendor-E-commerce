"use client";

import { useState } from "react";

import { ActionError } from "@/components/admin/action-error";
import { ConfirmDialog, ReasonDialog } from "@/components/admin/confirm-dialogs";
import { ReturnReceiptForm } from "@/components/orders/return-receipt-form";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ReceiptEvidence } from "@/components/orders/receipt-evidence";
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
// the instructions before dispatch, record the goods, correct a disputed
// receipt, refund a disputed inspection, mark a parcel lost and refund it.
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
      {(r.status === "received" || r.shipping_status === "lost") && (
        <ReceiptEvidence scope="admin" kind="return" refId={r.id} />
      )}
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
          description="After checking with the carrier (note its reference). Then refund the lost parcel here."
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
      {r.status === "approved" && r.shipping_status === "lost" && (
        <ReasonDialog
          variant="default"
          trigger={<Button size="sm">Refund lost parcel</Button>}
          title="Refund the lost return parcel?"
          description="The goods never reached the shop. The buyer is refunded the return amount; nothing is restocked."
          confirmLabel="Refund"
          onConfirm={(reason) =>
            run((token) =>
              api.decideReturnShipping(token, r.id, {
                action: "refund_lost",
                reason,
                expected_version: r.version,
              }),
            )
          }
        />
      )}
      {r.status === "received" && r.inspection_disputed && (
        <ReceiptCorrection returnRequest={r} onDone={onDone} />
      )}
      <ActionError error={error} />
    </>
  );
}

// ReceiptCorrection records a new receipt version (PW-042): every unit is
// sellable, damaged or missing; a note says why it changed.
function ReceiptCorrection({
  returnRequest: r,
  onDone,
}: {
  returnRequest: api.ReturnRequest;
  onDone: () => Promise<void>;
}) {
  const { callWithAuth } = useAuth();
  const [open, setOpen] = useState(false);
  const [counts, setCounts] = useState({ sellable: r.quantity, damaged: 0, missing: 0 });
  const [note, setNote] = useState("");
  const [error, setError] = useState<unknown>(null);
  const total = counts.sellable + counts.damaged + counts.missing;

  if (!open) {
    return (
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        Correct receipt
      </Button>
    );
  }
  const field = (key: keyof typeof counts, label: string) => (
    <label className="flex flex-col gap-1 text-xs">
      {label}
      <Input
        type="number"
        min={0}
        max={r.quantity}
        className="h-8 w-20"
        value={counts[key]}
        onChange={(e) => setCounts({ ...counts, [key]: Math.max(0, Number(e.target.value) || 0) })}
      />
    </label>
  );
  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={async (e) => {
        e.preventDefault();
        setError(null);
        try {
          await callWithAuth((token) =>
            api.correctReturnReceipt(token, r.id, {
              sellable_quantity: counts.sellable,
              damaged_quantity: counts.damaged,
              missing_quantity: counts.missing,
              note: note.trim(),
              expected_version: r.version,
            }),
          );
          setOpen(false);
          await onDone();
        } catch (err) {
          setError(err);
        }
      }}
    >
      {field("sellable", "Sellable")}
      {field("damaged", "Damaged")}
      {field("missing", "Missing")}
      <label className="flex flex-col gap-1 text-xs">
        Why
        <Input
          className="h-8 w-56"
          maxLength={500}
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      </label>
      <Button type="submit" size="sm" disabled={total !== r.quantity || !note.trim()}>
        Save receipt
      </Button>
      <Button type="button" size="sm" variant="ghost" onClick={() => setOpen(false)}>
        Cancel
      </Button>
      {total !== r.quantity && (
        <p className="w-full text-xs text-destructive">Counts must add up to {r.quantity}.</p>
      )}
      <ActionError error={error} />
    </form>
  );
}
