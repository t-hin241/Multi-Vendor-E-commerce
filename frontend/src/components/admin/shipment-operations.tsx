"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";

import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

const SECTIONS: { key: string; title: string; hint: string }[] = [
  {
    key: "fulfillment_lag",
    title: "Paid, not shipped for 3+ days",
    hint: "Chase the vendor or cancel the order with a refund.",
  },
  {
    key: "tracking_stale",
    title: "In transit, no update for 7+ days",
    hint: "Check with the carrier; record delivery, a failed attempt or a return.",
  },
  {
    key: "interception_pending",
    title: "Interception without a decision for 24+ hours",
    hint: "Call the carrier and record its answer. The package is not assumed stopped.",
  },
  {
    key: "failed_attempts",
    title: "Delivery attempts failed",
    hint: "Contact the buyer; record a return if the package comes back.",
  },
];

// ShipmentOperations lists fulfillment problems and lets an admin record
// what the carrier said. Every action needs a reason and is kept on the
// shipment's timeline.
export function ShipmentOperations({ focusID }: { focusID?: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const ops = useQuery({
    queryKey: ["shipment-operations"],
    queryFn: () => callWithAuth((token) => api.getShipmentOperations(token)),
    refetchInterval: 60_000,
  });

  const focused = useQuery({
    queryKey: ["shipment-operations", "focus", focusID],
    enabled: !!focusID,
    queryFn: () =>
      callWithAuth((token) =>
        api.request<api.Shipment>(
          `/api/shipments/admin/shipments/${encodeURIComponent(focusID!)}`,
          { token },
        ),
      ),
  });
  async function act(fn: (token: string) => Promise<unknown>) {
    await callWithAuth(fn);
    await queryClient.invalidateQueries({ queryKey: ["shipment-operations"] });
  }

  const action = (
    label: string,
    title: string,
    fn: (token: string, reason: string) => Promise<unknown>,
    destructive = false,
  ) => (
    <ReasonDialog
      trigger={
        <Button size="sm" variant="outline" className={destructive ? "text-destructive" : ""}>
          {label}
        </Button>
      }
      title={title}
      description="Recorded on the shipment's timeline with your name."
      confirmLabel={label}
      variant={destructive ? "destructive" : "default"}
      onConfirm={(reason) => act((token) => fn(token, reason))}
    />
  );

  const row = (s: api.Shipment) => (
    <div
      key={s.id}
      className="flex flex-wrap items-center justify-between gap-2 border-t py-2 text-sm"
    >
      <span>
        Package {s.vendor_order_id.slice(0, 8)} · {s.status.replace(/_/g, " ")}
        {s.tracking_number && ` · ${s.tracking_number}`}
        <span className="block text-xs text-muted-foreground">
          {s.province ?? ""} · updated {new Date(s.updated_at).toLocaleString()}
          {s.failed_attempts > 0 &&
            ` · ${s.failed_attempts} failed attempt(s): ${s.last_attempt_reason ?? ""}`}
        </span>
      </span>
      <span className="flex flex-wrap gap-2">
        {s.status === "shipped" && (
          <>
            {action("Delivered", "Record the package as delivered?", (token, note) =>
              api.adminShipmentAction(token, s.id, "deliver", { note }),
            )}
            {action("Failed attempt", "Record a failed delivery attempt?", (token, reason) =>
              api.adminShipmentAction(token, s.id, "failed-attempts", { reason }),
            )}
            {action(
              "Returned",
              "Record that the package came back to the vendor?",
              (token, reason) => api.adminShipmentAction(token, s.id, "return", { reason }),
              true,
            )}
          </>
        )}
        {s.status === "interception_requested" && (
          <>
            {action("Intercepted", "The carrier stopped the package?", (token, note) =>
              api.adminShipmentAction(token, s.id, "interception-decision", {
                accepted: true,
                note,
              }),
            )}
            {action("Not intercepted", "The carrier could not stop the package?", (token, note) =>
              api.adminShipmentAction(token, s.id, "interception-decision", {
                accepted: false,
                note,
              }),
            )}
          </>
        )}
      </span>
    </div>
  );

  const o = ops.data;
  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Fulfillment operations"
        subtitle="Manual tracking mode: record what the carrier reports."
      />
      {focused.error && <p role="alert">Không thể tải hồ sơ vận chuyển được yêu cầu.</p>}
      {focused.data && (
        <Card>
          <CardHeader>
            <CardTitle>Hồ sơ từ thông báo</CardTitle>
          </CardHeader>
          <CardContent>{row(focused.data)}</CardContent>
        </Card>
      )}
      {ops.error && (
        <p className="text-sm text-destructive">Could not load fulfillment operations.</p>
      )}
      {o && (
        <>
          {SECTIONS.map((section) => (
            <Card key={section.key}>
              <CardHeader>
                <CardTitle className="text-base">
                  {section.title} ({o.counts[section.key] ?? 0})
                </CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-xs text-muted-foreground">{section.hint}</p>
                {(o.lists[section.key] ?? []).map(row)}
              </CardContent>
            </Card>
          ))}
          <Card>
            <CardHeader>
              <CardTitle className="text-base">
                Order not updated ({o.counts.order_events_review ?? 0} need review)
              </CardTitle>
            </CardHeader>
            <CardContent className="text-sm">
              {o.outbox.length === 0 && <p className="text-muted-foreground">None.</p>}
              {o.outbox.map((e) => (
                <div
                  key={e.id}
                  className="flex flex-wrap items-center justify-between gap-2 border-t py-2"
                >
                  <span>
                    {e.event_type} for package{" "}
                    <Link className="underline" href="/admin/orders">
                      {e.vendor_order_id.slice(0, 8)}
                    </Link>{" "}
                    · {e.attempts} attempt(s){e.requires_review && " · refused by Order"}
                    {e.last_error && (
                      <span className="block text-xs text-muted-foreground">{e.last_error}</span>
                    )}
                  </span>
                  {action("Resend", "Send this event to Order again?", (token, reason) =>
                    api.retryShipmentOrderEvent(token, e.id, e.shipment_id, reason),
                  )}
                </div>
              ))}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
