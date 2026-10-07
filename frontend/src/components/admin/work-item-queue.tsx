"use client";

import Link from "next/link";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/lib/auth-context";
import { newOperationId } from "@/lib/api-client";
import {
  actionDue,
  changeWorkItem,
  workItems,
  workItemURL,
  type SLAOwner,
  type WorkItem,
} from "@/lib/case-sla";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const formatDate = (raw: string) =>
  new Date(raw).toLocaleString("vi-VN", { timeZone: "Asia/Ho_Chi_Minh" });

export function WorkItemQueue() {
  const { user, callWithAuth } = useAuth();
  const cache = useQueryClient();
  const [status, setStatus] = useState("overdue");
  const [cursors, setCursors] = useState<Partial<Record<SLAOwner, string>>>({});
  const query = useQuery({
    queryKey: ["case-sla", user?.id, status, cursors],
    queryFn: () => callWithAuth((token) => workItems(token, status, cursors)),
    enabled: user?.role === "admin",
    refetchInterval: 60_000,
  });
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Hạn xử lý hồ sơ</h1>
        <p className="text-muted-foreground">
          Phân công và theo dõi phản hồi. Thời gian hiển thị theo giờ Việt Nam.
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        {(
          [
            ["overdue", "Quá hạn"],
            ["unassigned", "Chưa phân công"],
            ["needs_attention", "Cần can thiệp"],
            ["active", "Đang mở"],
            ["legacy", "Hồ sơ cũ chờ duyệt nhắc"],
          ] as const
        ).map(([value, label]) => (
          <Button
            key={value}
            variant={status === value ? "default" : "outline"}
            onClick={() => {
              setStatus(value);
              setCursors({});
            }}
          >
            {label}
          </Button>
        ))}
        <Button variant="outline" onClick={() => query.refetch()}>
          Làm mới
        </Button>
      </div>
      {query.isPending && <p>Đang tải…</p>}
      {query.error && <p role="alert">{query.error.message}</p>}
      {query.data?.sources.map((source) => (
        <section key={source.name} className="space-y-3 rounded-lg border p-4">
          <h2 className="text-lg font-semibold capitalize">{source.name}</h2>
          {source.status === "unavailable" || !source.page ? (
            <p role="alert">Nguồn dữ liệu không khả dụng. Chưa xác định được số hồ sơ.</p>
          ) : (
            <>
              <p className="text-xs text-muted-foreground">
                Cập nhật: {formatDate(source.page.generated_at)}
              </p>
              {!source.page.items.length && <p>Không có hồ sơ phù hợp.</p>}
              {source.page.items.map((item) => (
                <WorkItemRow
                  key={`${user?.id}:${item.id}:${item.version}`}
                  item={item}
                  owner={source.name}
                  onChanged={() => cache.invalidateQueries({ queryKey: ["case-sla"] })}
                />
              ))}
              {source.page.next_cursor && (
                <Button
                  variant="outline"
                  onClick={() =>
                    setCursors((c) => ({ ...c, [source.name]: source.page?.next_cursor }))
                  }
                >
                  Trang tiếp theo của {source.name}
                </Button>
              )}
              {cursors[source.name] && (
                <Button
                  variant="ghost"
                  onClick={() => setCursors((c) => ({ ...c, [source.name]: undefined }))}
                >
                  Về trang đầu
                </Button>
              )}
            </>
          )}
        </section>
      ))}
    </div>
  );
}

function WorkItemRow({
  item,
  owner,
  onChanged,
}: {
  item: WorkItem;
  owner: SLAOwner;
  onChanged: () => Promise<unknown>;
}) {
  const { user, callWithAuth } = useAuth();
  const [assignee, setAssignee] = useState(item.assignee_id ?? "");
  const [reason, setReason] = useState("");
  const [due, setDue] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [operation, setOperation] = useState<{ payload: string; key: string } | null>(null);
  const act = async (action: "assignments" | "extensions" | "activations") => {
    if (!reason.trim()) {
      setError("Cần ghi lý do.");
      return;
    }
    if (action === "extensions" && (!due || !Number.isFinite(Date.parse(`${due}+07:00`)))) {
      setError("Chọn hạn mới theo giờ Việt Nam.");
      return;
    }
    const body = {
      expected_version: item.version,
      reason: reason.trim(),
      ...(action === "assignments" ? { assignee_id: assignee } : {}),
      ...(action === "extensions" ? { new_due_at: new Date(`${due}+07:00`).toISOString() } : {}),
    };
    const payload = JSON.stringify({ action, body });
    const key = operation?.payload === payload ? operation.key : newOperationId();
    setOperation({ payload, key });
    setBusy(true);
    setError("");
    try {
      await callWithAuth((token) => changeWorkItem(token, owner, item.id, action, body, key));
      await onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Không thể cập nhật hồ sơ.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <article className="space-y-3 rounded-md border p-4">
      <div className="flex flex-wrap justify-between gap-2">
        <Link className="font-medium underline" href={workItemURL(item)}>
          {item.resource_type} · {item.resource_id}
        </Link>
        <span>
          {item.needs_attention
            ? "Cần can thiệp"
            : item.legacy
              ? "Chưa bật nhắc cho hồ sơ cũ"
              : "Đang theo dõi"}
        </span>
      </div>
      <p>
        Hạn: {formatDate(actionDue(item))} · Chờ: {item.waiting_on}
        {item.paused_at ? " (đang tạm dừng hạn phản hồi; hạn tổng vẫn áp dụng)" : ""}
      </p>
      {item.breached_at && (
        <p className="text-sm text-destructive">Đã từng quá hạn: {formatDate(item.breached_at)}</p>
      )}
      <label className="block text-sm">
        Lý do
        <Input
          value={reason}
          maxLength={500}
          onChange={(e) => setReason(e.target.value)}
          disabled={busy}
        />
      </label>
      <div className="flex flex-wrap items-end gap-2">
        <label className="grow text-sm">
          ID admin nhận việc (để trống để bỏ phân công)
          <Input value={assignee} onChange={(e) => setAssignee(e.target.value)} disabled={busy} />
        </label>
        <Button variant="outline" disabled={busy} onClick={() => setAssignee(user?.id ?? "")}>
          Chọn tôi
        </Button>
        <Button disabled={busy} onClick={() => act("assignments")}>
          Phân công
        </Button>
      </div>
      <div className="flex flex-wrap items-end gap-2">
        <label className="text-sm">
          Hạn mới (giờ Việt Nam)
          <Input
            type="datetime-local"
            value={due}
            onChange={(e) => setDue(e.target.value)}
            disabled={busy || !!item.paused_at}
          />
        </label>
        <Button
          variant="outline"
          disabled={busy || !!item.paused_at}
          onClick={() => act("extensions")}
        >
          Gia hạn
        </Button>
        {item.legacy && (
          <Button variant="outline" disabled={busy} onClick={() => act("activations")}>
            Cho phép nhắc hồ sơ này
          </Button>
        )}
      </div>
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
    </article>
  );
}
