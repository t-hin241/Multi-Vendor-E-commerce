"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { ConfirmDialog, ReasonDialog } from "@/components/admin/confirm-dialogs";
import { StatusFilter } from "@/components/admin/status-filter";
import { ReceiveReturnDialog } from "@/components/orders/receive-return-dialog";
import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
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
import {
  adminCanDecide,
  adminCanRetryRefund,
  canReceive,
  returnStatusLabel,
} from "@/lib/order-workflow";

const RETURN_STATUS_OPTIONS = [
  "",
  "requested",
  "vendor_confirmed",
  "approved",
  "received",
  "refund_pending",
  "refund_failed",
  "refunded",
  "rejected",
] as const;

// ReturnModeration is admin's decision queue for returns. Approving lets
// the buyer send the goods back; the refund only starts once the goods are
// received, and every step is recorded in the return's history.
export function ReturnModeration() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("vendor_confirmed");
  const [historyFor, setHistoryFor] = useState<string | null>(null);

  const returnsQuery = useQuery({
    queryKey: ["admin-returns", status],
    queryFn: () =>
      callWithAuth((token) =>
        api.listAdminReturns(token, { status: status || undefined, limit: 50 }),
      ),
  });
  const historyQuery = useQuery({
    queryKey: ["admin-return-history", historyFor],
    queryFn: () => callWithAuth((token) => api.getReturnHistory(token, historyFor as string)),
    enabled: Boolean(historyFor),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["admin-returns"] });
    await queryClient.invalidateQueries({ queryKey: ["admin-return-history"] });
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Returns"
        subtitle="Approve or reject returns, record received goods and retry failed refunds."
        action={
          <StatusFilter status={status} onChange={setStatus} options={RETURN_STATUS_OPTIONS} />
        }
      />
      {returnsQuery.error && (
        <p className="text-sm text-destructive">
          {returnsQuery.error instanceof api.ApiError
            ? returnsQuery.error.message
            : "Could not load returns."}
        </p>
      )}
      <Card>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Order</TableHead>
                <TableHead>Qty</TableHead>
                <TableHead>Refund</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {returnsQuery.data?.map((r) => (
                <TableRow key={r.id}>
                  <TableCell>
                    <Link className="underline" href={`/admin/orders/${r.order_id}`}>
                      #{r.order_id.slice(0, 8)}
                    </Link>
                    <span className="block text-xs text-muted-foreground">
                      policy {r.policy_version}
                    </span>
                  </TableCell>
                  <TableCell>{r.quantity}</TableCell>
                  <TableCell>{formatMoney(r.refund_amount)}</TableCell>
                  <TableCell className="max-w-64 whitespace-normal text-sm">
                    {r.reason}
                    {r.evidence && (
                      <span className="block text-xs text-muted-foreground">
                        Evidence: {r.evidence}
                      </span>
                    )}
                    {r.vendor_note && (
                      <span className="block text-xs text-muted-foreground">
                        Vendor: {r.vendor_note}
                      </span>
                    )}
                  </TableCell>
                  <TableCell>{returnStatusLabel(r.status)}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex flex-wrap justify-end gap-2">
                      {adminCanDecide(r) && (
                        <>
                          <ConfirmDialog
                            trigger={<Button size="sm">Approve</Button>}
                            title="Approve this return?"
                            description="The buyer may send the goods back. No money moves until they are received."
                            confirmLabel="Approve"
                            onConfirm={async () => {
                              await callWithAuth((token) =>
                                api.decideReturn(token, r.id, true, ""),
                              );
                              await refresh();
                            }}
                          />
                          <ReasonDialog
                            trigger={
                              <Button size="sm" variant="outline" className="text-destructive">
                                Reject
                              </Button>
                            }
                            title="Reject this return?"
                            description="The buyer sees this reason."
                            confirmLabel="Reject"
                            onConfirm={async (note) => {
                              await callWithAuth((token) =>
                                api.decideReturn(token, r.id, false, note),
                              );
                              await refresh();
                            }}
                          />
                        </>
                      )}
                      {canReceive(r) && (
                        <ReceiveReturnDialog
                          onConfirm={async (input) => {
                            await callWithAuth((token) =>
                              api.receiveReturn(token, "admin", r.id, input),
                            );
                            await refresh();
                          }}
                        />
                      )}
                      {adminCanRetryRefund(r) && (
                        <ConfirmDialog
                          trigger={
                            <Button size="sm" variant="outline">
                              Retry refund
                            </Button>
                          }
                          title="Request the refund again?"
                          description="Use after the reason for the failed refund was fixed."
                          confirmLabel="Retry"
                          onConfirm={async () => {
                            await callWithAuth((token) => api.retryReturnRefund(token, r.id));
                            await refresh();
                          }}
                        />
                      )}
                      <Button size="sm" variant="ghost" onClick={() => setHistoryFor(r.id)}>
                        History
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {returnsQuery.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No returns for this filter.
            </p>
          )}
        </CardContent>
      </Card>
      {historyFor && (
        <Card>
          <CardContent className="flex flex-col gap-1 text-sm">
            <div className="flex items-center justify-between">
              <p className="font-medium">History of return #{historyFor.slice(0, 8)}</p>
              <Button size="sm" variant="ghost" onClick={() => setHistoryFor(null)}>
                Close
              </Button>
            </div>
            {historyQuery.data?.map((e, i) => (
              <p key={i}>
                {new Date(e.created_at).toLocaleString()} · {e.actor_role} · {e.action}:{" "}
                {e.from_status ?? "—"} → {e.to_status}
                {e.note && <span className="text-muted-foreground"> · {e.note}</span>}
              </p>
            ))}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
