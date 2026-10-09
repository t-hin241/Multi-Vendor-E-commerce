"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import {
  CONDITION_LABELS,
  type ReceiptDraft,
  deliveryErrorMessage,
  needsReceipt,
  receiptLines,
  vendorDeliveryLabel,
} from "@/lib/delivery-exceptions";

// VendorDeliveryExceptionsSection lists the shop's failed deliveries
// (AF-04). When a package comes back the shop records every unit as
// sellable, damaged or missing; the marketplace then decides redelivery
// or refund. Only sellable units go back to stock, after a refund.
export function VendorDeliveryExceptionsSection({
  vendorId,
  vendorOrders,
}: {
  vendorId: string;
  vendorOrders: api.VendorOrder[];
}) {
  const { callWithAuth } = useAuth();
  const cases = useQuery({
    queryKey: ["vendor-delivery-exceptions", vendorId],
    queryFn: () =>
      callWithAuth((token) => api.listVendorDeliveryExceptions(token, vendorId, "open")),
  });
  const list = cases.data ?? [];
  if (list.length === 0) return null;

  return (
    <div>
      <p className="text-sm font-medium">Giao thất bại / hàng hoàn về ({list.length})</p>
      <ul className="mt-2 flex flex-col gap-3">
        {list.map((d) => (
          <li key={d.id}>
            <Card>
              <CardContent className="flex flex-col gap-1 text-sm">
                <p className="font-medium">
                  Đơn #{d.order_id.slice(0, 8)} · {vendorDeliveryLabel(d.status)}
                </p>
                <p className="text-xs text-muted-foreground">
                  Lần giao {d.attempt_no} · {d.failed_attempts} lần giao không thành công
                  {d.detection_reason ? ` · ${d.detection_reason}` : ""}
                </p>
                {d.receipt && (
                  <p className="text-xs text-muted-foreground">
                    Đã ghi nhận:{" "}
                    {d.receipt.lines
                      .map((l) => `${l.quantity} ${CONDITION_LABELS[l.condition].toLowerCase()}`)
                      .join(", ")}
                  </p>
                )}
                {d.status === "redelivery_pending" && (
                  <p className="text-xs">
                    Người mua đồng ý giao lại: lần giao mới đã được tạo trong mục vận chuyển; phí
                    giao lại không thu của người mua.
                  </p>
                )}
                {needsReceipt(d) && (
                  <ReceiptForm
                    exception={d}
                    vendorOrder={vendorOrders.find((vo) => vo.id === d.vendor_order_id)}
                    vendorId={vendorId}
                  />
                )}
              </CardContent>
            </Card>
          </li>
        ))}
      </ul>
    </div>
  );
}

function ReceiptForm({
  exception: d,
  vendorOrder,
  vendorId,
}: {
  exception: api.DeliveryException;
  vendorOrder?: api.VendorOrder;
  vendorId: string;
}) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const items = (vendorOrder?.items ?? []).filter((i) => i.id);
  const [draft, setDraft] = useState<ReceiptDraft>(() =>
    Object.fromEntries(items.map((i) => [i.id, { sellable: i.quantity }])),
  );
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: (lines: ReturnType<typeof receiptLines>["lines"]) =>
      callWithAuth((token) =>
        api.recordGoodsReceipt(token, "vendor", d.id, {
          received_lines: lines,
          note: note.trim() || undefined,
          expected_version: d.version,
        }),
      ),
    onSuccess: async () => {
      setError(null);
      await queryClient.invalidateQueries({ queryKey: ["vendor-delivery-exceptions", vendorId] });
    },
    onError: (err) => setError(deliveryErrorMessage(err, "Không ghi nhận được hàng hoàn về.")),
  });

  if (items.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">Mở chi tiết đơn để ghi nhận hàng hoàn về.</p>
    );
  }
  return (
    <form
      className="mt-1 flex flex-col gap-2 rounded-md border p-2"
      onSubmit={(e) => {
        e.preventDefault();
        const out = receiptLines(items, draft);
        if (out.error) {
          setError(out.error);
          return;
        }
        save.mutate(out.lines);
      }}
    >
      <p className="text-xs">
        Ghi nhận từng đơn vị hàng hoàn về (tổng phải bằng số lượng đã giao):
      </p>
      {items.map((item) => (
        <div key={item.id} className="flex flex-wrap items-center gap-2 text-xs">
          <span className="min-w-40">
            {item.product_name} × {item.quantity}
          </span>
          {(["sellable", "damaged", "missing"] as const).map((condition) => (
            <label key={condition} className="flex items-center gap-1">
              {CONDITION_LABELS[condition]}
              <Input
                type="number"
                min={0}
                max={item.quantity}
                className="h-7 w-16"
                value={draft[item.id]?.[condition] ?? 0}
                onChange={(e) =>
                  setDraft((prev) => ({
                    ...prev,
                    [item.id]: { ...prev[item.id], [condition]: Number(e.target.value) },
                  }))
                }
              />
            </label>
          ))}
        </div>
      ))}
      <Textarea
        rows={2}
        maxLength={1000}
        value={note}
        placeholder="Ghi chú (ví dụ: hộp móp, thiếu phụ kiện)"
        onChange={(e) => setNote(e.target.value)}
      />
      {error && <p className="text-xs text-destructive">{error}</p>}
      <Button
        type="submit"
        size="sm"
        variant="outline"
        className="self-start"
        disabled={save.isPending}
      >
        {save.isPending ? "Đang lưu…" : "Ghi nhận hàng hoàn về"}
      </Button>
    </form>
  );
}
