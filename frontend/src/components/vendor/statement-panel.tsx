"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAuth } from "@/lib/auth-context";
import { newOperationId } from "@/lib/api-client";
import { formatMoney } from "@/lib/format";
import * as api from "@/lib/vendor-statements";

const labels = {
  opening: "Số dư đầu kỳ",
  credit: "Ghi có trong kỳ",
  debit: "Ghi nợ trong kỳ",
  closing: "Số dư cuối kỳ",
  held: "Đang giữ",
  not_eligible: "Chưa đến hạn",
  payable: "Có thể đối soát",
  in_payout: "Đang chờ chuyển khoản",
};
const date = (value: string) =>
  new Date(value).toLocaleString("vi-VN", { timeZone: "Asia/Ho_Chi_Minh" });

export function StatementPanel({ vendorId, admin = false }: { vendorId: string; admin?: boolean }) {
  const { callWithAuth, user } = useAuth();
  const [from, setFrom] = useState(() =>
    new Date(Date.now() - 29 * 86400000).toISOString().slice(0, 10),
  );
  const [to, setTo] = useState(() => new Date().toISOString().slice(0, 10));
  const [cursor, setCursor] = useState("");
  const [operation, setOperation] = useState("");
  const [operationInput, setOperationInput] = useState("");
  const [exportKey, setExportKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [payoutId, setPayoutId] = useState("");
  const period = api.statementPeriod(from, to);
  const scope = ["statements", user?.id, vendorId, admin];
  const capability = useQuery({
    queryKey: [...scope, "capability"],
    queryFn: () => callWithAuth((t) => api.statementCapability(t, vendorId, admin)),
    retry: false,
  });
  const enabled = capability.data?.statements_enabled && !capability.isError;
  const statement = useQuery({
    queryKey: [...scope, from, to, cursor],
    queryFn: () => callWithAuth((t) => api.readStatement(t, vendorId, period!, cursor, admin)),
    enabled: Boolean(enabled && period),
    retry: false,
  });
  const exported = useQuery({
    queryKey: [...scope, "export", operation],
    queryFn: () => callWithAuth((t) => api.getStatementExport(t, vendorId, operation)),
    enabled: Boolean(operation && !admin),
    retry: false,
    refetchInterval: (q) =>
      ["pending", "processing"].includes(q.state.data?.status ?? "") ? 3000 : false,
  });
  const payout = useQuery({
    queryKey: [...scope, "payout", payoutId],
    queryFn: () => callWithAuth((t) => api.statementPayout(t, vendorId, payoutId, admin)),
    enabled: Boolean(payoutId && enabled),
    retry: false,
  });
  const data = enabled && !statement.isError ? statement.data : undefined;
  async function exportCSV() {
    if (!period) return;
    setBusy(true);
    setError("");
    const key = exportKey || newOperationId();
    setExportKey(key);
    try {
      const op = await callWithAuth((t) => api.createStatementExport(t, vendorId, period, key));
      setOperation(op.operation_id);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Không tạo được bản xuất. Hãy thử lại.");
    } finally {
      setBusy(false);
    }
  }
  async function download() {
    setBusy(true);
    setError("");
    try {
      const link = await callWithAuth((t) => api.statementDownload(t, vendorId, operation));
      const anchor = document.createElement("a");
      anchor.href = link.url;
      anchor.rel = "noreferrer";
      anchor.referrerPolicy = "no-referrer";
      anchor.click();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Không tải được bản xuất.");
    } finally {
      setBusy(false);
    }
  }
  function changePeriod(value: string, end: boolean) {
    if (end) setTo(value);
    else setFrom(value);
    setCursor("");
    setExportKey("");
    setPayoutId("");
  }
  return (
    <section className="space-y-4">
      <h2 className="text-xl font-semibold">Sao kê tài chính · VND</h2>
      <p className="text-sm text-muted-foreground">
        Nguồn: sổ đối soát Payment. Số dư đầu kỳ + ghi có − ghi nợ = số dư cuối kỳ. Tiền đã thu của
        người mua chưa đồng nghĩa với tiền shop được nhận. Đây không phải hóa đơn thuế hay phần mềm
        kế toán.
      </p>
      {capability.isPending && <p>Đang kiểm tra quyền truy cập…</p>}
      {capability.error && <p role="alert">{capability.error.message}</p>}
      {capability.data && !enabled && (
        <p>Tính năng sao kê chưa được bật. Bản xuất còn hạn vẫn có thể tải bằng mã bên dưới.</p>
      )}
      {enabled && (
        <>
          <div className="flex flex-wrap gap-3">
            <label>
              Từ ngày
              <Input
                type="date"
                value={from}
                onChange={(e) => changePeriod(e.target.value, false)}
              />
            </label>
            <label>
              Đến ngày
              <Input type="date" value={to} onChange={(e) => changePeriod(e.target.value, true)} />
            </label>
          </div>
          <p className="text-sm">Ngày Việt Nam, lọc theo thời điểm ghi nhận; tối đa 366 ngày.</p>
          {!period && <p role="alert">Chọn khoảng ngày hợp lệ, tối đa 366 ngày.</p>}
          {period && statement.isPending && <p>Đang tải sao kê…</p>}
          {statement.error && <p role="alert">{statement.error.message}</p>}
          {data && (
            <>
              {!data.balances_available && (
                <p role="alert">
                  Có {data.gap_count} khoản lịch sử thiếu loại tiền. Số dư chưa khả dụng; cần đối
                  soát trước khi xuất.
                </p>
              )}
              <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                {Object.entries(labels).map(([key, label]) => (
                  <div key={key} className="rounded border p-3">
                    <dt className="text-sm">{label}</dt>
                    <dd className="font-semibold">
                      {data.balances_available
                        ? formatMoney(data.balances[key as keyof typeof labels], "VND")
                        : "Chưa khả dụng"}
                    </dd>
                  </div>
                ))}
              </dl>
              <p className="text-xs text-muted-foreground">
                Khoản giữ, chưa đến hạn, có thể đối soát và chờ chuyển khoản là tại{" "}
                {date(data.as_of)}, không phải cuối kỳ. Số âm được chuyển sang kỳ sau. Ghi nhận gần
                nhất: {data.watermark ? date(data.watermark) : "Chưa có"}. Giao dịch đến muộn xuất
                hiện trong kỳ ghi nhận mới.
              </p>
              <div className="overflow-x-auto">
                <table className="w-full text-left text-sm">
                  <caption className="sr-only">Các dòng sao kê</caption>
                  <thead>
                    <tr>
                      <th>Ghi nhận</th>
                      <th>Loại</th>
                      <th>Số tiền</th>
                      <th>Tham chiếu</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.entries.map((e) => (
                      <tr key={e.id} className="border-t">
                        <td className="py-3">{date(e.recorded_at)}</td>
                        <td>{e.type}</td>
                        <td>{formatMoney(e.amount, e.currency)}</td>
                        <td>
                          <details>
                            <summary className="cursor-pointer">Xem chi tiết</summary>
                            <p>Dòng: {e.id}</p>
                            <p>Đơn shop: {e.vendor_order_id ?? "—"}</p>
                            <p>Tham chiếu: {e.source_ref}</p>
                            <p>
                              Phát sinh: {date(e.occurred_at)}
                              {e.occurred_at_estimated
                                ? " (dùng ngày ghi nhận do lịch sử thiếu thời điểm)"
                                : ""}
                            </p>
                            <p>Đủ điều kiện từ: {date(e.eligible_at)}</p>
                            {e.source_ref.startsWith("payout_item:") && (
                              <Button
                                variant="outline"
                                onClick={() => setPayoutId(e.source_ref.slice(12))}
                              >
                                Xem chuyển khoản
                              </Button>
                            )}
                          </details>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {data.entries.length === 0 && (
                <p>Không có phát sinh trong kỳ; số dư đầu kỳ vẫn được giữ.</p>
              )}
              <div className="flex gap-2">
                <Button variant="outline" disabled={!cursor} onClick={() => setCursor("")}>
                  Về đầu kỳ
                </Button>
                <Button
                  variant="outline"
                  disabled={!data.next_cursor}
                  onClick={() => setCursor(data.next_cursor!)}
                >
                  Trang tiếp
                </Button>
                <Button
                  variant="outline"
                  onClick={() => {
                    setCursor("");
                    void statement.refetch();
                  }}
                >
                  Làm mới
                </Button>
              </div>
              {!admin && (
                <Button
                  disabled={busy || !data.balances_available}
                  onClick={() => void exportCSV()}
                >
                  Xuất CSV
                </Button>
              )}
            </>
          )}
          {payout.error && <p role="alert">{payout.error.message}</p>}
          {!payout.isError && payout.data && (
            <div className="rounded border p-3">
              <p>Batch: {payout.data.batch_id}</p>
              <p>
                {formatMoney(payout.data.amount, payout.data.currency)} · {payout.data.status} ·{" "}
                {payout.data.masked_destination}
              </p>
              <details>
                <summary>Các dòng được thanh toán</summary>
                {payout.data.entry_refs.map((id) => (
                  <p key={id}>{id}</p>
                ))}
              </details>
            </div>
          )}
        </>
      )}
      {!admin && (
        <div className="space-y-2 border-t pt-4">
          <p>
            Bản xuất giữ nguyên dữ liệu tại cutoff, tối đa 100.000 dòng. Lưu mã bản xuất để tải lại
            trong thời hạn.
          </p>
          <label>
            Mã bản xuất
            <Input value={operationInput} onChange={(e) => setOperationInput(e.target.value)} />
          </label>
          <Button
            variant="outline"
            disabled={!operationInput.trim()}
            onClick={() => setOperation(operationInput.trim())}
          >
            Tra bản xuất
          </Button>
          {operation && <p>Mã: {operation}</p>}
          {exported.error && <p role="alert">{exported.error.message}</p>}
          {!exported.isError && exported.data && (
            <>
              <p>
                Trạng thái: {exported.data.status} · Hết hạn: {date(exported.data.expires_at)} ·
                Cutoff: {date(exported.data.snapshot.as_of)}
              </p>
              {exported.data.status === "ready" && (
                <Button disabled={busy} onClick={() => void download()}>
                  Tải CSV (liên kết 60 giây)
                </Button>
              )}
            </>
          )}
        </div>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
