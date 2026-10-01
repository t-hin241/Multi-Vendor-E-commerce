"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";

export default function VendorReviewsPage() {
  const { selectedVendorId, callWithAuth } = useAuth();
  const client = useQueryClient();
  const [replyFor, setReplyFor] = useState<string | null>(null);
  const [message, setMessage] = useState("");
  const [reportFor, setReportFor] = useState<string | null>(null);
  const [reasonId, setReasonId] = useState("");
  const [reportNote, setReportNote] = useState("");
  const [reported, setReported] = useState<string | null>(null);
  const reviews = useQuery({
    queryKey: ["vendor-reviews", selectedVendorId],
    queryFn: () =>
      callWithAuth((token) => api.listVendorReviews(token, { vendorId: selectedVendorId! })),
    enabled: Boolean(selectedVendorId),
  });
  const reasons = useQuery({
    queryKey: ["review-reasons"],
    queryFn: () => callWithAuth(api.listVendorModerationReasons),
    enabled: Boolean(selectedVendorId),
  });
  const summary = useQuery({
    queryKey: ["vendor-review-summary", selectedVendorId],
    queryFn: () => callWithAuth((token) => api.getVendorReviewSummary(token, selectedVendorId!)),
    enabled: Boolean(selectedVendorId),
  });
  const reply = useMutation({
    mutationFn: () =>
      callWithAuth((token) => api.replyToReview(token, replyFor!, selectedVendorId!, message)),
    onSuccess: () => {
      setReplyFor(null);
      setMessage("");
      client.invalidateQueries({ queryKey: ["vendor-reviews"] });
    },
  });
  const report = useMutation({
    mutationFn: () =>
      callWithAuth((token) =>
        api.reportReview(token, reportFor!, selectedVendorId!, reasonId, reportNote || undefined),
      ),
    onSuccess: () => {
      setReported(reportFor);
      setReportFor(null);
      setReasonId("");
      setReportNote("");
    },
  });
  if (!selectedVendorId)
    return <p className="text-sm text-muted-foreground">Chọn một shop để xem đánh giá.</p>;
  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">Đánh giá sản phẩm</h1>
      {summary.data && (
        <p className="text-sm text-muted-foreground">
          Rating tổng: {summary.data.rating_average.toFixed(1)} / 5 từ {summary.data.rating_count}{" "}
          đánh giá
        </p>
      )}
      {summary.isError && (
        <p role="alert" className="text-sm text-destructive">
          {describeApiError(summary.error, "Không thể tải thống kê đánh giá.")}
        </p>
      )}
      {reviews.isPending && (
        <p role="status" className="text-sm text-muted-foreground">
          Đang tải đánh giá…
        </p>
      )}
      {reviews.isError && (
        <div role="alert" className="space-y-2">
          <p className="text-sm text-destructive">
            {describeApiError(reviews.error, "Không thể tải danh sách đánh giá.")}
          </p>
          <Button
            variant="outline"
            disabled={reviews.isFetching}
            onClick={() => {
              void reviews.refetch();
              void summary.refetch();
            }}
          >
            Thử lại
          </Button>
        </div>
      )}
      {reviews.isSuccess && reviews.data.length === 0 && (
        <p className="text-sm text-muted-foreground">Chưa có đánh giá công khai.</p>
      )}
      {reviews.data?.map((review) => (
        <Card key={review.id}>
          <CardHeader>
            <CardTitle className="text-base">
              {review.rating}/5 · {new Date(review.created_at).toLocaleDateString("vi-VN")}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="whitespace-pre-wrap text-sm">{review.comment}</p>
            {review.reply ? (
              <div className="mt-3 rounded bg-muted p-3 text-sm">
                Đã phản hồi: {review.reply.message}
              </div>
            ) : replyFor === review.id ? (
              <div className="mt-3 space-y-2">
                <Textarea
                  value={message}
                  onChange={(event) => setMessage(event.target.value)}
                  placeholder="Phản hồi của shop"
                />
                <Button
                  size="sm"
                  disabled={!message.trim() || reply.isPending}
                  onClick={() => reply.mutate()}
                >
                  Lưu phản hồi
                </Button>
              </div>
            ) : (
              <Button
                className="mt-3"
                size="sm"
                variant="outline"
                onClick={() => setReplyFor(review.id)}
              >
                Phản hồi
              </Button>
            )}
            {reportFor === review.id ? (
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <select
                  className="h-9 rounded-md border bg-background px-2 text-sm"
                  value={reasonId}
                  onChange={(event) => setReasonId(event.target.value)}
                >
                  <option value="">Chọn lý do vi phạm</option>
                  {reasons.data?.map((reason) => (
                    <option key={reason.id} value={reason.id}>
                      {reason.label}
                    </option>
                  ))}
                </select>
                <Input
                  className="max-w-72"
                  maxLength={1000}
                  placeholder="Mô tả thêm (không bắt buộc)"
                  value={reportNote}
                  onChange={(event) => setReportNote(event.target.value)}
                />
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={!reasonId || report.isPending}
                  onClick={() => report.mutate()}
                >
                  Gửi báo cáo
                </Button>
              </div>
            ) : (
              <Button
                className="mt-3 ml-2"
                size="sm"
                variant="ghost"
                onClick={() => setReportFor(review.id)}
              >
                Báo cáo vi phạm
              </Button>
            )}
            {reported === review.id && (
              <p role="status" className="mt-2 text-sm text-muted-foreground">
                Đã gửi báo cáo; quản trị viên sẽ xem xét.
              </p>
            )}
            {(reply.error || report.error) && (
              <p className="mt-2 text-sm text-destructive">
                {describeApiError(reply.error ?? report.error, "Không thể cập nhật đánh giá.")}
              </p>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  );
}
