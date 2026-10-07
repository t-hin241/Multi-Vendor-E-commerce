"use client";

import Link from "next/link";
import { useState } from "react";

import { StatusFilter } from "@/components/admin/status-filter";
import { SectionHeader } from "@/components/section-header";
import { SupportStatusBadge } from "@/components/support/support-status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useSupportCases } from "@/lib/hooks/use-support-cases";
import { SUPPORT_STATUS_OPTIONS, isOverdue, supportCategoryLabel } from "@/lib/support-cases";

type View = "unassigned" | "mine" | "overdue" | "all";

const VIEWS: { value: View; label: string }[] = [
  { value: "unassigned", label: "Unassigned" },
  { value: "mine", label: "Assigned to me" },
  { value: "overdue", label: "Overdue" },
  { value: "all", label: "All" },
];

// SupportQueue is the admins' work list of support cases. Order owns the
// cases; every change happens on the case page through Order's API.
export function SupportQueue() {
  const [view, setView] = useState<View>("unassigned");
  const [status, setStatus] = useState("");
  const [hideClosed, setHideClosed] = useState(true);
  const cases = useSupportCases(
    "admin",
    {
      status: status || undefined,
      unassigned: view === "unassigned",
      assignee: view === "mine" ? "me" : undefined,
      overdue: view === "overdue",
    },
    true,
  );
  const items = (cases.data?.pages.flatMap((p) => p.items) ?? []).filter(
    (c) => !hideClosed || status === "closed" || c.status !== "closed",
  );

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Support cases"
        subtitle="Buyer complaints on an order. Assign one, ask the buyer or the shop, then resolve it — linking the refund or return when money or goods are involved."
        action={
          <StatusFilter status={status} onChange={setStatus} options={SUPPORT_STATUS_OPTIONS} />
        }
      />
      <div className="flex flex-wrap items-center gap-2">
        {VIEWS.map((v) => (
          <Button
            key={v.value}
            size="sm"
            variant={view === v.value ? "default" : "outline"}
            onClick={() => setView(v.value)}
          >
            {v.label}
          </Button>
        ))}
        <label className="ml-auto flex items-center gap-2 text-sm">
          <Switch checked={hideClosed} onCheckedChange={setHideClosed} />
          Hide closed
        </label>
      </div>
      {cases.error && <p className="text-sm text-destructive">Could not load support cases.</p>}
      <Card className="py-0">
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Case</TableHead>
                <TableHead>Topic</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Due</TableHead>
                <TableHead>Assignee</TableHead>
                <TableHead>Opened</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {cases.isPending && (
                <TableRow>
                  <TableCell colSpan={6} className="text-muted-foreground">
                    Loading…
                  </TableCell>
                </TableRow>
              )}
              {cases.data && items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-muted-foreground">
                    Nothing here.
                  </TableCell>
                </TableRow>
              )}
              {items.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <Link
                      className="font-mono text-primary underline"
                      href={`/admin/support/${c.id}`}
                    >
                      {c.id.slice(0, 8)}
                    </Link>
                    {c.financial_hold && c.status !== "closed" && (
                      <span className="ml-2 text-xs text-muted-foreground">payout held</span>
                    )}
                  </TableCell>
                  <TableCell>{supportCategoryLabel(c.category)}</TableCell>
                  <TableCell>
                    <SupportStatusBadge status={c.status} locale="en" />
                  </TableCell>
                  <TableCell className={isOverdue(c) ? "text-destructive" : undefined}>
                    {c.due_at ? new Date(c.due_at).toLocaleString("vi-VN") : "—"}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {c.assignee_id ? c.assignee_id.slice(0, 8) : "—"}
                  </TableCell>
                  <TableCell>{new Date(c.created_at).toLocaleString("vi-VN")}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      {cases.hasNextPage && (
        <Button
          variant="outline"
          disabled={cases.isFetchingNextPage}
          onClick={() => cases.fetchNextPage()}
        >
          {cases.isFetchingNextPage ? "Loading…" : "Load more"}
        </Button>
      )}
    </div>
  );
}
