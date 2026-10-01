"use client";

import { useInfiniteQuery } from "@tanstack/react-query";
import { useSearchParams } from "next/navigation";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
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

const SOURCES = [
  "identity",
  "vendor",
  "catalog",
  "inventory",
  "order",
  "payment",
  "shipment",
  "review",
  "notification",
];
const FIELDS: { key: keyof api.AuditFilter; label: string; placeholder: string }[] = [
  { key: "request_id", label: "Request id", placeholder: "from an error message" },
  { key: "entity_id", label: "Entity id", placeholder: "order, refund, shop…" },
  { key: "entity_type", label: "Entity type", placeholder: "order, refund, vendor…" },
  { key: "action", label: "Action", placeholder: "refund_requested…" },
  { key: "actor_id", label: "Actor id", placeholder: "admin user id" },
];

function changes(value?: Record<string, unknown>) {
  if (!value) return "";
  return Object.entries(value)
    .map(([k, v]) =>
      Array.isArray(v) && v.length === 2
        ? `${k}: ${String(v[0])} → ${String(v[1])}`
        : `${k}: ${String(v)}`,
    )
    .join(", ");
}

// AuditSearch reads the audit every service keeps (read-only), newest
// first, one page at a time. It can open with ?request_id= from an
// "outcome unknown" message to find out whether an action was applied.
export function AuditSearch() {
  const { callWithAuth } = useAuth();
  const params = useSearchParams();
  const initial: api.AuditFilter = {};
  for (const f of FIELDS) {
    const v = params.get(f.key);
    if (v) (initial as Record<string, string>)[f.key] = v;
  }
  const [draft, setDraft] = useState<api.AuditFilter>(initial);
  const [filter, setFilter] = useState<api.AuditFilter>(initial);

  const audit = useInfiniteQuery({
    queryKey: ["admin-audit", filter],
    queryFn: ({ pageParam }) =>
      callWithAuth((token) =>
        api.searchAdminAudit(token, { ...filter, limit: 50, cursor: pageParam }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
  });

  const pages = audit.data?.pages ?? [];
  const entries = pages.flatMap((p) => p.entries);
  const missing = Array.from(
    new Set(pages.flatMap((p) => p.sources.filter((s) => s.status !== "ok").map((s) => s.name))),
  );

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Audit log"
        subtitle="Every admin decision recorded by the services, with its reason and request id. Read-only."
      />
      <Card>
        <CardContent>
          <form
            className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"
            onSubmit={(e) => {
              e.preventDefault();
              const next: api.AuditFilter = {};
              for (const [k, v] of Object.entries(draft)) {
                if (typeof v === "string" && v.trim())
                  (next as Record<string, string>)[k] = v.trim();
              }
              setFilter(next);
            }}
          >
            <Label className="flex flex-col items-start gap-1.5 text-xs">
              Service
              <select
                className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                value={draft.source ?? ""}
                onChange={(e) => setDraft({ ...draft, source: e.target.value || undefined })}
              >
                <option value="">All services</option>
                {SOURCES.map((s) => (
                  <option key={s} value={s}>
                    {s}
                  </option>
                ))}
              </select>
            </Label>
            {FIELDS.map((f) => (
              <Label key={f.key} className="flex flex-col items-start gap-1.5 text-xs">
                {f.label}
                <Input
                  value={(draft[f.key] as string | undefined) ?? ""}
                  placeholder={f.placeholder}
                  onChange={(e) => setDraft({ ...draft, [f.key]: e.target.value })}
                />
              </Label>
            ))}
            <Label className="flex flex-col items-start gap-1.5 text-xs">
              From
              <Input
                type="datetime-local"
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    from: e.target.value ? new Date(e.target.value).toISOString() : undefined,
                  })
                }
              />
            </Label>
            <Label className="flex flex-col items-start gap-1.5 text-xs">
              To
              <Input
                type="datetime-local"
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    to: e.target.value ? new Date(e.target.value).toISOString() : undefined,
                  })
                }
              />
            </Label>
            <div className="flex items-end">
              <Button type="submit" size="sm">
                Search
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>

      {missing.length > 0 && (
        <div className="rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-sm">
          Results are incomplete: {missing.join(", ")} did not answer. Search again later before
          concluding an action was not recorded.
        </div>
      )}
      {audit.error && (
        <p className="text-sm text-destructive">
          {audit.error instanceof api.ApiError
            ? audit.error.message
            : "Could not search the audit."}
        </p>
      )}
      {audit.isPending && <p className="text-sm text-muted-foreground">Loading…</p>}

      {audit.data && (
        <Card>
          <CardContent className="overflow-x-auto p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>When</TableHead>
                  <TableHead>Service</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead>Entity</TableHead>
                  <TableHead>Actor</TableHead>
                  <TableHead>Reason and changes</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entries.map((e) => (
                  <TableRow key={`${e.source}/${e.id}`}>
                    <TableCell className="whitespace-nowrap text-xs">
                      {new Date(e.occurred_at).toLocaleString()}
                    </TableCell>
                    <TableCell className="text-xs">{e.source}</TableCell>
                    <TableCell className="text-xs font-medium">
                      {e.action.replace(/_/g, " ")}
                    </TableCell>
                    <TableCell className="text-xs">
                      {e.entity_type} {e.entity_id.slice(0, 8)}
                    </TableCell>
                    <TableCell className="text-xs">
                      {e.actor_id ? e.actor_id.slice(0, 8) : "—"}
                    </TableCell>
                    <TableCell className="max-w-md text-xs">
                      {e.reason && <span className="block">{e.reason}</span>}
                      <span className="block text-muted-foreground">{changes(e.changes)}</span>
                      {e.request_id && (
                        <span className="block text-muted-foreground">request {e.request_id}</span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {entries.length === 0 && (
              <p className="p-6 text-center text-sm text-muted-foreground">
                No audit entries match.
              </p>
            )}
          </CardContent>
        </Card>
      )}
      {audit.hasNextPage && (
        <Button
          variant="outline"
          size="sm"
          className="self-center"
          disabled={audit.isFetchingNextPage}
          onClick={() => audit.fetchNextPage()}
        >
          {audit.isFetchingNextPage ? "Loading…" : "Older entries"}
        </Button>
      )}
    </div>
  );
}
