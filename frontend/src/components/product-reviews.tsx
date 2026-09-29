"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Star, Upload } from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";

function Stars({
  rating,
  interactive,
  onChange,
}: {
  rating: number;
  interactive?: boolean;
  onChange?: (rating: number) => void;
}) {
  return (
    <span className="inline-flex gap-0.5" aria-label={`${rating} out of 5 stars`}>
      {[1, 2, 3, 4, 5].map((value) => (
        <button
          key={value}
          type="button"
          disabled={!interactive}
          onClick={() => onChange?.(value)}
          className={interactive ? "cursor-pointer" : "cursor-default"}
        >
          <Star
            className={`size-4 ${value <= rating ? "fill-yellow-400 text-yellow-400" : "text-muted-foreground"}`}
          />
        </button>
      ))}
    </span>
  );
}

export function ProductReviews({ productId, className }: { productId: string; className?: string }) {
  const { user, callWithAuth } = useAuth();
  const client = useQueryClient();
  const [rating, setRating] = useState(5);
  const [comment, setComment] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const reviews = useQuery({
    queryKey: ["product-reviews", productId],
    queryFn: () => api.listProductReviews(productId),
  });
  const eligibility = useQuery({
    queryKey: ["review-eligibility", productId],
    queryFn: () => callWithAuth((token) => api.listReviewEligibility(token, productId)),
    enabled: user?.role === "buyer",
  });
  const eligible = eligibility.data?.[0];
  const create = useMutation({
    mutationFn: async () => {
      if (!eligible)
        throw new api.ApiError(
          403,
          "forbidden",
          "This product has no completed purchase eligible for review.",
        );
      return callWithAuth(async (token) => {
        const review = await api.createReview(token, {
          orderItemId: eligible.order_item_id,
          rating,
          comment,
        });
        await Promise.all(files.map((file) => api.uploadReviewImage(token, review.id, file)));
        return review;
      });
    },
    onSuccess: () => {
      setComment("");
      setFiles([]);
      client.invalidateQueries({ queryKey: ["product-reviews", productId] });
      client.invalidateQueries({ queryKey: ["review-eligibility", productId] });
    },
  });
  const summary = reviews.data?.summary;
  const error = create.error ? describeApiError(create.error, "Could not publish review.") : null;
  const fileLabel = useMemo(
    () =>
      files.length
        ? `${files.length} image${files.length === 1 ? "" : "s"} selected`
        : "Add up to 5 images",
    [files.length],
  );

  return (
    <section className={className ?? "mt-8"} aria-labelledby="reviews-heading">
      <div className="flex flex-wrap items-baseline gap-3">
        <h2 id="reviews-heading" className="text-lg font-medium">
          Đánh giá sản phẩm
        </h2>
        {summary && (
          <>
            <Stars rating={Math.round(summary.rating_average)} />
            <span className="text-sm text-muted-foreground">
              {summary.rating_average.toFixed(1)} / 5 · {summary.rating_count} đánh giá
            </span>
          </>
        )}
      </div>
      {user?.role === "buyer" && eligible && (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle className="text-base">Viết đánh giá</CardTitle>
          </CardHeader>
          <CardContent>
            <form
              className="space-y-3"
              onSubmit={(event) => {
                event.preventDefault();
                create.mutate();
              }}
            >
              <Stars rating={rating} interactive onChange={setRating} />
              <Textarea
                value={comment}
                onChange={(event) => setComment(event.target.value)}
                maxLength={2000}
                placeholder="Chia sẻ trải nghiệm của bạn..."
                required
              />
              <label className="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground">
                <Upload className="size-4" />
                {fileLabel}
                <input
                  className="sr-only"
                  type="file"
                  accept="image/jpeg,image/png,image/webp"
                  multiple
                  onChange={(event) => setFiles(Array.from(event.target.files ?? []).slice(0, 5))}
                />
              </label>
              {error && <p className="text-sm text-destructive">{error}</p>}
              <Button type="submit" disabled={create.isPending}>
                {create.isPending ? "Đang đăng..." : "Đăng đánh giá"}
              </Button>
            </form>
          </CardContent>
        </Card>
      )}
      {reviews.isPending && (
        <p className="mt-3 text-sm text-muted-foreground">Đang tải đánh giá…</p>
      )}
      <div className="mt-4 space-y-3">
        {reviews.data?.reviews.map((review) => (
          <Card key={review.id}>
            <CardContent className="pt-6">
              <div className="flex items-center justify-between">
                <Stars rating={review.rating} />
                <span className="text-xs text-muted-foreground">
                  {review.buyer_name || "Người mua đã xác minh"} · {new Date(review.created_at).toLocaleDateString("vi-VN")}
                </span>
              </div>
              <p className="mt-2 whitespace-pre-wrap text-sm">{review.comment}</p>
              {review.images.length > 0 && (
                <div className="mt-3 flex gap-2 overflow-x-auto">
                  {review.images.map((image) => (
                    <img
                      key={image.id}
                      src={image.url}
                      alt="Ảnh từ người đánh giá"
                      className="size-20 rounded-md object-cover"
                    />
                  ))}
                </div>
              )}
              {review.reply && (
                <div className="mt-3 rounded-md bg-muted p-3 text-sm">
                  <p className="font-medium">Phản hồi từ shop</p>
                  <p className="mt-1 whitespace-pre-wrap">{review.reply.message}</p>
                </div>
              )}
            </CardContent>
          </Card>
        ))}
      </div>
      {reviews.data?.reviews.length === 0 && (
        <p className="mt-3 text-sm text-muted-foreground">Chưa có đánh giá nào.</p>
      )}
    </section>
  );
}
