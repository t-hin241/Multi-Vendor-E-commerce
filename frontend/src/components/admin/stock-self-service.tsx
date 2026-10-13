"use client";

import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { isOutcomeUnknown, newOperationId } from "@/lib/api-client";
import * as stock from "@/lib/stock-self-service";

export function StockSelfServiceAdmin() {
  const { callWithAuth } = useAuth();
  const client = useQueryClient();
  const [vendor, setVendor] = useState("");
  const [selectedVendor, setSelectedVendor] = useState("");
  const [reason, setReason] = useState("");
  const [limit, setLimit] = useState("10000");
  const [cursor, setCursor] = useState("");
  const [alertCursor, setAlertCursor] = useState("");
  const [busy, setBusy] = useState(false);
  const [unknown, setUnknown] = useState(false);
  const [message, setMessage] = useState("");
  const pending = useRef<(() => Promise<unknown>) | null>(null);
  const policy = useQuery({
    queryKey: ["stock-admin-policy", selectedVendor],
    queryFn: () => callWithAuth((t) => stock.policy(t, selectedVendor, true)),
    enabled: !!selectedVendor,
  });
  const work = useQuery({
    queryKey: ["stock-admin-work", cursor],
    queryFn: () => callWithAuth((t) => stock.work(t, cursor)),
    refetchInterval: 15000,
  });
  const alerts = useQuery({
    queryKey: ["stock-admin-alerts", alertCursor],
    queryFn: () => callWithAuth((t) => stock.alertWork(t, alertCursor)),
    refetchInterval: 15000,
  });
  async function run(fn?: () => Promise<unknown>) {
    if (busy) return;
    if (fn) pending.current = fn;
    if (!pending.current) return;
    setBusy(true);
    try {
      await pending.current();
      pending.current = null;
      setUnknown(false);
      setMessage("Đã xử lý và ghi audit.");
      await Promise.all([
        client.invalidateQueries({ queryKey: ["stock-admin-work"] }),
        client.invalidateQueries({ queryKey: ["stock-admin-policy"] }),
        client.invalidateQueries({ queryKey: ["stock-policy"] }),
        client.invalidateQueries({ queryKey: ["stock-admin-alerts"] }),
      ]);
    } catch (e) {
      const uncertain = isOutcomeUnknown(e);
      setUnknown(uncertain);
      if (!uncertain) pending.current = null;
      setMessage(describeApiError(e, "Không thể xử lý kho."));
    } finally {
      setBusy(false);
    }
  }
  function changePolicy(enabled: boolean) {
    if (!policy.data) return;
    const key = newOperationId();
    const input = {
      enabled,
      limits: { receipt: Number(limit) },
      reason,
      expected_version: policy.data.version,
    };
    run(() => callWithAuth((t) => stock.setPolicy(t, key, selectedVendor, input)));
  }
  function decide(item: stock.Work, decision: string) {
    const key = newOperationId();
    run(() => callWithAuth((t) => stock.decide(t, key, item, decision, reason)));
  }
  return (
    <section className="space-y-4 rounded-xl border p-5">
      <h2 className="text-lg font-semibold">Tồn kho tự phục vụ</h2>
      <p>
        Chỉ bật cho shop đã xác minh tự giữ kho. Hàng trả phải qua kiểm tra và luồng thu hồi riêng.
      </p>
      <fieldset disabled={busy || unknown} className="space-y-3">
        <Input
          aria-label="Mã shop quản lý tồn kho"
          placeholder="Mã shop"
          value={vendor}
          onChange={(e) => setVendor(e.target.value)}
        />
        <Button variant="outline" onClick={() => setSelectedVendor(vendor.trim())}>
          Tra chính sách
        </Button>
        {policy.error && (
          <p role="alert">Không thể đọc chính sách hoặc bạn chưa có quyền cấu hình.</p>
        )}
        {policy.data && (
          <p>
            Shop {selectedVendor}: {policy.data.enabled ? "Đã bật" : "Đang tắt"}; phiên bản{" "}
            {policy.data.version}. Cờ toàn hệ thống: {policy.data.feature_enabled ? "bật" : "tắt"}.
          </p>
        )}
        <Input
          aria-label="Hạn mức nhập mỗi phiếu"
          type="number"
          min="1"
          max="10000"
          value={limit}
          onChange={(e) => setLimit(e.target.value)}
        />
        <Input
          aria-label="Lý do thao tác kho admin"
          placeholder="Lý do quyết định / kết quả kiểm tra thiếu hàng"
          value={reason}
          maxLength={500}
          onChange={(e) => setReason(e.target.value)}
        />
        <Button
          disabled={
            !policy.data ||
            reason.trim().length < 3 ||
            !Number.isSafeInteger(Number(limit)) ||
            Number(limit) < 1 ||
            Number(limit) > 10000
          }
          onClick={() => changePolicy(true)}
        >
          Bật / cập nhật hạn mức
        </Button>
        <Button
          variant="outline"
          disabled={!policy.data || reason.trim().length < 3}
          onClick={() => changePolicy(false)}
        >
          Thu hồi quyền tự phục vụ
        </Button>
        <h3 className="font-medium">Phiếu cần xử lý</h3>
        {work.isPending && <p>Đang tải…</p>}
        {work.error && <p role="alert">Không thể tải hàng chờ.</p>}
        {work.data?.items.length === 0 && <p>Không có phiếu chờ xử lý.</p>}
        {work.data?.items.map((w) => (
          <div key={w.id} className="space-y-2 rounded border p-3">
            <p>
              Shop {w.vendor_id} · Kho {w.item_id} ·{" "}
              {w.status === "approval_required" ? "Vượt hạn mức, chờ duyệt" : "Thiếu hàng đang giữ"}
            </p>
            <p>
              {w.kind} · {w.quantity_delta} · {w.reason} · Chứng từ {w.evidence_ref}
            </p>
            {w.physical_count !== undefined && <p>Số đếm thực tế: {w.physical_count}</p>}
            {w.first_approved_by && <p>Đã có duyệt lần đầu; cần admin khác duyệt lần hai.</p>}
            {w.status === "approval_required" ? (
              <>
                <Button disabled={reason.trim().length < 3} onClick={() => decide(w, "approve")}>
                  Duyệt phiếu
                </Button>
                <Button
                  variant="outline"
                  disabled={reason.trim().length < 3}
                  onClick={() => decide(w, "reject")}
                >
                  Từ chối / yêu cầu kiểm kê lại
                </Button>
              </>
            ) : (
              <Button
                variant="outline"
                disabled={reason.trim().length < 3}
                onClick={() => decide(w, "resolve")}
              >
                Ghi kết quả kiểm tra thiếu hàng
              </Button>
            )}
          </div>
        ))}
        {cursor && (
          <Button variant="outline" onClick={() => setCursor("")}>
            Mới nhất
          </Button>
        )}
        {work.data?.next_cursor && (
          <Button variant="outline" onClick={() => setCursor(work.data!.next_cursor!)}>
            Xem tiếp
          </Button>
        )}
      </fieldset>
      <h3 className="font-medium">Cảnh báo kho cần gửi lại</h3>
      {alerts.error && <p>Không tải được cảnh báo cần xử lý.</p>}
      {alerts.data?.items.map((a) => (
        <div key={a.id} className="rounded border p-3">
          <p>
            Shop {a.vendor_id} · Kho {a.item_id} · {a.attempts} lượt gửi
          </p>
          <Button
            disabled={busy || unknown || reason.trim().length < 3}
            onClick={() => {
              const key = newOperationId();
              run(() => callWithAuth((t) => stock.retryAlert(t, key, a.id, reason)));
            }}
          >
            Gửi lại cảnh báo
          </Button>
        </div>
      ))}
      {alertCursor && (
        <Button variant="outline" onClick={() => setAlertCursor("")}>
          Cảnh báo mới nhất
        </Button>
      )}
      {alerts.data?.next_cursor && (
        <Button variant="outline" onClick={() => setAlertCursor(alerts.data!.next_cursor!)}>
          Xem thêm cảnh báo
        </Button>
      )}
      {unknown && (
        <Button disabled={busy} onClick={() => run()}>
          Thử lại cùng thao tác
        </Button>
      )}
      {message && <p role="status">{message}</p>}
    </section>
  );
}
