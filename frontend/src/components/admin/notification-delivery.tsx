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

const STATUSES = ["", "pending", "sending", "sent", "failed", "parked"];
const PAGE = 20;

const COUNTERS: { key: string; label: string }[] = [
  { key: "pending", label: "Waiting" },
  { key: "pending_over_15m", label: "Waiting 15+ min" },
  { key: "parked", label: "Stopped after retries" },
  { key: "failed_24h", label: "Refused (24h)" },
  { key: "sent_24h", label: "Sent (24h)" },
  { key: "delivery_p95_seconds_24h", label: "p95 delivery (s)" },
];

// NotificationDelivery shows what Notification is sending: status per
// email with its attempts, and an audited retry for failed or parked ones.
// Recipients are masked by the server.
export function NotificationDelivery() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(0);
  const [open, setOpen] = useState<string | null>(null);

  const ops = useQuery({
    queryKey: ["notification-operations"],
    queryFn: () => callWithAuth((token) => api.getNotificationOperations(token)),
    refetchInterval: 60_000,
  });
  const list = useQuery({
    queryKey: ["admin-notifications", status, page],
    queryFn: () =>
      callWithAuth((token) =>
        api.listAdminNotifications(token, {
          status: status || undefined,
          limit: PAGE,
          offset: page * PAGE,
        }),
      ),
  });
  const attempts = useQuery({
    queryKey: ["notification-attempts", open],
    queryFn: () => callWithAuth((token) => api.listNotificationAttempts(token, open as string)),
    enabled: open !== null,
  });

  async function refresh() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["admin-notifications"] }),
      queryClient.invalidateQueries({ queryKey: ["notification-operations"] }),
      queryClient.invalidateQueries({ queryKey: ["notification-attempts"] }),
    ]);
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Notifications"
        subtitle="Emails are recorded first and sent by background jobs; failures retry on their own and stop after the last attempt."
      />
      <Card>
        <CardContent className="grid gap-3 sm:grid-cols-3 lg:grid-cols-6">
          {COUNTERS.map((c) => (
            <div key={c.key}>
              <p className="text-xs text-muted-foreground">{c.label}</p>
              <p className="text-xl font-semibold">
                {ops.data ? (ops.data.counts[c.key] ?? 0) : ops.error ? "—" : "…"}
              </p>
            </div>
          ))}
          {ops.error && (
            <p className="text-sm text-destructive sm:col-span-3 lg:col-span-6">
              Delivery counters unavailable.
            </p>
          )}
          {(ops.data?.counts.queue_unreachable ?? 0) > 0 && (
            <p className="text-sm text-destructive sm:col-span-3 lg:col-span-6">
              The delivery queue (Redis) is unreachable. Requests are still recorded and are queued
              again from the database once Redis is back.
            </p>
          )}
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
          {list.error instanceof api.ApiError
            ? list.error.message
            : "Could not load notifications."}
        </p>
      )}
      <Card>
        <CardContent className="overflow-x-auto p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Created</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Recipient</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Attempts</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {(list.data ?? []).map((n) => (
                <TableRow key={n.id}>
                  <TableCell className="whitespace-nowrap text-xs">
                    {new Date(n.created_at).toLocaleString()}
                  </TableCell>
                  <TableCell className="text-xs">
                    {n.type.replace(/_/g, " ")}
                    <span className="block text-muted-foreground">
                      {n.source} · {n.reference_id.slice(0, 8)}
                    </span>
                  </TableCell>
                  <TableCell className="text-xs">{n.recipient ?? "—"}</TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        n.status === "failed" || n.status === "parked" ? "destructive" : "outline"
                      }
                    >
                      {n.status}
                    </Badge>
                    {n.status === "pending" && n.attempts > 0 && (
                      <span className="block text-xs text-muted-foreground">
                        next {new Date(n.next_attempt_at).toLocaleTimeString()}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-xs">
                    {n.attempts}/{n.max_attempts}
                  </TableCell>
                  <TableCell className="max-w-xs text-xs">{n.fail_reason ?? ""}</TableCell>
                  <TableCell>
                    <div className="flex flex-wrap justify-end gap-2">
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => setOpen(open === n.id ? null : n.id)}
                      >
                        {open === n.id ? "Hide attempts" : "Attempts"}
                      </Button>
                      {(n.status === "failed" || n.status === "parked") && (
                        <ReasonDialog
                          trigger={
                            <Button size="sm" variant="outline">
                              Retry
                            </Button>
                          }
                          title="Send this email again?"
                          description="Retry only after fixing the cause (mail provider, recipient). It gets three more attempts; the reason is kept in the audit."
                          confirmLabel="Retry"
                          variant="default"
                          onConfirm={async (reason) => {
                            await callWithAuth((token) =>
                              api.retryNotification(token, n.id, reason),
                            );
                            await refresh();
                          }}
                        />
                      )}
                    </div>
                    {open === n.id && (
                      <div className="mt-2 text-left text-xs">
                        {attempts.isPending && <p className="text-muted-foreground">Loading…</p>}
                        {attempts.data?.length === 0 && (
                          <p className="text-muted-foreground">No attempt yet.</p>
                        )}
                        {attempts.data?.map((a) => (
                          <p key={`${a.attempt}-${a.created_at}`}>
                            #{a.attempt} {a.outcome} · {new Date(a.created_at).toLocaleString()} ·{" "}
                            {a.duration_ms} ms{a.error && ` · ${a.error}`}
                          </p>
                        ))}
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {list.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No notifications for this filter.
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
