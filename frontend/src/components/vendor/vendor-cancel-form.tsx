"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import {
  VENDOR_CANCEL_REASONS,
  buyerCancellationLabel,
  cancellationErrorMessage,
} from "@/lib/cancellations";

// VendorCancelForm: the shop reports it cannot fulfil a paid package
// (AF-03). The marketplace decides; the package is not shipped meanwhile
// (Order refuses the handover while the request is open).
export function VendorCancelForm({
  vendorOrder,
  openRequest,
  onChanged,
}: {
  vendorOrder: api.VendorOrder;
  openRequest?: api.CancellationRequest;
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [open, setOpen] = useState(false);
  const [reasonCode, setReasonCode] = useState("out_of_stock");
  const [reason, setReason] = useState("");
  const [key, setKey] = useState(() => crypto.randomUUID());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (openRequest) {
    return (
      <p className="mt-2 text-xs text-destructive">
        Có yêu cầu hủy gói này ({buyerCancellationLabel(openRequest.status)}). Không giao gói này
        trong khi sàn xử lý.
      </p>
    );
  }
  if (!open) {
    return (
      <Button
        size="sm"
        variant="ghost"
        className="mt-2 h-auto p-0 text-xs"
        onClick={() => setOpen(true)}
      >
        Báo không thể giao gói này
      </Button>
    );
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!reason.trim()) {
      setError("Vui lòng mô tả lý do.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await callWithAuth((t) =>
        api.requestCancellation(
          t,
          "vendor",
          vendorOrder.id,
          { reason_code: reasonCode, reason: reason.trim() },
          key,
        ),
      );
      setOpen(false);
      setReason("");
      setKey(crypto.randomUUID());
      onChanged();
    } catch (err) {
      setError(cancellationErrorMessage(err, "Không gửi được báo cáo."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="mt-2 flex flex-col gap-2">
      <div className="flex flex-wrap gap-2">
        {VENDOR_CANCEL_REASONS.map((r) => (
          <label key={r.value} className="flex items-center gap-1 text-xs">
            <input
              type="radio"
              name={`vendor-cancel-${vendorOrder.id}`}
              checked={reasonCode === r.value}
              onChange={() => setReasonCode(r.value)}
            />
            {r.label}
          </label>
        ))}
      </div>
      <Textarea
        rows={2}
        maxLength={1000}
        value={reason}
        placeholder="Mô tả cho sàn (ví dụ: món nào hết, số lượng)"
        onChange={(e) => setReason(e.target.value)}
      />
      {error && <p className="text-xs text-destructive">{error}</p>}
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="outline" disabled={busy}>
          {busy ? "Đang gửi…" : "Gửi báo cáo"}
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={() => setOpen(false)}>
          Đóng
        </Button>
      </div>
    </form>
  );
}
