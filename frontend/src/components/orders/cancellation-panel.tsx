"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import {
  BUYER_CANCEL_REASONS,
  buyerCancellationLabel,
  canAskCancellation,
  cancellationErrorMessage,
} from "@/lib/cancellations";

// CancellationPanel lets the buyer ask to cancel a paid package that is
// not handed over yet (AF-03) and follow each request. "Accepted",
// "stopped" and "refunded" are separate steps; nothing says the money is
// back before Payment confirmed it.
export function CancellationPanel({
  order,
  shipments,
}: {
  order: api.Order;
  shipments?: api.Shipment[];
}) {
  const { callWithAuth } = useAuth();
  const requests = useQuery({
    queryKey: ["order-cancellations", order.id],
    queryFn: () => callWithAuth((token) => api.listOrderCancellations(token, order.id)),
    refetchInterval: (q) =>
      q.state.data?.some((r) => r.status !== "resolved" && r.status !== "rejected")
        ? 30_000
        : false,
  });
  const packages = order.vendor_orders ?? [];
  const list = requests.data ?? [];
  const eligible = packages.filter((vo) =>
    canAskCancellation(
      vo,
      list,
      shipments?.find((s) => s.vendor_order_id === vo.id),
    ),
  );
  if (list.length === 0 && eligible.length === 0) return null;

  return (
    <Card className="mt-4">
      <CardHeader>
        <CardTitle className="text-base">Hủy gói hàng đã thanh toán</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        {list.map((r) => {
          const idx = packages.findIndex((vo) => vo.id === r.vendor_order_id);
          return (
            <div key={r.id} className="rounded-md border p-2">
              <p className="font-medium">
                Gói hàng {idx >= 0 ? idx + 1 : ""} · {buyerCancellationLabel(r.status)}
              </p>
              {r.origin === "vendor" && (
                <p className="text-muted-foreground">Người bán báo không thể giao gói này.</p>
              )}
              {r.status === "rejected" && r.decision_reason && (
                <p className="text-muted-foreground">Lý do: {r.decision_reason}</p>
              )}
              {r.status === "refund_pending" && (
                <p className="text-xs text-muted-foreground">
                  Khoản hoàn tiền được thông báo riêng khi được xác nhận.
                </p>
              )}
            </div>
          );
        })}
        {eligible.map((vo) => (
          <CancelForm
            key={vo.id}
            orderId={order.id}
            vendorOrder={vo}
            label={`Gói hàng ${packages.findIndex((p) => p.id === vo.id) + 1}`}
          />
        ))}
      </CardContent>
    </Card>
  );
}

function CancelForm({
  orderId,
  vendorOrder,
  label,
}: {
  orderId: string;
  vendorOrder: api.VendorOrder;
  label: string;
}) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [reasonCode, setReasonCode] = useState("changed_mind");
  const [reason, setReason] = useState("");
  const [key, setKey] = useState(() => crypto.randomUUID());
  const [error, setError] = useState<string | null>(null);
  const send = useMutation({
    mutationFn: () =>
      callWithAuth((token) =>
        api.requestCancellation(
          token,
          "buyer",
          vendorOrder.id,
          { reason_code: reasonCode, reason: reason.trim() },
          key,
        ),
      ),
    onSuccess: async () => {
      setOpen(false);
      setReason("");
      setKey(crypto.randomUUID());
      await queryClient.invalidateQueries({ queryKey: ["order-cancellations", orderId] });
    },
    onError: (err) => setError(cancellationErrorMessage(err, "Không gửi được yêu cầu hủy.")),
  });

  if (!open) {
    return (
      <Button variant="outline" size="sm" className="self-start" onClick={() => setOpen(true)}>
        Yêu cầu hủy {label}
      </Button>
    );
  }
  return (
    <form
      className="flex flex-col gap-2 rounded-md border p-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (!reason.trim()) {
          setError("Vui lòng cho biết lý do.");
          return;
        }
        setError(null);
        send.mutate();
      }}
    >
      <p className="font-medium">Yêu cầu hủy {label}</p>
      <p className="text-xs text-muted-foreground">
        Gói hàng sẽ tạm dừng giao trong khi sàn xem xét. Nếu người bán đã giao cho vận chuyển, yêu
        cầu có thể không được chấp nhận.
      </p>
      <Label>Lý do</Label>
      <Select value={reasonCode} onValueChange={setReasonCode}>
        <SelectTrigger>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {BUYER_CANCEL_REASONS.map((r) => (
            <SelectItem key={r.value} value={r.value}>
              {r.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Textarea
        rows={2}
        maxLength={1000}
        value={reason}
        placeholder="Mô tả ngắn"
        onChange={(e) => setReason(e.target.value)}
      />
      {error && <p className="text-destructive">{error}</p>}
      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={send.isPending}>
          {send.isPending ? "Đang gửi…" : "Gửi yêu cầu hủy"}
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={() => setOpen(false)}>
          Đóng
        </Button>
      </div>
    </form>
  );
}
