"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import * as bulk from "@/lib/bulk-operations";

// AF-16 operations view: imports of every shop, with a stop that keeps
// applied rows (rollback = stop new work and drain, never delete products).
export function BulkImportJobsAdmin() {
  const { callWithAuth } = useAuth();
  const client = useQueryClient();
  const [owner, setOwner] = useState<bulk.BulkOwner>("catalog");
  const [status, setStatus] = useState("applying");
  const [cursor, setCursor] = useState("");
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState("");
  const jobs = useQuery({
    queryKey: ["bulk-admin-jobs", owner, status, cursor],
    queryFn: () => callWithAuth((t) => bulk.adminJobs(t, owner, status, cursor)),
    refetchInterval: 15000,
    retry: false,
  });
  async function stop(job: bulk.BulkJob) {
    setMessage("");
    try {
      await callWithAuth((t) => bulk.adminCancel(t, owner, job.id, reason));
      setMessage("Đã dừng lượt nhập; các dòng đã áp dụng được giữ nguyên.");
      await client.invalidateQueries({ queryKey: ["bulk-admin-jobs"] });
    } catch (e) {
      setMessage(describeApiError(e, "Không dừng được lượt nhập."));
    }
  }
  return (
    <section className="space-y-3">
      <h2 className="text-lg font-semibold">Nhập hàng loạt của các shop</h2>
      <div className="flex flex-wrap gap-2">
        <label>
          Dịch vụ
          <select
            className="ml-2 rounded border bg-background p-1"
            value={owner}
            onChange={(e) => {
              setOwner(e.target.value as bulk.BulkOwner);
              setCursor("");
            }}
          >
            <option value="catalog">Sản phẩm/giá (Catalog)</option>
            <option value="inventory">Nhập kho (Inventory)</option>
          </select>
        </label>
        <label>
          Trạng thái
          <select
            className="ml-2 rounded border bg-background p-1"
            value={status}
            onChange={(e) => {
              setStatus(e.target.value);
              setCursor("");
            }}
          >
            <option value="">Tất cả</option>
            {Object.entries(bulk.JOB_STATUS_LABELS).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </label>
      </div>
      <label className="block">
        Lý do dừng (ghi audit)
        <Input value={reason} onChange={(e) => setReason(e.target.value)} />
      </label>
      {jobs.error && (
        <p role="alert">{describeApiError(jobs.error, "Không tải được danh sách.")}</p>
      )}
      {jobs.data?.items.length === 0 && <p>Không có lượt nhập nào.</p>}
      <ul className="space-y-2">
        {jobs.data?.items.map((j) => (
          <li key={j.id} className="rounded border p-3 text-sm">
            <p>
              Shop {j.vendor_id.slice(0, 8)} · {j.kind} · {bulk.JOB_STATUS_LABELS[j.status]}
              {j.outcome && ` · ${bulk.OUTCOME_LABELS[j.outcome]}`}
            </p>
            <p>
              {j.summary.total} dòng · đã áp dụng {j.summary.succeeded} · chờ {j.summary.pending} ·
              thất bại {j.summary.failed} · đã dừng {j.summary.cancelled}
            </p>
            {bulk.canCancel(j) && (
              <Button
                variant="destructive"
                disabled={reason.trim().length < 3}
                onClick={() => void stop(j)}
              >
                Dừng lượt nhập
              </Button>
            )}
          </li>
        ))}
      </ul>
      <div className="flex gap-2">
        <Button variant="outline" disabled={!cursor} onClick={() => setCursor("")}>
          Mới nhất
        </Button>
        <Button
          variant="outline"
          disabled={!jobs.data?.next_cursor}
          onClick={() => setCursor(jobs.data!.next_cursor!)}
        >
          Cũ hơn
        </Button>
      </div>
      {message && <p role="status">{message}</p>}
    </section>
  );
}
