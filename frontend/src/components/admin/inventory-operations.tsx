"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAuth } from "@/lib/auth-context";
import * as api from "@/lib/api-client";
import { describeApiError } from "@/lib/errors";

const labels: Record<string, string> = {
  held: "Đơn đang giữ hàng",
  overdue: "Hold quá hạn",
  legacy_held: "Hold cũ cần đối soát",
  expiry_parked: "Expiry hết lượt retry",
  events_pending: "Sự kiện chờ gửi",
  events_parked: "Sự kiện cần xử lý",
  order_mismatches: "Lệch trạng thái Order",
  reserved_mismatches: "Lệch số lượng giữ hàng",
  cache_pending: "Chờ làm mới cache",
  cache_parked: "Cache hết lượt retry",
};

export function InventoryOperations() {
  const { callWithAuth } = useAuth();
  const client = useQueryClient();
  const [page, setPage] = useState(0);
  const [orderId, setOrderId] = useState("");
  const [action, setAction] = useState("replay");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const stats = useQuery({
    queryKey: ["inventory-operations"],
    queryFn: () => callWithAuth(api.getInventoryOperations),
    refetchInterval: 15000,
  });
  const issues = useQuery({
    queryKey: ["inventory-issues", page],
    queryFn: () => callWithAuth((token) => api.getInventoryIssues(token, page * 20)),
    refetchInterval: 15000,
  });
  async function repair(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setMessage(null);
    try {
      await callWithAuth((token) =>
        api.repairInventoryOperation(token, {
          order_id: orderId.trim(),
          action,
          reason: reason.trim(),
        }),
      );
      await Promise.all([
        client.invalidateQueries({ queryKey: ["inventory-operations"] }),
        client.invalidateQueries({ queryKey: ["inventory-issues"] }),
      ]);
      setMessage("Đã xử lý yêu cầu và ghi audit.");
    } catch (error) {
      setMessage(describeApiError(error, "Không thể xử lý operation."));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="space-y-4 rounded-xl border p-5">
      <h2 className="text-lg font-semibold">Đối soát tồn kho</h2>
      <p className="text-sm text-muted-foreground">
        Hold cũ cần kiểm tra với Order trước khi xử lý. Hàng đã commit không được giải phóng qua
        công cụ này.
      </p>
      {(stats.isPending || issues.isPending) && <p role="status">Đang tải…</p>}
      {(stats.isError || issues.isError) && (
        <div role="alert">
          Không thể tải dữ liệu.{" "}
          <Button
            variant="outline"
            onClick={() => {
              stats.refetch();
              issues.refetch();
            }}
          >
            Thử lại
          </Button>
        </div>
      )}
      {stats.data && (
        <>
          <p className="text-sm">
            Tự xử lý hết hạn: {stats.data.expiry_enabled === 1 ? "đã bật" : "chưa bật"}.
          </p>
          <dl className="grid grid-cols-2 gap-3 md:grid-cols-5">
            {Object.entries(labels).map(([key, label]) => (
              <div key={key} className="rounded border p-3">
                <dt className="text-xs text-muted-foreground">{label}</dt>
                <dd className="text-xl font-semibold">{stats.data?.[key] ?? 0}</dd>
              </div>
            ))}
          </dl>
        </>
      )}
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead>
            <tr>
              <th>Order</th>
              <th>Reservation</th>
              <th>Vấn đề</th>
            </tr>
          </thead>
          <tbody>
            {issues.data?.map((issue) => (
              <tr key={issue.order_id} className="border-t">
                <td>
                  <button
                    type="button"
                    className="py-2 text-primary underline"
                    onClick={() => setOrderId(issue.order_id)}
                  >
                    {issue.order_id}
                  </button>
                </td>
                <td>{issue.status}</td>
                <td>{issue.issue}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {issues.data?.length === 0 && (
        <p className="text-sm text-muted-foreground">
          Không có trường hợp cần xử lý trên trang này.
        </p>
      )}
      <nav className="flex items-center gap-3" aria-label="Phân trang đối soát">
        <Button
          variant="outline"
          disabled={page === 0 || issues.isFetching}
          onClick={() => setPage(page - 1)}
        >
          Trước
        </Button>
        <span>Trang {page + 1}</span>
        <Button
          variant="outline"
          disabled={
            issues.isFetching || issues.isError || (issues.data?.length ?? 0) < 20 || page >= 500
          }
          onClick={() => setPage(page + 1)}
        >
          Sau
        </Button>
      </nav>
      <form className="flex flex-col gap-3" onSubmit={repair}>
        <label className="text-sm">
          Order ID
          <Input value={orderId} onChange={(e) => setOrderId(e.target.value)} required />
        </label>
        <label className="text-sm">
          Thao tác
          <select
            className="ml-2 rounded border p-2"
            value={action}
            onChange={(e) => setAction(e.target.value)}
          >
            <option value="replay">Gửi lại sự kiện / thử expiry lại</option>
            <option value="release_cancelled">Giải phóng hold của Order đã hủy</option>
            <option value="adopt_legacy">Đưa hold cũ còn hạn vào worker</option>
          </select>
        </label>
        <label className="text-sm">
          Lý do
          <Input
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            maxLength={500}
            required
          />
        </label>
        <Button disabled={busy || !reason.trim() || !orderId.trim()} className="self-start">
          {busy ? "Đang xử lý…" : "Thực hiện và ghi audit"}
        </Button>
        {message && (
          <p role="status" className="text-sm">
            {message}
          </p>
        )}
      </form>
    </section>
  );
}
