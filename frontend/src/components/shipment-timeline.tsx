"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import type { ShipmentStatus } from "@/lib/api-client";
import { useShipmentEvents } from "@/lib/hooks/use-shipments";
import { cn } from "@/lib/utils";

const SHIPMENT_STATUS_LABEL: Record<ShipmentStatus, string> = {
  pending: "Chờ đóng gói",
  ready_to_ship: "Sẵn sàng giao cho đơn vị vận chuyển",
  shipped: "Đang giao hàng",
  delivered: "Đã giao hàng",
  cancelled: "Đã hủy",
  interception_requested: "Yêu cầu thu hồi",
};

// ShipmentTimeline is collapsed by default and only fetches
// listShipmentEvents once expanded — a buyer with several packages
// shouldn't trigger N event-history calls just for loading the order page.
export function ShipmentTimeline({ shipmentId }: { shipmentId: string }) {
  const [open, setOpen] = useState(false);
  const eventsQuery = useShipmentEvents(shipmentId, open);

  return (
    <div className="mt-1">
      <Button
        type="button"
        variant="link"
        size="sm"
        className="h-auto p-0 text-xs"
        onClick={() => setOpen((v) => !v)}
      >
        {open ? "Ẩn lịch sử vận chuyển" : "Xem lịch sử vận chuyển"}
      </Button>

      {open && (
        <div className="mt-2">
          {eventsQuery.isPending && (
            <p className="text-xs text-muted-foreground">Đang tải lịch sử vận chuyển…</p>
          )}
          {eventsQuery.error && (
            <p className="text-xs text-destructive">Không thể tải lịch sử vận chuyển.</p>
          )}
          {eventsQuery.data && eventsQuery.data.length === 0 && (
            <p className="text-xs text-muted-foreground">Chưa có cập nhật vận chuyển nào.</p>
          )}
          {eventsQuery.data && eventsQuery.data.length > 0 && (
            <ol className="flex flex-col gap-3">
              {eventsQuery.data.map((event, i) => (
                <li key={`${event.status}-${event.created_at}`} className="relative pl-5">
                  <span
                    className={cn(
                      "absolute top-1 left-0 size-2.5 rounded-full",
                      i === eventsQuery.data.length - 1 ? "bg-primary" : "bg-muted-foreground/40",
                    )}
                  />
                  {i < eventsQuery.data.length - 1 && (
                    <span className="absolute top-3.5 left-[4.5px] h-[calc(100%+0.25rem)] w-px bg-border" />
                  )}
                  <p className="text-xs font-medium">{SHIPMENT_STATUS_LABEL[event.status]}</p>
                  {event.note && <p className="text-xs text-muted-foreground">{event.note}</p>}
                  <p className="text-[11px] text-muted-foreground">
                    {new Date(event.created_at).toLocaleString("vi-VN")}
                  </p>
                </li>
              ))}
            </ol>
          )}
        </div>
      )}
    </div>
  );
}
