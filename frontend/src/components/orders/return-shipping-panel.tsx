"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { buyerShippingLabel, canDispatch, returnShippingErrorMessage } from "@/lib/return-shipping";

// ReturnShippingPanel shows the buyer how to send an approved return back
// (AF-05): the shop's return address, the return code, the deadline and who
// pays; then the buyer reports the carrier and tracking number. A late
// parcel is still accepted. Nothing here says the money is back.
export function ReturnShippingPanel({
  returnRequest: r,
  orderId,
}: {
  returnRequest: api.ReturnRequest;
  orderId: string;
}) {
  const { callWithAuth } = useAuth();
  const instructions = useQuery({
    queryKey: ["return-shipping-instructions", r.id, r.version],
    queryFn: () => callWithAuth((token) => api.getReturnShippingInstructions(token, r.id)),
    enabled: r.authorization_version > 0,
  });
  if (r.shipping_status === "destination_missing") {
    return (
      <p className="text-xs text-muted-foreground">{buyerShippingLabel(r.shipping_status)}.</p>
    );
  }
  if (r.authorization_version === 0 || !instructions.data) return null;
  const i = instructions.data;
  return (
    <div className="mt-2 flex flex-col gap-1 rounded-md bg-muted/40 p-2 text-xs">
      <p className="font-medium">
        {buyerShippingLabel(r.shipping_status)} · Mã trả hàng {i.return_code}
      </p>
      <p>
        Gửi tới: {i.address.recipient_name} ({i.address.phone}), {i.address.street_address},{" "}
        {i.address.ward}, {i.address.district}, {i.address.province}
      </p>
      <p>Giờ nhận hàng: {i.address.receiving_hours}</p>
      {i.dispatch_deadline && r.shipping_status === "awaiting_dispatch" && (
        <p className={i.dispatch_overdue ? "text-destructive" : undefined}>
          Hạn gửi: {new Date(i.dispatch_deadline).toLocaleString("vi-VN")}
          {i.dispatch_overdue && " (đã quá hạn, bạn vẫn có thể gửi; sàn sẽ xem xét)"}
        </p>
      )}
      <p className="text-muted-foreground">{i.instructions}</p>
      {i.tracking_number && (
        <p>
          Đã gửi qua {i.carrier_name}, mã vận đơn {i.tracking_number}
        </p>
      )}
      {canDispatch(r) && <DispatchForm returnRequest={r} orderId={orderId} />}
    </div>
  );
}

function localNow(): string {
  const d = new Date();
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
}

function DispatchForm({
  returnRequest: r,
  orderId,
}: {
  returnRequest: api.ReturnRequest;
  orderId: string;
}) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [carrier, setCarrier] = useState("");
  const [tracking, setTracking] = useState("");
  const [sentAt, setSentAt] = useState(localNow);
  const [key] = useState(() => crypto.randomUUID());
  const [error, setError] = useState<string | null>(null);
  const send = useMutation({
    mutationFn: () =>
      callWithAuth((token) =>
        api.reportReturnDispatch(
          token,
          r.id,
          {
            carrier_name: carrier.trim(),
            tracking_number: tracking.trim(),
            dispatched_at: new Date(sentAt).toISOString(),
            expected_version: r.version,
          },
          key,
        ),
      ),
    onSuccess: async () => {
      setError(null);
      await queryClient.invalidateQueries({ queryKey: ["order", orderId] });
      await queryClient.invalidateQueries({ queryKey: ["return-shipping-instructions", r.id] });
    },
    onError: (err) =>
      setError(returnShippingErrorMessage(err, "Không ghi nhận được thông tin gửi hàng.")),
  });
  return (
    <form
      className="mt-1 flex flex-wrap items-end gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (!carrier.trim() || !tracking.trim()) {
          setError("Nhập đơn vị vận chuyển và mã vận đơn.");
          return;
        }
        send.mutate();
      }}
    >
      <label className="flex flex-col gap-1">
        Đơn vị vận chuyển
        <Input
          className="h-8 w-36"
          maxLength={60}
          value={carrier}
          onChange={(e) => setCarrier(e.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1">
        Mã vận đơn
        <Input
          className="h-8 w-40"
          maxLength={64}
          value={tracking}
          onChange={(e) => setTracking(e.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1">
        Thời gian gửi
        <Input
          className="h-8 w-48"
          type="datetime-local"
          value={sentAt}
          onChange={(e) => setSentAt(e.target.value)}
        />
      </label>
      <Button type="submit" size="sm" disabled={send.isPending}>
        {send.isPending ? "Đang gửi…" : "Báo đã gửi hàng"}
      </Button>
      {error && <p className="w-full text-destructive">{error}</p>}
    </form>
  );
}
