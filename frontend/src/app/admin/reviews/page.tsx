"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";

const COUNTERS: { key: string; label: string }[] = [
  { key: "open_reports", label: "Báo cáo đang chờ" },
  { key: "oldest_open_report_hours", label: "Báo cáo cũ nhất (giờ)" },
  { key: "hidden_7d", label: "Đã ẩn (7 ngày)" },
  { key: "reviews_24h", label: "Review mới (24h)" },
  { key: "image_cleanup_parked", label: "Ảnh lỗi chưa dọn" },
];

// A decision in progress: hide or restore one review, or resolve a report.
type Pending =
  | { kind: "hide"; reviewId: string }
  | { kind: "restore"; reviewId: string }
  | { kind: "resolve"; reportId: string; decision: "keep" | "hide" };

export default function AdminReviewsPage() {
  const { callWithAuth } = useAuth();
  const client = useQueryClient();
  const [buyerId, setBuyerId] = useState("");
  const [vendorId, setVendorId] = useState("");
  const [productId, setProductId] = useState("");
  const [status, setStatus] = useState("");
  const [code, setCode] = useState("");
  const [label, setLabel] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);
  const [reasonId, setReasonId] = useState("");
  const [note, setNote] = useState("");

  const ops = useQuery({
    queryKey: ["review-operations"],
    queryFn: () => callWithAuth(api.getReviewOperations),
    refetchInterval: 60_000,
  });
  const reviews = useQuery({
    queryKey: ["admin-reviews", buyerId, vendorId, productId, status],
    queryFn: () =>
      callWithAuth((token) =>
        api.listAdminReviews(token, {
          buyerId: buyerId.trim() || undefined,
          vendorId: vendorId.trim() || undefined,
          productId: productId.trim() || undefined,
          status: status || undefined,
        }),
      ),
  });
  const reports = useQuery({
    queryKey: ["review-reports"],
    queryFn: () => callWithAuth((token) => api.listReviewReports(token, "open")),
  });
  const reasons = useQuery({
    queryKey: ["admin-review-reasons"],
    queryFn: () => callWithAuth(api.listModerationReasons),
  });
  const activeReasons = reasons.data?.filter((reason) => reason.is_active) ?? [];

  function start(next: Pending) {
    setPending(next);
    setReasonId("");
    setNote("");
  }

  const decide = useMutation({
    mutationFn: (p: Pending) =>
      callWithAuth((token): Promise<unknown> => {
        switch (p.kind) {
          case "hide":
            return api.hideReview(token, p.reviewId, reasonId, note);
          case "restore":
            return api.restoreReview(token, p.reviewId, note);
          case "resolve":
            return api.resolveReviewReport(
              token,
              p.reportId,
              p.decision,
              p.decision === "hide" ? reasonId : undefined,
              note || undefined,
            );
        }
      }),
    onSuccess: () => {
      setPending(null);
      client.invalidateQueries({ queryKey: ["review-reports"] });
      client.invalidateQueries({ queryKey: ["admin-reviews"] });
      client.invalidateQueries({ queryKey: ["review-operations"] });
    },
  });
  const createReason = useMutation({
    mutationFn: () => callWithAuth((token) => api.createModerationReason(token, code, label)),
    onSuccess: () => {
      setCode("");
      setLabel("");
      client.invalidateQueries({ queryKey: ["admin-review-reasons"] });
    },
  });
  const toggleReason = useMutation({
    mutationFn: (reason: api.ModerationReason) =>
      callWithAuth((token) =>
        api.updateModerationReason(token, { ...reason, is_active: !reason.is_active }),
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ["admin-review-reasons"] }),
  });

  // The form for the decision in progress, shown under its review/report.
  function decisionForm(p: Pending) {
    const needsReason = p.kind === "hide" || (p.kind === "resolve" && p.decision === "hide");
    const needsNote = p.kind !== "resolve";
    const ready = (!needsReason || reasonId) && (!needsNote || note.trim());
    return (
      <div className="mt-3 flex flex-wrap items-center gap-2">
        {needsReason && (
          <select
            aria-label="Lý do"
            className="h-9 rounded-md border bg-background px-2 text-sm"
            value={reasonId}
            onChange={(event) => setReasonId(event.target.value)}
          >
            <option value="">Chọn lý do</option>
            {activeReasons.map((reason) => (
              <option key={reason.id} value={reason.id}>
                {reason.label}
              </option>
            ))}
          </select>
        )}
        <Input
          className="max-w-80"
          maxLength={1000}
          placeholder={needsNote ? "Ghi chú (bắt buộc)" : "Ghi chú (không bắt buộc)"}
          value={note}
          onChange={(event) => setNote(event.target.value)}
        />
        <Button size="sm" disabled={!ready || decide.isPending} onClick={() => decide.mutate(p)}>
          Xác nhận
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setPending(null)}>
          Huỷ
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">Review moderation</h1>
      <Card>
        <CardContent className="grid gap-3 pt-5 sm:grid-cols-3 lg:grid-cols-5">
          {COUNTERS.map((c) => (
            <div key={c.key}>
              <p className="text-xs text-muted-foreground">{c.label}</p>
              <p className="text-xl font-semibold">
                {ops.data ? (ops.data.counts[c.key] ?? 0) : ops.error ? "—" : "…"}
              </p>
            </div>
          ))}
        </CardContent>
      </Card>
      {decide.error && (
        <p role="alert" className="text-sm text-destructive">
          {describeApiError(decide.error, "Không thể cập nhật review.")}
        </p>
      )}
      <section>
        <h2 className="mb-2 text-lg font-medium">Báo cáo đang chờ</h2>
        {reports.isSuccess && reports.data.length === 0 && (
          <p className="text-sm text-muted-foreground">Không có báo cáo nào đang chờ.</p>
        )}
        <div className="space-y-2">
          {reports.data?.map((report) => (
            <Card key={report.id}>
              <CardContent className="pt-5">
                <div className="flex flex-wrap items-center gap-2">
                  <div className="mr-auto text-sm">
                    <p className="font-medium">{report.reason_label}</p>
                    {report.note && <p className="whitespace-pre-wrap">{report.note}</p>}
                    <p className="text-muted-foreground">
                      Review {report.review_id} ·{" "}
                      {new Date(report.created_at).toLocaleString("vi-VN")}
                    </p>
                  </div>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      start({ kind: "resolve", reportId: report.id, decision: "keep" })
                    }
                  >
                    Giữ review
                  </Button>
                  <Button
                    size="sm"
                    variant="destructive"
                    disabled={activeReasons.length === 0}
                    onClick={() =>
                      start({ kind: "resolve", reportId: report.id, decision: "hide" })
                    }
                  >
                    Ẩn review
                  </Button>
                </div>
                {pending?.kind === "resolve" &&
                  pending.reportId === report.id &&
                  decisionForm(pending)}
              </CardContent>
            </Card>
          ))}
        </div>
      </section>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Bộ lọc review</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-2 sm:grid-cols-4">
          <Input
            placeholder="Buyer ID"
            value={buyerId}
            onChange={(event) => setBuyerId(event.target.value)}
          />
          <Input
            placeholder="Vendor ID"
            value={vendorId}
            onChange={(event) => setVendorId(event.target.value)}
          />
          <Input
            placeholder="Product ID"
            value={productId}
            onChange={(event) => setProductId(event.target.value)}
          />
          <select
            aria-label="Trạng thái"
            className="h-9 rounded-md border bg-background px-2 text-sm"
            value={status}
            onChange={(event) => setStatus(event.target.value)}
          >
            <option value="">Mọi trạng thái</option>
            <option value="published">Đang hiển thị</option>
            <option value="hidden">Đã ẩn</option>
          </select>
        </CardContent>
      </Card>
      <section>
        <h2 className="mb-2 text-lg font-medium">Tất cả review</h2>
        {reviews.isError && (
          <p role="alert" className="text-sm text-destructive">
            {describeApiError(reviews.error, "Không thể tải review.")}
          </p>
        )}
        <div className="space-y-2">
          {reviews.data?.map((review) => (
            <Card key={review.id}>
              <CardContent className="pt-5">
                <div className="flex flex-wrap items-start gap-2">
                  <div className="mr-auto">
                    <p className="font-medium">
                      {review.rating}/5 · {review.status === "hidden" ? "Đã ẩn" : "Đang hiển thị"}
                      {review.verified_purchase ? " · Đã mua hàng" : " · Chưa xác minh"}
                    </p>
                    <p className="mt-1 whitespace-pre-wrap text-sm">{review.comment}</p>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {review.buyer_name || "Người mua"} · Buyer: {review.buyer_id} · Product:{" "}
                      {review.product_id}
                    </p>
                    {review.status === "hidden" && review.hidden_note && (
                      <p className="mt-1 text-xs text-muted-foreground">
                        Lý do ẩn: {review.hidden_note}
                      </p>
                    )}
                  </div>
                  {review.status === "hidden" ? (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => start({ kind: "restore", reviewId: review.id })}
                    >
                      Hiển thị lại
                    </Button>
                  ) : (
                    <Button
                      size="sm"
                      variant="destructive"
                      disabled={activeReasons.length === 0}
                      onClick={() => start({ kind: "hide", reviewId: review.id })}
                    >
                      Ẩn
                    </Button>
                  )}
                </div>
                {pending &&
                  pending.kind !== "resolve" &&
                  pending.reviewId === review.id &&
                  decisionForm(pending)}
              </CardContent>
            </Card>
          ))}
        </div>
      </section>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Moderation reasons</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-2">
            <Input
              className="max-w-48"
              placeholder="code (vd: spam_link)"
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
            <Input
              className="max-w-64"
              placeholder="Tên lý do"
              maxLength={100}
              value={label}
              onChange={(event) => setLabel(event.target.value)}
            />
            <Button
              disabled={!code.trim() || !label.trim() || createReason.isPending}
              onClick={() => createReason.mutate()}
            >
              Thêm lý do
            </Button>
          </div>
          <ul className="mt-3 space-y-1 text-sm">
            {reasons.data?.map((reason) => (
              <li key={reason.id} className="flex items-center gap-2">
                <span className="mr-auto">
                  {reason.label} ({reason.code}) — {reason.is_active ? "active" : "inactive"}
                </span>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={toggleReason.isPending}
                  onClick={() => toggleReason.mutate(reason)}
                >
                  {reason.is_active ? "Tắt" : "Bật"}
                </Button>
              </li>
            ))}
          </ul>
          {(createReason.error || toggleReason.error) && (
            <p className="mt-2 text-sm text-destructive">
              {describeApiError(
                createReason.error ?? toggleReason.error,
                "Không thể cập nhật lý do.",
              )}
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
