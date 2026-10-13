"use client";

import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAuth } from "@/lib/auth-context";
import { useConsoleShops } from "@/lib/hooks/use-console-shops";
import { describeApiError } from "@/lib/errors";
import { isOutcomeUnknown, newOperationId, type InventoryItem } from "@/lib/api-client";
import * as stock from "@/lib/stock-self-service";

export function StockSelfService({
  item,
  onChanged,
}: {
  item: InventoryItem;
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const client = useQueryClient();
  const shops = useConsoleShops();
  const canAdjust = shops.shops
    .find((s) => s.vendor_id === item.vendor_id)
    ?.capabilities.includes("inventory.adjust");
  const policy = useQuery({
    queryKey: ["stock-policy", item.vendor_id],
    queryFn: () => callWithAuth((t) => stock.policy(t, item.vendor_id)),
  });
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<stock.Adjustment["kind"]>("receipt");
  const [quantity, setQuantity] = useState("");
  const [reason, setReason] = useState("");
  const [evidence, setEvidence] = useState("");
  const [threshold, setThreshold] = useState(String(item.low_stock_threshold ?? 0));
  const [enabled, setEnabled] = useState(item.alert_enabled ?? false);
  const [cursor, setCursor] = useState("");
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [alertUncertain, setAlertUncertain] = useState(false);
  const [message, setMessage] = useState("");
  // Retain the exact payload and key after a timeout, including the old
  // version. Never silently retry against newly fetched stock.
  const pending = useRef<{ key: string; input: stock.Adjustment } | null>(null);
  const pendingAlert = useRef<{
    key: string;
    input: { enabled: boolean; low_stock_threshold: number; expected_version: number };
  } | null>(null);
  const history = useQuery({
    queryKey: ["stock-history", item.id, cursor],
    queryFn: () => callWithAuth((t) => stock.history(t, item.id, cursor)),
    enabled: open,
  });
  async function refresh() {
    onChanged();
    await client.invalidateQueries({ queryKey: ["stock-history", item.id] });
  }
  async function submit() {
    if (busy) return;
    const n = Number(quantity);
    if (!pending.current && (!Number.isSafeInteger(n) || quantity === "")) {
      setMessage("Nhập số lượng nguyên trong phạm vi hỗ trợ.");
      return;
    }
    if (!pending.current)
      pending.current = {
        key: newOperationId(),
        input: {
          item_id: item.id,
          kind,
          quantity_delta: kind === "count" ? 0 : n,
          ...(kind === "count" ? { physical_count: n } : {}),
          reason,
          evidence_ref: evidence,
          expected_version: item.version ?? 0,
        },
      };
    setBusy(true);
    setMessage("");
    try {
      const p = pending.current;
      const receipt = await callWithAuth((t) => stock.adjust(t, p.key, p.input));
      setMessage(
        receipt.status === "approval_required"
          ? `Đang chờ admin duyệt. Mã phiếu: ${receipt.operation_id}`
          : `Đã ghi nhận. Tồn khả dụng: ${receipt.available}.`,
      );
      pending.current = null;
      setUncertain(false);
      setQuantity("");
      await refresh();
    } catch (e) {
      const unknown = isOutcomeUnknown(e);
      setUncertain(unknown);
      if (!unknown) {
        pending.current = null;
        await refresh();
      }
      setMessage(
        describeApiError(e, "Không thể ghi nhận kho.") +
          (unknown ? " Bấm thử lại để kiểm tra cùng phiếu." : ""),
      );
    } finally {
      setBusy(false);
    }
  }
  async function saveAlerts() {
    if (busy) return;
    if (!pendingAlert.current)
      pendingAlert.current = {
        key: newOperationId(),
        input: {
          enabled,
          low_stock_threshold: Number(threshold),
          expected_version: item.version ?? 0,
        },
      };
    setBusy(true);
    try {
      const p = pendingAlert.current;
      await callWithAuth((t) => stock.alerts(t, p.key, item.id, p.input));
      pendingAlert.current = null;
      setAlertUncertain(false);
      setMessage("Đã lưu cảnh báo.");
      await refresh();
    } catch (e) {
      setAlertUncertain(isOutcomeUnknown(e));
      if (!isOutcomeUnknown(e)) pendingAlert.current = null;
      setAlertUncertain(false);
      setMessage(describeApiError(e, "Không thể lưu cảnh báo."));
    } finally {
      setBusy(false);
    }
  }
  const active = policy.data?.feature_enabled && policy.data.enabled;
  return (
    <div className="space-y-2 rounded border p-3 text-sm">
      <Button size="sm" variant="outline" onClick={() => setOpen(!open)}>
        Điều chỉnh kho và lịch sử
      </Button>
      {open && (
        <>
          {policy.error && <p role="alert">Không tải được chính sách tồn kho.</p>}
          {active && canAdjust ? (
            <div className="space-y-2">
              <p>
                Nhập tối đa {policy.data?.limits.receipt} đơn vị mỗi phiếu; vượt hạn mức sẽ chờ
                duyệt. Hàng trả cần đi qua kiểm tra hàng hoàn.
              </p>
              <fieldset disabled={busy || uncertain || alertUncertain} className="space-y-2">
                <select
                  aria-label="Loại điều chỉnh"
                  value={kind}
                  onChange={(e) => setKind(e.target.value as stock.Adjustment["kind"])}
                >
                  <option value="receipt">Nhập hàng</option>
                  <option value="damage">Hàng hỏng (số âm)</option>
                  <option value="correction">Điều chỉnh chênh lệch</option>
                  <option value="count">Kiểm kê thực tế (gồm hàng giữ)</option>
                </select>
                <Input
                  aria-label="Số lượng điều chỉnh"
                  type="number"
                  step="1"
                  value={quantity}
                  onChange={(e) => setQuantity(e.target.value)}
                />
                <Input
                  aria-label="Lý do điều chỉnh"
                  placeholder="Lý do"
                  maxLength={500}
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                />
                <Input
                  aria-label="Mã chứng từ"
                  placeholder="Mã chứng từ nhập / kiểm kê"
                  maxLength={120}
                  value={evidence}
                  onChange={(e) => setEvidence(e.target.value)}
                />
              </fieldset>
              <Button
                size="sm"
                disabled={
                  busy ||
                  alertUncertain ||
                  (!uncertain && (!quantity || reason.trim().length < 3 || !evidence.trim()))
                }
                onClick={submit}
              >
                {uncertain ? "Thử lại cùng phiếu" : "Ghi nhận phiếu"}
              </Button>
              <fieldset
                disabled={busy || uncertain || alertUncertain}
                className="flex flex-wrap gap-2"
              >
                <label>
                  <input
                    type="checkbox"
                    checked={enabled}
                    onChange={(e) => setEnabled(e.target.checked)}
                  />{" "}
                  Cảnh báo tồn thấp
                </label>
                <Input
                  aria-label="Ngưỡng tồn thấp"
                  type="number"
                  min="0"
                  value={threshold}
                  onChange={(e) => setThreshold(e.target.value)}
                />
              </fieldset>
              <Button
                size="sm"
                variant="outline"
                disabled={
                  busy ||
                  uncertain ||
                  !Number.isSafeInteger(Number(threshold)) ||
                  Number(threshold) < 0
                }
                onClick={saveAlerts}
              >
                Lưu / thử lại cảnh báo
              </Button>
            </div>
          ) : (
            <p>
              Shop chưa được bật tự điều chỉnh kho hoặc bạn chưa có quyền. Bổ sung kho qua yêu cầu
              duyệt hiện có.
            </p>
          )}
          {message && <p role="status">{message}</p>}
          <h4 className="font-medium">Lịch sử kho</h4>
          {history.isPending && <p>Đang tải…</p>}
          {history.error && <p role="alert">Không tải được lịch sử kho.</p>}
          {history.data?.items.length === 0 && <p>Chưa có biến động.</p>}
          <ul>
            {history.data?.items.map((m) => (
              <li key={m.id} className="border-b py-2">
                {new Date(m.created_at).toLocaleString("vi-VN", { timeZone: "Asia/Saigon" })} ·{" "}
                {m.kind} · {m.quantity_delta > 0 ? "+" : ""}
                {m.quantity_delta} · {m.reason} · {m.evidence_ref}
              </li>
            ))}
          </ul>
          {cursor && (
            <Button size="sm" variant="outline" onClick={() => setCursor("")}>
              Mới nhất
            </Button>
          )}
          {history.data?.next_cursor && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => setCursor(history.data!.next_cursor!)}
            >
              Xem tiếp
            </Button>
          )}
        </>
      )}
    </div>
  );
}
