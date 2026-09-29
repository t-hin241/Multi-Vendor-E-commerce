"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";

export default function AdminReviewsPage() {
  const { callWithAuth } = useAuth();
  const client = useQueryClient();
  const [buyerId, setBuyerId] = useState("");
  const [vendorId, setVendorId] = useState("");
  const [productId, setProductId] = useState("");
  const [code, setCode] = useState("");
  const [label, setLabel] = useState("");
  const reviews = useQuery({
    queryKey: ["admin-reviews", buyerId, vendorId, productId],
    queryFn: () =>
      callWithAuth((token) => api.listAdminReviews(token, { buyerId, vendorId, productId })),
  });
  const reports = useQuery({
    queryKey: ["review-reports"],
    queryFn: () => callWithAuth((token) => api.listReviewReports(token, "open")),
  });
  const reasons = useQuery({
    queryKey: ["admin-review-reasons"],
    queryFn: () => callWithAuth(api.listModerationReasons),
  });
  const resolve = useMutation({
    mutationFn: ({
      reportId,
      decision,
      reasonId,
    }: {
      reportId: string;
      decision: "keep" | "hide";
      reasonId?: string;
    }) => callWithAuth((token) => api.resolveReviewReport(token, reportId, decision, reasonId)),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["review-reports"] });
      client.invalidateQueries({ queryKey: ["admin-reviews"] });
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
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">Review moderation</h1>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Bộ lọc review</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-2 sm:grid-cols-3">
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
        </CardContent>
      </Card>
      <section>
        <h2 className="mb-2 text-lg font-medium">Tất cả review</h2>
        <div className="space-y-2">
          {reviews.data?.map((review) => (
            <Card key={review.id}>
              <CardContent className="pt-5">
                <p className="font-medium">
                  {review.rating}/5 · {review.status}
                </p>
                <p className="mt-1 text-sm">{review.comment}</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  Buyer: {review.buyer_id} · Product: {review.product_id}
                </p>
              </CardContent>
            </Card>
          ))}
        </div>
      </section>
      <section>
        <h2 className="mb-2 text-lg font-medium">Báo cáo đang chờ</h2>
        <div className="space-y-2">
          {reports.data?.map((report) => (
            <Card key={report.id}>
              <CardContent className="flex flex-wrap items-center gap-2 pt-5">
                <div className="mr-auto text-sm">
                  <p className="font-medium">{report.reason_label}</p>
                  <p className="text-muted-foreground">Review {report.review_id}</p>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={resolve.isPending}
                  onClick={() => resolve.mutate({ reportId: report.id, decision: "keep" })}
                >
                  Giữ review
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={resolve.isPending || !reasons.data?.some((reason) => reason.is_active)}
                  onClick={() =>
                    resolve.mutate({
                      reportId: report.id,
                      decision: "hide",
                      reasonId: reasons.data?.find((reason) => reason.is_active)?.id,
                    })
                  }
                >
                  Ẩn review
                </Button>
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
              placeholder="code"
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
            <Input
              className="max-w-64"
              placeholder="Tên lý do"
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
              <li key={reason.id}>
                {reason.label} ({reason.code}) — {reason.is_active ? "active" : "inactive"}
              </li>
            ))}
          </ul>
          {(resolve.error || createReason.error) && (
            <p className="mt-2 text-sm text-destructive">
              {describeApiError(
                resolve.error ?? createReason.error,
                "Không thể cập nhật moderation.",
              )}
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
