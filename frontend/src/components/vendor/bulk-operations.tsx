"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ApiError, newOperationId } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import type { ConsoleShop } from "@/lib/hooks/use-console-shops";
import { can } from "@/lib/shop-access";
import * as bulk from "@/lib/bulk-operations";

const time = (value?: string) =>
  value ? new Date(value).toLocaleString("vi-VN", { timeZone: "Asia/Ho_Chi_Minh" }) : "—";

function problem(e: unknown, fallback: string) {
  if (e instanceof ApiError && e.status > 0 && e.status < 500)
    return bulk.errorText(e.code, e.message);
  return describeApiError(e, fallback);
}

function openLink(url: string) {
  const a = document.createElement("a");
  a.href = url;
  a.rel = "noreferrer";
  a.referrerPolicy = "no-referrer";
  a.click();
}

export function BulkOperations({ shop }: { shop: ConsoleShop }) {
  const kinds = bulk.BULK_KINDS.filter((k) => can(shop, k.read));
  const [selected, setSelected] = useState<bulk.BulkKind | null>(null);
  const spec = kinds.find((k) => k.kind === selected) ?? kinds[0];
  if (!spec) return <p role="alert">Bạn cần quyền xem sản phẩm hoặc tồn kho của cửa hàng.</p>;
  return (
    <section className="space-y-4">
      <h1 className="text-xl font-semibold">Nhập/xuất hàng loạt</h1>
      <p className="text-sm text-muted-foreground">
        Tải mẫu CSV, điền dữ liệu rồi tải lên. Hệ thống kiểm tra từng dòng trước, không thay đổi gì
        cho đến khi bạn xác nhận. Mỗi lượt một loại dữ liệu, một cửa hàng.
      </p>
      <div className="flex flex-wrap gap-2" role="group" aria-label="Loại dữ liệu">
        {kinds.map((k) => (
          <Button
            key={k.kind}
            variant={k.kind === spec.kind ? "default" : "outline"}
            aria-pressed={k.kind === spec.kind}
            onClick={() => setSelected(k.kind)}
          >
            {k.label}
          </Button>
        ))}
      </div>
      <KindPanel key={`${shop.id}:${spec.kind}`} shop={shop} spec={spec} />
    </section>
  );
}

