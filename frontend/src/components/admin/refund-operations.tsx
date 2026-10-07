"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { StatusFilter } from "@/components/admin/status-filter";
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
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import { paymentRefundStatusLabels } from "@/lib/order-workflow";

const REFUND_STATUS_OPTIONS = ["open", "succeeded", "failed", ""] as const;

// RefundOperations is where money actually goes back. Payment holds each
// refund Order requested; an operator returns the money through the
// provider or bank, then records the reference here. Only that marks the
// refund succeeded and lets Order show it to the buyer.
export function RefundOperations({ focusID }: { focusID?: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("open");

  const refundsQuery = useQuery({
    queryKey: ["payment-refunds", status, focusID],
    queryFn: () =>
      callWithAuth((token) =>
        focusID
          ? api
              .request<api.PaymentRefund>(
                `/api/payments/admin/refunds/${encodeURIComponent(focusID)}`,
                { token },
              )
              .then((item) => [item])
          : api.listPaymentRefunds(token, { status: status || undefined, limit: 50 }),
      ),
  });
  const exceptionsQuery = useQuery({
    queryKey: ["order-payment-exceptions"],
    queryFn: () => callWithAuth((token) => api.listPaymentExceptions(token, { limit: 50 })),
  });

  async function resolve(id: string, input: Parameters<typeof api.resolvePaymentRefund>[2]) {
    await callWithAuth((token) => api.resolvePaymentRefund(token, id, input));
    await queryClient.invalidateQueries({ queryKey: ["payment-refunds"] });
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Refunds"
        subtitle="Return the money through the provider or bank, then record the reference."
        action={
          <StatusFilter status={status} onChange={setStatus} options={REFUND_STATUS_OPTIONS} />
        }
      />
      {refundsQuery.error && (
        <p className="text-sm text-destructive">
          {refundsQuery.error instanceof api.ApiError
            ? refundsQuery.error.message
            : "Could not load refunds."}
        </p>
      )}
      <Card>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Order</TableHead>
                <TableHead>Amount</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {refundsQuery.data?.map((r) => (
                <TableRow key={r.id}>
                  <TableCell>
                    <Link className="underline" href={`/admin/orders/${r.order_id}`}>
                      #{r.order_id.slice(0, 8)}
                    </Link>
                    <span className="block text-xs text-muted-foreground">
                      payment {r.payment_intent_id.slice(0, 8)}
                    </span>
                  </TableCell>
                  <TableCell>{formatMoney(r.amount, r.currency)}</TableCell>
                  <TableCell className="max-w-64 whitespace-normal text-sm">{r.reason}</TableCell>
                  <TableCell>
                    {paymentRefundStatusLabels[r.status] ?? r.status}
                    {r.evidence_reference && (
                      <span className="block text-xs text-muted-foreground">
                        ref {r.evidence_reference}
                      </span>
                    )}
                    {r.failure_reason && (
                      <span className="block text-xs text-destructive">{r.failure_reason}</span>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    {(r.status === "awaiting_provider_refund" || r.status === "pending") && (
                      <div className="flex justify-end gap-2">
                        <SucceededDialog
                          onConfirm={(input) => resolve(r.id, { outcome: "succeeded", ...input })}
                        />
                        <ReasonDialog
                          trigger={
                            <Button size="sm" variant="outline" className="text-destructive">
                              Failed
                            </Button>
                          }
                          title="Record the refund as failed?"
                          description="The amount becomes refundable again and Order is told the refund failed."
                          confirmLabel="Record failure"
                          onConfirm={(note) => resolve(r.id, { outcome: "failed", note })}
                        />
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {refundsQuery.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No refunds for this filter.
            </p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Captures that did not pay an order</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-2 text-sm">
          <p className="text-muted-foreground">
            Late, duplicate or mismatched payments with no refund yet. Open the order to refund
            them.
          </p>
          {exceptionsQuery.data?.length === 0 && <p>None.</p>}
          {exceptionsQuery.data?.map((p) => (
            <p key={p.payment_id}>
              <Link className="underline" href={`/admin/orders/${p.order_id}`}>
                #{p.order_id.slice(0, 8)}
              </Link>{" "}
              · {formatMoney(p.amount, p.currency)} · {p.rejection_reason?.replace(/_/g, " ")} ·{" "}
              {new Date(p.received_at).toLocaleString()}
            </p>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

function SucceededDialog({
  onConfirm,
}: {
  onConfirm: (input: { evidence_reference: string; note?: string }) => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [reference, setReference] = useState("");
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    setError(null);
    setBusy(true);
    try {
      await onConfirm({ evidence_reference: reference.trim(), note: note.trim() || undefined });
      setOpen(false);
      setReference("");
      setNote("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not record the refund.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>
        <Button size="sm">Succeeded</Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Record the refund as paid out?</AlertDialogTitle>
          <AlertDialogDescription>
            Only after the money left: enter the provider refund id or bank transfer reference.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex flex-col gap-2">
          <Label htmlFor="refund-reference">Provider or bank reference</Label>
          <Input
            id="refund-reference"
            maxLength={200}
            value={reference}
            onChange={(e) => setReference(e.target.value)}
          />
          <Label htmlFor="refund-note">Note (optional)</Label>
          <Textarea
            id="refund-note"
            rows={2}
            maxLength={500}
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button disabled={!reference.trim() || busy} onClick={submit}>
            {busy ? "Working…" : "Record succeeded"}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
