"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { SectionHeader } from "@/components/section-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function time(value?: string) {
  return value ? new Date(value).toLocaleTimeString() : "—";
}

// OperationsDashboard is the admin's starting point: what is waiting and
// what is stuck, per service. A figure whose service did not answer says
// "unavailable" instead of showing zero.
export function OperationsDashboard() {
  const { callWithAuth } = useAuth();
  const dashboard = useQuery({
    queryKey: ["admin-dashboard"],
    queryFn: () => callWithAuth((token) => api.getAdminDashboard(token)),
    refetchInterval: 60_000,
  });

  const d = dashboard.data;
  const groups: { name: string; tiles: api.AdminDashboardTile[] }[] = [];
  for (const tile of d?.tiles ?? []) {
    let group = groups.find((g) => g.name === tile.group);
    if (!group) {
      group = { name: tile.group, tiles: [] };
      groups.push(group);
    }
    group.tiles.push(tile);
  }
  const down = (d?.sources ?? []).filter((s) => s.status !== "ok");

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Operations"
        subtitle="Counts come from each service when the page loads and refresh every minute."
        action={
          <Button
            size="sm"
            variant="outline"
            disabled={dashboard.isFetching}
            onClick={() => dashboard.refetch()}
          >
            {dashboard.isFetching ? "Refreshing…" : "Refresh"}
          </Button>
        }
      />
      {dashboard.isPending && <p className="text-sm text-muted-foreground">Loading…</p>}
      {dashboard.error && (
        <p className="text-sm text-destructive">
          Could not load the dashboard
          {dashboard.error instanceof api.ApiError ? `: ${dashboard.error.message}` : "."}
        </p>
      )}
      {d && (
        <>
          <p className="text-xs text-muted-foreground">
            Generated {time(d.generated_at)}.{" "}
            {d.sources
              .filter((s) => s.status === "ok")
              .map((s) => `${s.name} ${time(s.generated_at ?? s.fetched_at)}`)
              .join(" · ")}
          </p>
          {down.length > 0 && (
            <div className="rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-sm">
              <p className="font-medium">Some services did not answer</p>
              <p className="text-muted-foreground">
                {down.map((s) => `${s.name} (${s.error ?? "unavailable"})`).join(", ")}. Their
                figures show as unavailable, not as zero.
              </p>
            </div>
          )}
          {groups.map((group) => (
            <Card key={group.name}>
              <CardHeader>
                <CardTitle className="text-base">{group.name}</CardTitle>
              </CardHeader>
              <CardContent className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {group.tiles.map((tile) => (
                  <Link
                    key={tile.key}
                    href={tile.link}
                    className="rounded-md border p-3 transition-colors hover:bg-muted/50"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <span className="text-sm">{tile.label}</span>
                      {tile.status === "unavailable" ? (
                        <Badge variant="outline">Unavailable</Badge>
                      ) : (
                        <span
                          className={
                            tile.status === "attention"
                              ? "text-2xl font-semibold text-destructive"
                              : "text-2xl font-semibold"
                          }
                        >
                          {tile.count}
                        </span>
                      )}
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">{tile.hint}</p>
                  </Link>
                ))}
              </CardContent>
            </Card>
          ))}
        </>
      )}
    </div>
  );
}
