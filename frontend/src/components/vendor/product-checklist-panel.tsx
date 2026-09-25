"use client";

import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, Circle } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import {
  ProductImageEditor,
  ProductMediaGallery,
  ProductMediaUploader,
} from "@/components/vendor/product-media";
import { ProductStockManager, useHasStock } from "@/components/vendor/product-stock";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function ChecklistRow({
  done,
  label,
  optional,
  children,
}: {
  done: boolean;
  label: string;
  optional?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2 text-sm font-medium">
        {done ? (
          <CheckCircle2 className="size-4 text-success" />
        ) : (
          <Circle className="size-4 text-muted-foreground" />
        )}
        {label}
        {optional && (
          <Badge variant="outline" className="font-normal">
            Tùy chọn
          </Badge>
        )}
      </div>
      <div className="pl-6">{children}</div>
    </div>
  );
}

// The just-created product's setup flow, as an explicit task checklist
// (spec: "polish panel success thành task checklist") -- image and stock
// are the two things the backend actually requires before it'll accept a
// submit-for-review call; media stays visually marked optional. These
// checkmarks are hints only, matching the pre-existing, intentional design
// that the backend is the authority on completeness: submit stays
// clickable regardless of checklist state, and its own error message is
// what actually gates it.
export function ProductChecklistPanel({
  vendorId,
  product,
  onFinishLater,
  onSubmitForReview,
  isSubmitting,
}: {
  vendorId: string;
  product: api.Product;
  onFinishLater: () => void;
  onSubmitForReview: () => void;
  isSubmitting: boolean;
}) {
  const { callWithAuth } = useAuth();
  const imageQuery = useQuery({
    queryKey: ["product-image", product.id],
    queryFn: () => callWithAuth((token) => api.listProductImages(token, product.id)),
  });
  const hasImage = (imageQuery.data?.length ?? 0) > 0;
  const hasStock = useHasStock(vendorId, product.id, product.category_id);

  return (
    <Card className="border-success/40 bg-success/5">
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="font-medium">
            &ldquo;{product.name}&rdquo; đã được tạo — hoàn tất các bước bên dưới rồi gửi duyệt
          </h2>
          <Button type="button" variant="ghost" size="sm" onClick={onFinishLater}>
            Hoàn tất sau
          </Button>
        </div>

        <Separator />

        <ChecklistRow done={hasImage} label="Ảnh đại diện">
          <ProductImageEditor productId={product.id} />
        </ChecklistRow>

        <Separator />

        <ChecklistRow done={hasStock} label="Tồn kho">
          <ProductStockManager
            vendorId={vendorId}
            productId={product.id}
            categoryId={product.category_id}
          />
        </ChecklistRow>

        <Separator />

        <ChecklistRow done label="Media khác" optional>
          <ProductMediaUploader productId={product.id} />
          <div className="mt-2">
            <ProductMediaGallery productId={product.id} />
          </div>
        </ChecklistRow>

        <Button type="button" onClick={onSubmitForReview} disabled={isSubmitting} className="self-start">
          {isSubmitting ? "Đang gửi…" : "Gửi duyệt"}
        </Button>
      </CardContent>
    </Card>
  );
}
