"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";

// StockCountForm records a physical count (kiểm kê) of one stock item. It
// can only write stock down; adding units still goes through a restock
// request. Units held for pending orders are counted as on hand and are
// never changed by the count.
export function StockCountForm({
  item,
  onRecorded,
}: {
  item: api.InventoryItem;
  onRecorded: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [open, setOpen] = useState(false);
  const [counted, setCounted] = useState("");
  const [reason, setReason] = useState("");
  // One id per submission, kept across retries so a lost response is not
  // applied twice; a fresh id is taken after each recorded count.
  const [countId, setCountId] = useState(() => crypto.randomUUID());
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const onHand = item.available_quantity + item.reserved_quantity;
  const countedNumber = Number(counted);
  const valid =
    counted !== "" &&
    Number.isInteger(countedNumber) &&
    countedNumber >= item.reserved_quantity &&
    countedNumber <= onHand &&
    reason.trim() !== "";

  async function submit() {
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      const result = await callWithAuth((token) =>
        api.recordStockCount(token, item.id, {
          count_id: countId,
          counted_on_hand: countedNumber,
          reason: reason.trim(),
        }),
      );
      setMessage(
        `Đã ghi kiểm kê: còn bán được ${result.new_available} (trước đó ${result.previous_available}).`,
      );
      setCounted("");
      setReason("");
      setCountId(crypto.randomUUID());
      setOpen(false);
      onRecorded();
    } catch (err) {
      setError(describeApiError(err, "Không thể ghi kiểm kê."));
    } finally {
      setSaving(false);
    }
  }

  if (!open) {
    return (
      <div className="flex flex-col gap-0.5">
        <Button size="sm" variant="outline" className="w-fit" onClick={() => setOpen(true)}>
          Kiểm kê / ghi giảm
        </Button>
        {message && <span className="text-xs text-success">{message}</span>}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-3 text-sm">
      <p className="text-muted-foreground">
        Hệ thống ghi nhận {onHand} sản phẩm trong kho, trong đó {item.reserved_quantity} đang giữ
        cho đơn chờ thanh toán. Nhập số thực tế đếm được (từ {item.reserved_quantity} đến {onHand}).
        Muốn tăng tồn kho, hãy gửi yêu cầu bổ sung.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          type="number"
          min={item.reserved_quantity}
          max={onHand}
          value={counted}
          onChange={(e) => setCounted(e.target.value)}
          placeholder="Số đếm được"
          aria-label="Số lượng thực tế đếm được"
          className="w-32"
        />
        <Input
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          maxLength={500}
          placeholder="Lý do (hàng hỏng, thất lạc…)"
          aria-label="Lý do kiểm kê"
          className="min-w-48 flex-1"
        />
      </div>
      <div className="flex gap-2">
        <Button size="sm" onClick={submit} disabled={!valid || saving}>
          {saving ? "Đang ghi…" : "Ghi kiểm kê"}
        </Button>
        <Button size="sm" variant="ghost" disabled={saving} onClick={() => setOpen(false)}>
          Hủy
        </Button>
      </div>
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}