function KindPanel({ shop, spec }: { shop: ConsoleShop; spec: (typeof bulk.BULK_KINDS)[number] }) {
  const { callWithAuth, user } = useAuth();
  const client = useQueryClient();
  const writable = can(shop, spec.write);
  const scope = ["bulk", user?.id, shop.id, spec.kind];
  const [file, setFile] = useState<File | null>(null);
  const [uploadKey, setUploadKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [jobId, setJobId] = useState("");
  const [cursor, setCursor] = useState("");
  const [exportKey, setExportKey] = useState("");
  const [exportId, setExportId] = useState("");
  const template = useQuery({
    queryKey: [...scope, "template"],
    queryFn: () => callWithAuth((t) => bulk.getTemplate(t, spec.kind, shop.id)),
    retry: false,
  });
  const jobs = useQuery({
    queryKey: [...scope, "jobs", cursor],
    queryFn: () => callWithAuth((t) => bulk.listJobs(t, spec.kind, shop.id, cursor)),
    retry: false,
  });
  const exported = useQuery({
    queryKey: [...scope, "export", exportId],
    queryFn: () => callWithAuth((t) => bulk.getExport(t, spec.owner, exportId)),
    enabled: !!exportId,
    retry: false,
    refetchInterval: (q) =>
      ["pending", "processing"].includes(q.state.data?.status ?? "") ? 2000 : false,
  });
  const t = template.data;
  const myJobs = jobs.data?.items ?? [];

  function downloadTemplate() {
    if (!t) return;
    const url = URL.createObjectURL(new Blob([bulk.templateCsv(t)], { type: "text/csv" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `mau-${t.kind}-v${t.version}.csv`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  async function upload() {
    if (!t || !file || busy) return;
    const refused = bulk.precheck(file, t);
    if (refused) {
      setMessage(refused);
      return;
    }
    // The key stays with this file until the server answers, so a retry
    // after a lost response returns the same job instead of a second one.
    const key = uploadKey || newOperationId();
    setUploadKey(key);
    setBusy(true);
    setMessage("");
    try {
      const job = await callWithAuth((tk) =>
        bulk.uploadJob(tk, key, shop.id, spec.kind, t.version, file),
      );
      setJobId(job.id);
      setUploadKey("");
      setFile(null);
      setMessage("Đã tải lên. Hệ thống đang kiểm tra từng dòng.");
      await client.invalidateQueries({ queryKey: [...scope, "jobs"] });
    } catch (e) {
      if (e instanceof ApiError && e.status > 0 && e.status < 500) setUploadKey("");
      setMessage(problem(e, "Không tải lên được."));
    } finally {
      setBusy(false);
    }
  }
  async function requestExport() {
    const key = exportKey || newOperationId();
    setExportKey(key);
    setBusy(true);
    setMessage("");
    try {
      const e = await callWithAuth((tk) => bulk.createExport(tk, key, shop.id, spec.kind));
      setExportId(e.id);
      setExportKey("");
    } catch (e) {
      setMessage(problem(e, "Không tạo được bản xuất."));
    } finally {
      setBusy(false);
    }
  }
  async function downloadExport() {
    setBusy(true);
    try {
      openLink((await callWithAuth((tk) => bulk.exportLink(tk, spec.owner, exportId))).url);
    } catch (e) {
      setMessage(problem(e, "Không tải được bản xuất."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      {template.isPending && <p>Đang tải mẫu…</p>}
      {template.error && <p role="alert">{problem(template.error, "Không tải được mẫu.")}</p>}
      {t && (
        <div className="space-y-3 rounded border p-4">
          <h2 className="font-semibold">
            Mẫu {spec.label} · phiên bản {t.version}
          </h2>
          {!t.enabled && <p role="alert">Chưa thể nhập mẫu này: {t.disabled_reason}</p>}
          <ul className="list-disc pl-5 text-sm">
            {t.notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
            <li>
              Tối đa {t.max_rows} dòng và {Math.floor(t.max_bytes / 1024 / 1024)} MB mỗi tệp; tiền
              là số nguyên VND, không dấu chấm/phẩy; không công thức, HTML hay đường link.
            </li>
          </ul>
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <caption className="sr-only">Các cột của mẫu</caption>
              <thead>
                <tr>
                  <th>Cột</th>
                  <th>Bắt buộc</th>
                  <th>Ý nghĩa</th>
                </tr>
              </thead>
              <tbody>
                {t.columns.map((c) => (
                  <tr key={c.name} className="border-t">
                    <td className="py-1 font-mono">{c.name}</td>
                    <td>{c.read_only ? "Chỉ để tham khảo" : c.required ? "Có" : "Không"}</td>
                    <td>{c.description}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={downloadTemplate}>
              Tải mẫu CSV
            </Button>
            {t.exportable && (
              <Button
                variant="outline"
                disabled={busy || !t.enabled}
                onClick={() => void requestExport()}
              >
                Xuất dữ liệu hiện tại
              </Button>
            )}
          </div>
          {exported.error && (
            <p role="alert">{problem(exported.error, "Không đọc được bản xuất.")}</p>
          )}
          {exported.data && (
            <p role="status">
              Bản xuất:{" "}
              {exported.data.status === "ready"
                ? `${exported.data.row_count} dòng, sẵn sàng`
                : exported.data.status}
              {exported.data.status === "ready" && (
                <Button className="ml-2" disabled={busy} onClick={() => void downloadExport()}>
                  Tải bản xuất (liên kết 60 giây)
                </Button>
              )}
            </p>
          )}
          {writable && t.enabled && (
            <div className="space-y-2 border-t pt-3">
              <label className="block">
                Tệp CSV
                <Input
                  type="file"
                  accept=".csv,text/csv"
                  disabled={busy}
                  onChange={(e) => {
                    setFile(e.target.files?.[0] ?? null);
                    setUploadKey("");
                  }}
                />
              </label>
              <Button disabled={!file || busy} onClick={() => void upload()}>
                {uploadKey ? "Thử lại cùng tệp" : "Tải lên và kiểm tra"}
              </Button>
            </div>
          )}
          {!writable && <p>Bạn chỉ có quyền xem; không thể nhập dữ liệu.</p>}
        </div>
      )}
      {message && <p role="status">{message}</p>}
      <div className="space-y-2">
        <h2 className="font-semibold">Các lượt nhập gần đây</h2>
        {jobs.error && <p role="alert">{problem(jobs.error, "Không tải được lịch sử.")}</p>}
        {jobs.data && myJobs.length === 0 && <p>Chưa có lượt nhập nào.</p>}
        <ul className="space-y-1">
          {myJobs.map((j) => (
            <li key={j.id}>
              <button
                type="button"
                className="text-left underline-offset-2 hover:underline"
                aria-current={j.id === jobId}
                onClick={() => setJobId(j.id)}
              >
                {time(j.created_at)} · {j.file_name} · {bulk.JOB_STATUS_LABELS[j.status]}
                {j.outcome && ` · ${bulk.OUTCOME_LABELS[j.outcome]}`} · {j.summary.total} dòng
              </button>
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
      </div>
      {jobId && (
        <JobDetail
          key={jobId}
          owner={spec.owner}
          jobId={jobId}
          writable={writable}
          onChanged={() => client.invalidateQueries({ queryKey: [...scope, "jobs"] })}
        />
      )}
    </div>
  );
}

const FILTERS: { value: string; label: string }[] = [
  { value: "", label: "Tất cả" },
  { value: "invalid", label: "Lỗi" },
  { value: "pending", label: "Chờ áp dụng" },
  { value: "succeeded", label: "Đã áp dụng" },
  { value: "needs_approval", label: "Chờ sàn duyệt" },
  { value: "failed", label: "Thất bại" },
  { value: "cancelled", label: "Đã dừng" },
  { value: "unchanged", label: "Không đổi" },
];

function JobDetail({
  owner,
  jobId,
  writable,
  onChanged,
}: {
  owner: bulk.BulkOwner;
  jobId: string;
  writable: boolean;
  onChanged: () => void;
}) {
  const { callWithAuth, user } = useAuth();
  const client = useQueryClient();
  const [filter, setFilter] = useState("");
  const [cursors, setCursors] = useState<string[]>([""]);
  const [ack, setAck] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const scope = ["bulk-job", user?.id, owner, jobId];
  const job = useQuery({
    queryKey: scope,
    queryFn: () => callWithAuth((t) => bulk.getJob(t, owner, jobId)),
    retry: false,
    refetchInterval: (q) => (bulk.isActive(q.state.data) ? 2000 : false),
  });
  const cursor = cursors[cursors.length - 1];
  const rows = useQuery({
    queryKey: [...scope, "rows", filter, cursor, job.data?.version],
    queryFn: () => callWithAuth((t) => bulk.listRows(t, owner, jobId, filter, cursor)),
    enabled: !!job.data && job.data.summary.total > 0,
    retry: false,
  });
  const j = job.data;
  async function act(fn: () => Promise<unknown>, done: string, fallback: string) {
    setBusy(true);
    setMessage("");
    try {
      await fn();
      setMessage(done);
      onChanged();
    } catch (e) {
      setMessage(problem(e, fallback));
    } finally {
      setBusy(false);
      await client.invalidateQueries({ queryKey: scope });
    }
  }
  if (job.isPending) return <p>Đang tải lượt nhập…</p>;
  if (job.error || !j) return <p role="alert">{problem(job.error, "Không tải được lượt nhập.")}</p>;
  const s = j.summary;
  return (
    <div className="space-y-3 rounded border p-4" aria-label="Chi tiết lượt nhập">
      <h2 className="font-semibold">
        {j.file_name} · {bulk.JOB_STATUS_LABELS[j.status]}
        {j.outcome && ` · ${bulk.OUTCOME_LABELS[j.outcome]}`}
      </h2>
      <p className="text-sm">
        Tải lên {time(j.created_at)} · Kiểm tra {time(j.preview_at)}
        {j.finished_at && ` · Kết thúc ${time(j.finished_at)}`}
      </p>
      {j.error_message && <p role="alert">{bulk.errorText(j.error_code ?? "", j.error_message)}</p>}
      <dl className="grid gap-2 text-sm sm:grid-cols-4" aria-label="Tổng hợp">
        {(
          [
            ["Tổng dòng", s.total],
            ["Lỗi", s.invalid],
            ["Không đổi", s.unchanged],
            ["Chờ áp dụng", s.pending],
            ["Đã áp dụng", s.succeeded],
            ["Chờ sàn duyệt", s.needs_approval],
            ["Thất bại", s.failed],
            ["Đã dừng", s.cancelled],
          ] as const
        ).map(([label, n]) => (
          <div key={label} className="rounded border p-2">
            <dt>{label}</dt>
            <dd className="font-semibold">{n}</dd>
          </div>
        ))}
      </dl>
      {j.status === "preview_ready" && writable && (
        <div className="space-y-2 border-t pt-3">
          <p className="text-sm">
            Chỉ {s.pending} dòng hợp lệ được áp dụng; dòng lỗi giữ nguyên để bạn sửa và tải lên
            thành lượt mới. Bản kiểm tra có hiệu lực 24 giờ.
          </p>
          {j.duplicate_of && (
            <label className="flex items-center gap-2" role="alert">
              <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} />
              Tệp này đã được áp dụng ở lượt {j.duplicate_of.slice(0, 8)}. Tôi chắc chắn muốn áp
              dụng lại.
            </label>
          )}
          <Button
            disabled={busy || s.pending === 0 || (!!j.duplicate_of && !ack)}
            onClick={() =>
              void act(
                () => callWithAuth((t) => bulk.confirmJob(t, owner, j, ack)),
                "Đã xác nhận. Các dòng đang được áp dụng.",
                "Không xác nhận được; tải lại trang rồi thử lại.",
              )
            }
          >
            Xác nhận áp dụng {s.pending} dòng
          </Button>
        </div>
      )}
      {writable && bulk.canCancel(j) && (
        <div className="flex flex-wrap items-center gap-2">
          {!stopping ? (
            <Button variant="outline" disabled={busy} onClick={() => setStopping(true)}>
              Dừng lượt nhập
            </Button>
          ) : (
            <>
              <span className="text-sm">
                Các dòng chưa áp dụng sẽ dừng; dòng đã áp dụng không được hoàn tác.
              </span>
              <Button
                variant="destructive"
                disabled={busy}
                onClick={() =>
                  void act(
                    () => callWithAuth((t) => bulk.cancelJob(t, owner, j.id)),
                    "Đã dừng lượt nhập.",
                    "Không dừng được lượt nhập.",
                  ).then(() => setStopping(false))
                }
              >
                Xác nhận dừng
              </Button>
            </>
          )}
        </div>
      )}
      {["preview_ready", "completed", "cancelled"].includes(j.status) && bulk.hasProblems(s) && (
        <Button
          variant="outline"
          disabled={busy}
          onClick={() =>
            void act(
              async () =>
                openLink((await callWithAuth((t) => bulk.reportLink(t, owner, j.id))).url),
              "Đã tạo báo cáo dòng lỗi (liên kết 60 giây).",
              "Không tải được báo cáo.",
            )
          }
        >
          Tải báo cáo dòng lỗi
        </Button>
      )}
      {message && <p role="status">{message}</p>}
      {s.total > 0 && (
        <div className="space-y-2">
          <label className="block text-sm">
            Lọc dòng
            <select
              className="ml-2 rounded border bg-background p-1"
              value={filter}
              onChange={(e) => {
                setFilter(e.target.value);
                setCursors([""]);
              }}
            >
              {FILTERS.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label}
                </option>
              ))}
            </select>
          </label>
          {rows.error && <p role="alert">{problem(rows.error, "Không tải được các dòng.")}</p>}
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <caption className="sr-only">Các dòng của tệp</caption>
              <thead>
                <tr>
                  <th>Dòng dữ liệu</th>
                  <th>Trạng thái</th>
                  <th>Hiện tại</th>
                  <th>Sau khi áp dụng</th>
                  <th>Lỗi</th>
                </tr>
              </thead>
              <tbody>
                {rows.data?.items.map((r) => (
                  <tr key={r.row_no} className="border-t align-top">
                    <td className="py-1">{r.row_no}</td>
                    <td>{bulk.ROW_STATUS_LABELS[r.status]}</td>
                    <td>{bulk.describeValues(r.old_values)}</td>
                    <td>{bulk.describeValues(r.new_values)}</td>
                    <td>
                      {r.error && (
                        <>
                          {r.error.field && <span className="font-mono">{r.error.field}: </span>}
                          {r.error.message}
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {rows.data?.items.length === 0 && <p>Không có dòng nào ở bộ lọc này.</p>}
          <div className="flex gap-2">
            <Button
              variant="outline"
              disabled={cursors.length === 1}
              onClick={() => setCursors(cursors.slice(0, -1))}
            >
              Trang trước
            </Button>
            <Button
              variant="outline"
              disabled={!rows.data?.next_cursor}
              onClick={() => setCursors([...cursors, rows.data!.next_cursor!])}
            >
              Trang sau
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            Dòng dữ liệu 1 là dòng ngay dưới tiêu đề (dòng 2 trong bảng tính).
          </p>
        </div>
      )}
    </div>
  );
}
