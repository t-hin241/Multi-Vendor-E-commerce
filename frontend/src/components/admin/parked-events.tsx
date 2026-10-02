"use client";

import { useQueries, useQueryClient } from "@tanstack/react-query";

import { ReasonDialog } from "@/components/admin/confirm-dialogs";
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
import { describeApiError } from "@/lib/errors";

// ParkedEvents lists, per consuming service, the domain events it could not
// apply (refused, or failing after its retries). An operator fixes the
// cause, then replays the event or discards it; both need a reason and are
// audited by the service.
export function ParkedEvents() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const services = api.EVENT_CONSUMER_SERVICES;
  const queries = useQueries({
    queries: services.map((s) => ({
      queryKey: ["parked-events", s.name],
      queryFn: () => callWithAuth((token) => api.listParkedEvents(token, s.prefix)),
      refetchInterval: 60_000,
    })),
  });

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Parked events"
        subtitle="Events a service could not apply. Fix the cause first, then replay; discard only an event that must never be applied."
      />
      {services.map((s, i) => {
        const q = queries[i];
        if (!q) return null;
        const refresh = () =>
          queryClient.invalidateQueries({ queryKey: ["parked-events", s.name] });
        return (
          <Card key={s.name}>
            <CardContent className="overflow-x-auto p-0">
              <div className="flex items-center justify-between px-4 pt-4">
                <p className="font-medium capitalize">{s.name}</p>
                {q.data && (
                  <Badge variant={q.data.length ? "destructive" : "outline"}>
                    {q.data.length} parked
                  </Badge>
                )}
              </div>
              {q.error && (
                <p className="px-4 py-2 text-sm text-destructive">
                  {describeApiError(q.error, "Could not load this service's events.")}
                </p>
              )}
              {q.data && q.data.length > 0 && (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Parked</TableHead>
                      <TableHead>Event</TableHead>
                      <TableHead>Attempts</TableHead>
                      <TableHead>Why</TableHead>
                      <TableHead />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {q.data.map((e) => (
                      <TableRow key={`${e.consumer}:${e.event_id}`}>
                        <TableCell className="whitespace-nowrap text-xs">
                          {new Date(e.parked_at).toLocaleString()}
                        </TableCell>
                        <TableCell className="text-xs">
                          {e.event_type}
                          <span className="block text-muted-foreground">
                            {e.consumer} · {e.aggregate_id.slice(0, 13)}
                          </span>
                        </TableCell>
                        <TableCell className="text-xs">{e.attempts}</TableCell>
                        <TableCell className="max-w-xs text-xs">{e.last_error ?? ""}</TableCell>
                        <TableCell>
                          <div className="flex flex-wrap justify-end gap-2">
                            <ReasonDialog
                              trigger={<Button size="sm">Replay</Button>}
                              title="Apply this event again?"
                              description="Replay only after fixing what made it fail. The reason is kept in the audit."
                              confirmLabel="Replay"
                              variant="default"
                              onConfirm={async (reason) => {
                                await callWithAuth((token) =>
                                  api.resolveParkedEvent(
                                    token,
                                    s.prefix,
                                    e.consumer,
                                    e.event_id,
                                    "replay",
                                    reason,
                                  ),
                                );
                                await refresh();
                              }}
                            />
                            <ReasonDialog
                              trigger={
                                <Button size="sm" variant="outline">
                                  Discard
                                </Button>
                              }
                              title="Never apply this event?"
                              description="Discard only an event that is wrong or obsolete. The reason is kept in the audit."
                              confirmLabel="Discard"
                              onConfirm={async (reason) => {
                                await callWithAuth((token) =>
                                  api.resolveParkedEvent(
                                    token,
                                    s.prefix,
                                    e.consumer,
                                    e.event_id,
                                    "discard",
                                    reason,
                                  ),
                                );
                                await refresh();
                              }}
                            />
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
              {q.data && q.data.length === 0 && (
                <p className="px-4 pb-4 text-sm text-muted-foreground">Nothing parked.</p>
              )}
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
