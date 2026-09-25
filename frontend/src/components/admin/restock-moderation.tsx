"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ConfirmDialog, ReasonDialog } from "@/components/admin/confirm-dialogs";
import { RestockStatusBadge } from "@/components/admin/status-badges";
import { StatusFilter } from "@/components/admin/status-filter";
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

const RESTOCK_STATUS_OPTIONS = ["pending", "approved", "rejected", ""] as const;

// RestockModeration is a vendor's "add stock" queue: a stock increase for an
// already-approved product only takes effect once approved here, mirroring
// ProductModeration's own shape.
export function RestockModeration() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("pending");

  const requestsQuery = useQuery({
    queryKey: ["admin-restock-requests", status],
    queryFn: () =>
      callWithAuth((token) => api.listRestockRequestsForAdmin(token, { status: status || undefined })),
  });

  async function handleApprove(id: string) {
    await callWithAuth((token) => api.approveRestockRequest(token, id));
    await queryClient.invalidateQueries({ queryKey: ["admin-restock-requests"] });
  }

  async function handleReject(id: string, reason: string) {
    await callWithAuth((token) => api.rejectRestockRequest(token, id, reason));
    await queryClient.invalidateQueries({ queryKey: ["admin-restock-requests"] });
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Restock requests"
        action={
          <StatusFilter status={status} onChange={setStatus} options={RESTOCK_STATUS_OPTIONS} />
        }
      />

      {requestsQuery.error && (
        <p className="text-sm text-destructive">
          Could not load restock requests:{" "}
          {requestsQuery.error instanceof api.ApiError ? requestsQuery.error.message : "unknown error"}
        </p>
      )}

      <Card>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>Requested</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {requestsQuery.data?.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="whitespace-normal">
                    Product #{r.product_id.slice(0, 8)}
                    {r.variant_id ? ` — Variant #${r.variant_id.slice(0, 8)}` : ""}
                  </TableCell>
                  <TableCell>+{r.requested_quantity}</TableCell>
                  <TableCell>
                    <RestockStatusBadge status={r.status} />
                    {r.status === "rejected" && r.rejection_reason && (
                      <p className="mt-1 text-xs text-muted-foreground">{r.rejection_reason}</p>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    {r.status === "pending" && (
                      <div className="flex justify-end gap-2">
                        <ConfirmDialog
                          trigger={
                            <Button size="sm" variant="secondary">
                              Approve
                            </Button>
                          }
                          title="Approve this restock request?"
                          description={`Adds ${r.requested_quantity} to available stock immediately.`}
                          confirmLabel="Approve"
                          onConfirm={() => handleApprove(r.id)}
                        />
                        <ReasonDialog
                          trigger={
                            <Button size="sm" variant="outline" className="text-destructive">
                              Reject
                            </Button>
                          }
                          title="Reject this restock request?"
                          description="The vendor will see this reason on the request."
                          confirmLabel="Reject"
                          onConfirm={(reason) => handleReject(r.id, reason)}
                        />
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {requestsQuery.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No restock requests for this filter.
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
