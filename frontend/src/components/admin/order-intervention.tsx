"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ShieldAlert } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { OrderStatusBadge } from "@/components/admin/status-badges";
import { StatusFilter } from "@/components/admin/status-filter";
import { SectionHeader } from "@/components/section-header";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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

const ORDER_STATUS_OPTIONS = [
  "",
  "pending_payment",
  "paid",
  "processing",
  "shipped",
  "completed",
  "cancelled",
  "refunded",
] as const;

const PAGE_SIZE = 20;

// OrderIntervention is admin's view across every buyer's orders. Normal
// fulfillment stays vendor-driven. Admin cancels an unpaid order here;
// returning money goes through a refund on the order's detail page, which
// Payment must confirm before the order counts as refunded.
export function OrderIntervention() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(0);

  const ordersQuery = useQuery({
    queryKey: ["admin-orders", status, page],
    queryFn: () =>
      callWithAuth((token) =>
        api.listAdminOrders(token, {
          status: status || undefined,
          limit: PAGE_SIZE + 1,
          offset: page * PAGE_SIZE,
        }),
      ),
  });
  const rows = ordersQuery.data?.slice(0, PAGE_SIZE) ?? [];
  const hasNext = (ordersQuery.data?.length ?? 0) > PAGE_SIZE;

  async function cancel(orderId: string, reason: string) {
    await callWithAuth((token) => api.adminCancelOrder(token, orderId, reason));
    await queryClient.invalidateQueries({ queryKey: ["admin-orders"] });
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Orders"
        action={
          <StatusFilter
            status={status}
            onChange={(next) => {
              setStatus(next);
              setPage(0);
            }}
            options={ORDER_STATUS_OPTIONS}
          />
        }
      />

      <Alert>
        <ShieldAlert />
        <AlertTitle>Audit &amp; intervention only</AlertTitle>
        <AlertDescription>
          Fulfillment is vendor-driven. Admin can cancel an unpaid order with a reason. Refunds are
          requested from an order&apos;s detail page and only count once Payment confirms the money
          was returned.
        </AlertDescription>
      </Alert>

      <OrderOperationsPanel />

      {ordersQuery.error && (
        <p className="text-sm text-destructive">
          Could not load orders:{" "}
          {ordersQuery.error instanceof api.ApiError ? ordersQuery.error.message : "unknown error"}
        </p>
      )}

      <Card>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Order</TableHead>
                <TableHead>Total</TableHead>
                <TableHead>Refunded</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((o) => (
                <TableRow key={o.id}>
                  <TableCell className="font-medium">
                    <Link className="underline" href={`/admin/orders/${o.id}`}>
                      #{o.id.slice(0, 8)}
                    </Link>
                  </TableCell>
                  <TableCell>{formatMoney(o.total_amount, o.currency)}</TableCell>
                  <TableCell>
                    {o.refunded_amount ? formatMoney(o.refunded_amount, o.currency) : "—"}
                  </TableCell>
                  <TableCell>
                    <OrderStatusBadge status={o.status} />
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {new Date(o.created_at).toLocaleDateString()}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">
                      <Button size="sm" variant="outline" asChild>
                        <Link href={`/admin/orders/${o.id}`}>Details</Link>
                      </Button>
                      {o.status === "pending_payment" && (
                        <ReasonDialog
                          trigger={
                            <Button size="sm" variant="outline" className="text-destructive">
                              Cancel
                            </Button>
                          }
                          title={`Cancel order #${o.id.slice(0, 8)}?`}
                          description="The buyer will see this reason on their order. Held stock is released."
                          confirmLabel="Cancel order"
                          onConfirm={(reason) => cancel(o.id, reason)}
                        />
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {ordersQuery.data && rows.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No orders for this filter.
            </p>
          )}
        </CardContent>
      </Card>
      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" disabled={page === 0} onClick={() => setPage(page - 1)}>
          Previous
        </Button>
        <Button variant="outline" size="sm" disabled={!hasNext} onClick={() => setPage(page + 1)}>
          Next
        </Button>
      </div>
    </div>
  );
}

// OrderOperationsPanel shows Order's side-effect backlog (shipments,
// stock releases, refunds, notifications) and lets admin replay the ones
// that gave up after repeated failures.
function OrderOperationsPanel() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const ops = useQuery({
    queryKey: ["admin-order-operations"],
    queryFn: () => callWithAuth((token) => api.getOrderOperations(token)),
    refetchInterval: 60_000,
  });
  if (!ops.data) return null;
  const { pending, parked, oldest_pending, parked_effects } = ops.data;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Order side effects</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-2 text-sm">
        <p>
          {pending} pending
          {oldest_pending && ` (oldest ${new Date(oldest_pending).toLocaleString()})`}, {parked}{" "}
          parked after repeated failures.
        </p>
        {parked_effects.map((effect) => (
          <div
            key={effect.id}
            className="flex flex-wrap items-center justify-between gap-2 border-t pt-2"
          >
            <span>
              <span className="font-medium">{effect.kind.replace(/_/g, " ")}</span> for order{" "}
              <Link className="underline" href={`/admin/orders/${effect.order_id}`}>
                #{effect.order_id.slice(0, 8)}
              </Link>{" "}
              after {effect.attempts} attempts
              {effect.last_error && (
                <span className="block text-xs text-muted-foreground">{effect.last_error}</span>
              )}
            </span>
            <ReasonDialog
              trigger={
                <Button size="sm" variant="outline">
                  Replay
                </Button>
              }
              title="Replay this side effect?"
              description="Only replay once the cause (for example a service outage) is fixed. Every effect is idempotent; the reason is kept in the audit."
              confirmLabel="Replay"
              variant="default"
              onConfirm={async (reason) => {
                await callWithAuth((token) => api.replayOrderEffect(token, effect.id, reason));
                await queryClient.invalidateQueries({ queryKey: ["admin-order-operations"] });
              }}
            />
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
