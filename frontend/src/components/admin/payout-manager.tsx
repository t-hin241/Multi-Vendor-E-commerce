"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ReasonDialog } from "@/components/admin/confirm-dialogs";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import { newIdempotencyKey } from "@/lib/order-workflow";

const ENTRY_LABELS: Record<api.SettlementEntry["entry_type"], string> = {
  sale: "Sale",
  shipping: "Shipping fee",
  commission: "Commission",
  refund: "Refund",
  commission_reversal: "Commission returned",
  payout: "Payout",
  adjustment: "Adjustment",
};

// PayoutManager shows what the marketplace owes each vendor from Payment's
// append-only ledger, and runs manual payout batches: transfer the money,
// then record the bank reference. Nothing counts as paid before that.
export function PayoutManager() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [statementFor, setStatementFor] = useState<string | null>(null);
  const [batchKey, setBatchKey] = useState<string | null>(null);
  const [batchError, setBatchError] = useState<string | null>(null);
  const [skipped, setSkipped] = useState<{ vendor_id: string; reason: string }[]>([]);
  const [openBatch, setOpenBatch] = useState<string | null>(null);

  const balances = useQuery({
    queryKey: ["settlement-balances"],
    queryFn: () => callWithAuth((token) => api.listSettlementBalances(token)),
  });
  const statement = useQuery({
    queryKey: ["settlement-entries", statementFor],
    queryFn: () =>
      callWithAuth((token) => api.listSettlementEntries(token, statementFor as string)),
    enabled: Boolean(statementFor),
  });
  const batches = useQuery({
    queryKey: ["payout-batches"],
    queryFn: () => callWithAuth((token) => api.listPayoutBatches(token)),
  });
  const batch = useQuery({
    queryKey: ["payout-batch", openBatch],
    queryFn: () => callWithAuth((token) => api.getPayoutBatch(token, openBatch as string)),
    enabled: Boolean(openBatch),
  });

  async function refresh() {
    for (const key of [
      "settlement-balances",
      "settlement-entries",
      "payout-batches",
      "payout-batch",
    ]) {
      await queryClient.invalidateQueries({ queryKey: [key] });
    }
  }

  async function createBatch() {
    // Keep the key until the batch is created: a retry after a timeout
    // returns the same batch instead of paying twice.
    const key = batchKey ?? newIdempotencyKey();
    setBatchKey(key);
    setBatchError(null);
    try {
      const res = await callWithAuth((token) =>
        api.createPayoutBatch(token, { idempotency_key: key, currency: "VND" }),
      );
      setBatchKey(null);
      setSkipped(res.skipped ?? []);
      setOpenBatch(res.batch.id);
      await refresh();
    } catch (err) {
      setBatchError(
        err instanceof api.ApiError
          ? err.message
          : "Could not create the batch; retry sends the same request.",
      );
      if (err instanceof api.ApiError && err.status < 500) setBatchKey(null);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Vendor payouts"
        subtitle="Balances come from the settlement ledger. Sales become payable after the return window and while no return or refund is open."
        action={
          <Button onClick={createBatch}>
            {batchKey ? "Retry creating batch" : "Create payout batch"}
          </Button>
        }
      />
      {batchError && <p className="text-sm text-destructive">{batchError}</p>}
      {skipped.length > 0 && (
        <p className="text-sm text-muted-foreground">
          Skipped:{" "}
          {skipped
            .map((s) => `${s.vendor_id.slice(0, 8)} (${s.reason.replace(/_/g, " ")})`)
            .join(", ")}
        </p>
      )}

      <Card>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Vendor</TableHead>
                <TableHead>Owed</TableHead>
                <TableHead>Payable now</TableHead>
                <TableHead>In payout</TableHead>
                <TableHead>Refunded</TableHead>
                <TableHead>Paid out</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {balances.data?.map((b) => (
                <TableRow key={b.vendor_id}>
                  <TableCell className="font-mono text-xs">{b.vendor_id.slice(0, 8)}</TableCell>
                  <TableCell>{formatMoney(b.owed, b.currency)}</TableCell>
                  <TableCell>{formatMoney(b.eligible, b.currency)}</TableCell>
                  <TableCell>{formatMoney(b.in_payout, b.currency)}</TableCell>
                  <TableCell>{formatMoney(b.refunded, b.currency)}</TableCell>
                  <TableCell>{formatMoney(b.paid_out, b.currency)}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => setStatementFor(b.vendor_id)}
                      >
                        Statement
                      </Button>
                      <AdjustmentDialog vendorId={b.vendor_id} onDone={refresh} />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {balances.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">No settled orders yet.</p>
          )}
        </CardContent>
      </Card>

      {statementFor && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center justify-between text-base">
              Statement of vendor {statementFor.slice(0, 8)}
              <Button size="sm" variant="ghost" onClick={() => setStatementFor(null)}>
                Close
              </Button>
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-1 text-sm">
            {statement.data?.map((e) => (
              <p key={e.id}>
                {new Date(e.created_at).toLocaleDateString()} ·{" "}
                {ENTRY_LABELS[e.entry_type] ?? e.entry_type} ·{" "}
                <b className={e.amount < 0 ? "text-destructive" : ""}>
                  {formatMoney(e.amount, e.currency)}
                </b>
                {e.vendor_order_id && ` · package ${e.vendor_order_id.slice(0, 8)}`}
                {e.amount > 0 && ` · payable from ${new Date(e.eligible_at).toLocaleDateString()}`}
                {e.note && <span className="text-muted-foreground"> · {e.note}</span>}
              </p>
            ))}
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Payout batches</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-2 text-sm">
          {batches.data?.length === 0 && <p className="text-muted-foreground">No batches yet.</p>}
          {batches.data?.map((b) => (
            <div key={b.id} className="border-t pt-2">
              <div className="flex items-center justify-between">
                <span>
                  Batch {b.id.slice(0, 8)} · {b.status} · {new Date(b.created_at).toLocaleString()}
                </span>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setOpenBatch(openBatch === b.id ? null : b.id)}
                >
                  {openBatch === b.id ? "Hide" : "Items"}
                </Button>
              </div>
              {openBatch === b.id &&
                batch.data?.items?.map((item) => (
                  <div
                    key={item.id}
                    className="ml-4 flex flex-wrap items-center justify-between gap-2 border-t py-2"
                  >
                    <span>
                      Vendor {item.vendor_id.slice(0, 8)} ·{" "}
                      {formatMoney(item.amount, item.currency)} · to {item.destination_mask}{" "}
                      (account {item.destination_account_id.slice(0, 8)} v{item.destination_version}
                      ) · <b>{item.status}</b>
                      {item.evidence_reference && ` · ref ${item.evidence_reference}`}
                      {item.failure_reason && ` · ${item.failure_reason}`}
                    </span>
                    {item.status === "pending" && (
                      <span className="flex gap-2">
                        <PaidDialog itemId={item.id} onDone={refresh} />
                        <ReasonDialog
                          trigger={
                            <Button size="sm" variant="outline" className="text-destructive">
                              Failed
                            </Button>
                          }
                          title="Record the transfer as failed?"
                          description="The amount becomes payable again in the next batch."
                          confirmLabel="Record failure"
                          onConfirm={async (note) => {
                            await callWithAuth((token) =>
                              api.resolvePayoutItem(token, item.id, { outcome: "failed", note }),
                            );
                            await refresh();
                          }}
                        />
                      </span>
                    )}
                  </div>
                ))}
            </div>
          ))}
          <p className="text-xs text-muted-foreground">
            The full account number is shown only in the vendor&apos;s payout accounts (admin view,
            audited).
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

function PaidDialog({ itemId, onDone }: { itemId: string; onDone: () => Promise<void> }) {
  const { callWithAuth } = useAuth();
  const [open, setOpen] = useState(false);
  const [reference, setReference] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>
        <Button size="sm">Paid</Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Record the transfer as paid?</AlertDialogTitle>
          <AlertDialogDescription>
            Only after the money left: enter the bank transfer reference.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Label htmlFor="payout-reference">Bank reference</Label>
        <Input
          id="payout-reference"
          maxLength={200}
          value={reference}
          onChange={(e) => setReference(e.target.value)}
        />
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button
            disabled={!reference.trim() || busy}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try {
                await callWithAuth((token) =>
                  api.resolvePayoutItem(token, itemId, {
                    outcome: "succeeded",
                    evidence_reference: reference.trim(),
                  }),
                );
                setOpen(false);
                await onDone();
              } catch (err) {
                setError(err instanceof Error ? err.message : "Could not record the payout.");
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? "Working…" : "Record paid"}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function AdjustmentDialog({ vendorId, onDone }: { vendorId: string; onDone: () => Promise<void> }) {
  const { callWithAuth } = useAuth();
  const [open, setOpen] = useState(false);
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const value = Math.trunc(Number(amount));
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>
        <Button size="sm" variant="outline">
          Adjust
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Post a ledger adjustment</AlertDialogTitle>
          <AlertDialogDescription>
            Positive credits the vendor, negative debits. The ledger is append-only; an adjustment
            is the only correction.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Label htmlFor="adjust-amount">Amount (VND, signed)</Label>
        <Input
          id="adjust-amount"
          type="number"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
        />
        <Label htmlFor="adjust-reason">Reason</Label>
        <Input
          id="adjust-reason"
          maxLength={500}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button
            disabled={!value || !reason.trim()}
            onClick={async () => {
              setError(null);
              try {
                await callWithAuth((token) =>
                  api.createSettlementAdjustment(token, {
                    vendor_id: vendorId,
                    amount: value,
                    currency: "VND",
                    reason: reason.trim(),
                  }),
                );
                setOpen(false);
                setAmount("");
                setReason("");
                await onDone();
              } catch (err) {
                setError(err instanceof Error ? err.message : "Could not post the adjustment.");
              }
            }}
          >
            Post adjustment
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
