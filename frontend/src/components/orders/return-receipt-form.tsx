"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { receiptError, returnShippingErrorMessage } from "@/lib/return-shipping";

const LABELS = {
  vendor: {
    open: "Ghi nhận hàng trả",
    hint: "Ghi từng sản phẩm: bán lại được, hỏng hoặc thiếu. Hàng bán được nhập lại kho; nếu có hàng hỏng/thiếu, sàn quyết định hoàn tiền.",
    sellable: "Bán lại được",
    damaged: "Hỏng",
    missing: "Thiếu",
    note: "Ghi chú kiểm tra",
    submit: "Lưu",
    cancel: "Đóng",
    failed: "Không ghi nhận được hàng trả.",
  },
  admin: {
    open: "Record goods",
    hint: "Every unit: sellable, damaged or missing. Sellable units go back to stock; damaged or missing goods wait for a refund decision.",
    sellable: "Sellable",
    damaged: "Damaged",
    missing: "Missing",
    note: "Inspection note",
    submit: "Save",
    cancel: "Close",
    failed: "Could not record the goods.",
  },
};

// ReturnReceiptForm records what came back for a return (AF-05): every
// unit sellable, damaged or missing. All sellable starts the refund; a
// damaged or missing unit waits for an admin (no silent deduction).
export function ReturnReceiptForm({
  returnRequest: r,
  scope,
  onDone,
}: {
  returnRequest: api.ReturnRequest;
  scope: "vendor" | "admin";
  onDone: () => void | Promise<void>;
}) {
  const t = LABELS[scope];
  const { callWithAuth } = useAuth();
  const [open, setOpen] = useState(false);
  const [counts, setCounts] = useState({ sellable: r.quantity, damaged: 0, missing: 0 });
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!open) {
    return (
      <Button size="sm" onClick={() => setOpen(true)}>
        {t.open}
      </Button>
    );
  }
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const problem = receiptError(r.quantity, counts);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await callWithAuth((token) =>
        api.recordReturnGoodsReceipt(token, scope, r.id, {
          sellable_quantity: counts.sellable,
          damaged_quantity: counts.damaged,
          missing_quantity: counts.missing,
          note: note.trim() || undefined,
          expected_version: r.version,
        }),
      );
      setOpen(false);
      await onDone();
    } catch (err) {
      setError(returnShippingErrorMessage(err, t.failed));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={submit} className="flex w-full flex-col gap-2 rounded-md border p-2 text-xs">
      <p className="text-muted-foreground">{t.hint}</p>
      <div className="flex flex-wrap gap-2">
        {(["sellable", "damaged", "missing"] as const).map((k) => (
          <label key={k} className="flex items-center gap-1">
            {t[k]}
            <Input
              type="number"
              min={0}
              max={r.quantity}
              className="h-7 w-16"
              value={counts[k]}
              onChange={(e) => setCounts((c) => ({ ...c, [k]: Number(e.target.value) }))}
            />
          </label>
        ))}
      </div>
      <Textarea
        rows={2}
        maxLength={1000}
        placeholder={t.note}
        value={note}
        onChange={(e) => setNote(e.target.value)}
      />
      {error && <p className="text-destructive">{error}</p>}
      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={busy}>
          {t.submit}
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={() => setOpen(false)}>
          {t.cancel}
        </Button>
      </div>
    </form>
  );
}
