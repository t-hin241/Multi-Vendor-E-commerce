"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import type { AuditLogEntry } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// AuditLogPanel is collapsed by default and only fetches once expanded —
// same pattern as ShipmentTimeline: a moderation table with many rows
// shouldn't trigger N history calls just for loading the page. Generic over
// which endpoint it calls (vendor vs product audit log), since both return
// the same AuditLogEntry shape. `targetId` (not `fetchLog`, a fresh closure
// every render) is what makes the query key — and therefore the cache —
// unique per row.
export function AuditLogPanel({
  targetId,
  fetchLog,
  paginated = false,
}: {
  targetId: string;
  fetchLog: (token: string, offset?: number) => Promise<AuditLogEntry[]>;
  paginated?: boolean;
}) {
  const { callWithAuth } = useAuth();
  const [open, setOpen] = useState(false);
  const [offset, setOffset] = useState(0);
  const logQuery = useQuery({
    queryKey: ["audit-log", targetId, offset],
    queryFn: () => callWithAuth((token) => fetchLog(token, offset)),
    enabled: open,
    refetchInterval: open ? 10000 : false,
  });

  return (
    <div className="mt-1">
      <Button
        type="button"
        variant="link"
        size="sm"
        className="h-auto p-0 text-xs"
        onClick={() => setOpen((v) => !v)}
      >
        {open ? "Hide history" : "View history"}
      </Button>

      {open && (
        <div className="mt-1">
          {logQuery.isPending && <p className="text-xs text-muted-foreground">Loading…</p>}
          {logQuery.error && <p className="text-xs text-destructive">Could not load history.</p>}
          {logQuery.data && logQuery.data.length === 0 && (
            <p className="text-xs text-muted-foreground">No history yet.</p>
          )}
          {logQuery.data && logQuery.data.length > 0 && (
            <ul className="flex flex-col gap-1.5">
              {logQuery.data.map((entry, i) => (
                <li key={`${entry.created_at}-${i}`} className="text-xs">
                  <span className="font-medium capitalize">{entry.action}</span>{" "}
                  <span className="text-muted-foreground">
                    by {entry.actor_user_id.slice(0, 8)} ·{" "}
                    {new Date(entry.created_at).toLocaleString()}
                  </span>
                  {entry.reason && <p className="text-muted-foreground">Reason: {entry.reason}</p>}
                </li>
              ))}
            </ul>
          )}
          {paginated && (
            <div className="mt-2 flex gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={offset === 0 || logQuery.isFetching}
                onClick={() => setOffset(Math.max(0, offset - 20))}
              >
                Previous
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={logQuery.data?.length !== 20 || logQuery.isFetching}
                onClick={() => setOffset(offset + 20)}
              >
                Next
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
