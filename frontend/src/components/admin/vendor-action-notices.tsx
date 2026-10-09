"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { StatusFilter } from "@/components/admin/status-filter";
import { SectionHeader } from "@/components/section-header";
import { Badge } from "@/components/ui/badge";
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
import { needsReview, noticesDisabled, vendorActionLabel } from "@/lib/vendor-notices";

const STATUSES = ["no_recipient", "parked", "pending", "resolved", ""];
const PAGE = 20;

const COUNTERS: { key: string; label: string }[] = [
  { key: "vendor_actions_no_recipient", label: "No recipient" },
  { key: "vendor_actions_parked", label: "Vendor unreachable" },
  { key: "vendor_actions_pending", label: "Waiting" },
  { key: "vendor_actions_pending_over_15m", label: "Waiting 15+ min" },
  { key: "vendor_actions_resolved_24h", label: "Resolved (24h)" },
];

// VendorActionNotices (AF-08) lists shop work events by how their
// recipients were resolved. "No recipient" means nobody may receive it
// (owner locked, shop gone): follow up with the shop, then retry. Who
// received a notice is not shown, only how many.
export function VendorActionNotices() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("no_recipient");
  const [page, setPage] = useState(0);

  const summary = useQuery({
    queryKey: ["vendor-action-summary"],
    queryFn: () => callWithAuth((token) => api.getVendorActionSummary(token)),
    refetchInterval: 60_000,
  });
  const list = useQuery({
    queryKey: ["vendor-action-notices", status, page],
    queryFn: () =>
      callWithAuth((token) =>
        api.listVendorActionNotices(token, {
          status: status || undefined,
          limit: PAGE,
          offset: page * PAGE,
        }),
      ),
  });

  async function refresh() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["vendor-action-notices"] }),
      queryClient.invalidateQueries({ queryKey: ["vendor-action-summary"] }),
    ]);
  }

  // Routes exist but the feature may be off (nothing recorded yet): the
  // list is simply empty; a 404 means an older Notification build.
  if (
    noticesDisabled(list.error) ||
    (list.error instanceof api.ApiError && list.error.status === 404)
  ) {
    return null;
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Shop work notices"
        subtitle="Orders and payouts the shop must hear about. Notification asks Vendor who may receive each one; events nobody may receive wait here."
      />
      <Card>
        <CardContent className="grid gap-3 sm:grid-cols-3 lg:grid-cols-5">
          {COUNTERS.map((c) => (
            <div key={c.key}>
              <p className="text-xs text-muted-foreground">{c.label}</p>
              <p className="text-xl font-semibold">
                {summary.data ? (summary.data.counts[c.key] ?? 0) : summary.error ? "—" : "…"}
              </p>
            </div>
          ))}
        </CardContent>
      </Card>

      <StatusFilter
        options={STATUSES}
        status={status}
        onChange={(v) => {
          setStatus(v);
          setPage(0);
        }}
      />
      {list.error && (
        <p className="text-sm text-destructive">
          {list.error instanceof api.ApiError ? list.error.message : "Could not load shop notices."}
        </p>
      )}
      <Card>
        <CardContent className="overflow-x-auto p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Created</TableHead>
                <TableHead>Work</TableHead>
                <TableHead>Shop</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Recipients</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {(list.data ?? []).map((a) => (
                <TableRow key={a.id}>
                  <TableCell className="whitespace-nowrap text-xs">
                    {new Date(a.created_at).toLocaleString()}
                  </TableCell>
                  <TableCell className="text-xs">
                    {vendorActionLabel(a.action_kind)}
                    <span className="block text-muted-foreground">
                      {a.source} · {a.reference_id.slice(0, 8)}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{a.vendor_id.slice(0, 8)}</TableCell>
                  <TableCell>
                    <Badge variant={needsReview(a.status) ? "destructive" : "outline"}>
                      {a.status.replace(/_/g, " ")}
                    </Badge>
                    {a.status === "pending" && a.attempts > 0 && (
                      <span className="block text-xs text-muted-foreground">
                        next {new Date(a.next_attempt_at).toLocaleTimeString()}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-xs">{a.recipients}</TableCell>
                  <TableCell className="max-w-xs text-xs">{a.last_error ?? ""}</TableCell>
                  <TableCell>
                    {needsReview(a.status) && (
                      <ReasonDialog
                        trigger={
                          <Button size="sm" variant="outline">
                            Retry
                          </Button>
                        }
                        title="Resolve the recipients again?"
                        description="Retry after the cause is fixed (owner account unlocked, Vendor reachable). People already told are not told twice; the reason is kept in the audit."
                        confirmLabel="Retry"
                        variant="default"
                        onConfirm={async (reason) => {
                          await callWithAuth((token) =>
                            api.retryVendorActionNotice(token, a.id, reason),
                          );
                          await refresh();
                        }}
                      />
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {list.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No shop notices for this filter.
            </p>
          )}
        </CardContent>
      </Card>
      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" disabled={page === 0} onClick={() => setPage(page - 1)}>
          Previous
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={(list.data?.length ?? 0) < PAGE}
          onClick={() => setPage(page + 1)}
        >
          Next
        </Button>
      </div>
    </div>
  );
}
