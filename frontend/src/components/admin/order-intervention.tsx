"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ShieldAlert } from "lucide-react";
import { useState } from "react";

import { OrderStatusBadge } from "@/components/admin/status-badges";
import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { StatusFilter } from "@/components/admin/status-filter";
import { SectionHeader } from "@/components/section-header";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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

// OrderIntervention is admin's moderation view of orders: browsing across
// every buyer, and — for a dispute or a stuck order — cancelling or
// refunding it outside the normal vendor-driven fulfillment path. Ordinary
// progress (processing/shipped/completed) stays vendor-driven; admin only
// ever moves an order to cancelled or refunded, same as the backend enforces.
export function OrderIntervention() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("");

  const ordersQuery = useQuery({
    queryKey: ["admin-orders", status],
    queryFn: () => callWithAuth((token) => api.listAdminOrders(token, { status: status || undefined })),
  });

  async function handleTransition(orderId: string, target: "cancelled" | "refunded", reason: string) {
    await callWithAuth((token) => api.adminTransitionOrder(token, orderId, target, reason));
    await queryClient.invalidateQueries({ queryKey: ["admin-orders"] });
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Orders"
        action={<StatusFilter status={status} onChange={setStatus} options={ORDER_STATUS_OPTIONS} />}
      />

      <Alert>
        <ShieldAlert />
        <AlertTitle>Audit &amp; intervention only</AlertTitle>
        <AlertDescription>
          Normal fulfillment (processing → shipped → completed) is vendor-driven and not editable
          here. Admin can only cancel a not-yet-paid order or refund a paid one — every action
          below requires a reason and is recorded against the order.
        </AlertDescription>
      </Alert>

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
                <TableHead>Status</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ordersQuery.data?.map((o) => (
                <TableRow key={o.id}>
                  <TableCell className="font-medium">#{o.id.slice(0, 8)}</TableCell>
                  <TableCell>{formatMoney(o.total_amount, o.currency)}</TableCell>
                  <TableCell>
                    <OrderStatusBadge status={o.status} />
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {new Date(o.created_at).toLocaleDateString()}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">
                      {o.status === "pending_payment" && (
                        <ReasonDialog
                          trigger={
                            <Button size="sm" variant="outline" className="text-destructive">
                              Cancel
                            </Button>
                          }
                          title={`Cancel order #${o.id.slice(0, 8)}?`}
                          description="The buyer will see this reason on their order."
                          confirmLabel="Cancel order"
                          onConfirm={(reason) => handleTransition(o.id, "cancelled", reason)}
                        />
                      )}
                      {["paid", "processing", "shipped", "completed"].includes(o.status) && (
                        <ReasonDialog
                          trigger={
                            <Button size="sm" variant="outline" className="text-destructive">
                              Refund
                            </Button>
                          }
                          title={`Refund order #${o.id.slice(0, 8)}?`}
                          description="The buyer will see this reason on their order."
                          confirmLabel="Refund order"
                          onConfirm={(reason) => handleTransition(o.id, "refunded", reason)}
                        />
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {ordersQuery.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No orders for this filter.
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
