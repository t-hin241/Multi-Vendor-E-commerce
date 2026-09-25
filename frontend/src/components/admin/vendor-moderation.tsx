"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { AuditLogPanel } from "@/components/admin/audit-log-panel";
import { ConfirmDialog, ReasonDialog } from "@/components/admin/confirm-dialogs";
import { StatusFilter } from "@/components/admin/status-filter";
import { VendorStatusBadge } from "@/components/admin/status-badges";
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

const VENDOR_STATUS_OPTIONS = ["pending", "approved", "rejected", ""] as const;

export function VendorModeration() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("pending");

  const vendorsQuery = useQuery({
    queryKey: ["admin-vendor-applications", status],
    queryFn: () =>
      callWithAuth((token) => api.listVendorApplications(token, { status: status || undefined })),
  });

  // Errors are left to propagate -- ConfirmDialog/ReasonDialog catch them
  // and show `err.message` inline (ApiError extends Error), no separate
  // page-level error state needed for these actions.
  async function handleApprove(id: string) {
    await callWithAuth((token) => api.approveVendor(token, id));
    await queryClient.invalidateQueries({ queryKey: ["admin-vendor-applications"] });
  }

  async function handleReject(id: string, reason: string) {
    await callWithAuth((token) => api.rejectVendor(token, id, reason));
    await queryClient.invalidateQueries({ queryKey: ["admin-vendor-applications"] });
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Vendor applications"
        action={<StatusFilter status={status} onChange={setStatus} options={VENDOR_STATUS_OPTIONS} />}
      />

      {vendorsQuery.error && (
        <p className="text-sm text-destructive">
          Could not load vendor applications:{" "}
          {vendorsQuery.error instanceof api.ApiError ? vendorsQuery.error.message : "unknown error"}
        </p>
      )}

      <Card>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Shop</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {vendorsQuery.data?.map((v) => (
                <TableRow key={v.id}>
                  <TableCell className="font-medium whitespace-normal">{v.shop_name}</TableCell>
                  <TableCell className="max-w-xs whitespace-normal text-muted-foreground">
                    {v.description || "—"}
                  </TableCell>
                  <TableCell>
                    <VendorStatusBadge status={v.status} />
                    {v.status === "rejected" && v.rejection_reason && (
                      <p className="mt-1 text-xs text-muted-foreground">{v.rejection_reason}</p>
                    )}
                    <AuditLogPanel
                      targetId={v.id}
                      fetchLog={(token) => api.getVendorAuditLog(token, v.id)}
                    />
                  </TableCell>
                  <TableCell className="text-right">
                    {v.status === "pending" && (
                      <div className="flex justify-end gap-2">
                        <ConfirmDialog
                          trigger={
                            <Button size="sm" variant="secondary">
                              Approve
                            </Button>
                          }
                          title={`Approve "${v.shop_name}"?`}
                          description="This shop becomes able to list products on the storefront immediately."
                          confirmLabel="Approve"
                          onConfirm={() => handleApprove(v.id)}
                        />
                        <ReasonDialog
                          trigger={
                            <Button size="sm" variant="outline" className="text-destructive">
                              Reject
                            </Button>
                          }
                          title={`Reject "${v.shop_name}"?`}
                          description="The applicant will see this reason on their application."
                          confirmLabel="Reject"
                          onConfirm={(reason) => handleReject(v.id, reason)}
                        />
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {vendorsQuery.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No vendor applications for this filter.
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
