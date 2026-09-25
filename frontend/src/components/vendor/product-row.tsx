"use client";

import { useQuery } from "@tanstack/react-query";
import { ImageOff } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import {
  ProductImageEditor,
  ProductMediaGallery,
  ProductMediaUploader,
} from "@/components/vendor/product-media";
import { ProductStockManager } from "@/components/vendor/product-stock";
import { ProductStatusBadge } from "@/components/vendor/status-badges";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";

// One product's list entry -- a hybrid card/table row (spec: "card/table
// hybrid: ảnh, tên, giá, status, active toggle, stock setup, media
// actions"). The top row reads like a compact table row at `sm+` and stacks
// like a card on mobile; the management controls (image/media/stock) sit
// below a divider, unchanged logic, just restyled.
export function ProductRow({
  vendorId,
  product,
  onToggleActive,
  onSubmitForReview,
}: {
  vendorId: string;
  product: api.Product;
  onToggleActive: (productId: string, isActive: boolean) => void;
  onSubmitForReview: (productId: string) => void;
}) {
  const { callWithAuth } = useAuth();
  const imageQuery = useQuery({
    queryKey: ["product-image", product.id],
    queryFn: () => callWithAuth((token) => api.listProductImages(token, product.id)),
  });
  const thumbnail = imageQuery.data?.[0];

  return (
    <Card>
      <CardContent>
        <div className="grid grid-cols-[auto_1fr] items-center gap-3 sm:grid-cols-[auto_1fr_auto_auto]">
          <div className="flex size-12 shrink-0 items-center justify-center rounded-lg bg-muted">
            {thumbnail ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={thumbnail.url} alt="" className="size-12 rounded-lg object-cover" />
            ) : (
              <ImageOff className="size-5 text-muted-foreground" />
            )}
          </div>

          <div className="min-w-0">
            <p className="truncate font-medium">{product.name}</p>
            <p className="text-sm text-muted-foreground">
              {formatMoney(product.price_amount, product.currency)}
            </p>
            <div className="mt-1 flex items-center gap-2">
              <ProductStatusBadge status={product.status} />
              {product.status === "rejected" && product.rejection_reason && (
                <span className="text-xs text-destructive">{product.rejection_reason}</span>
              )}
            </div>
          </div>

          <Label className="col-span-2 justify-self-start text-sm sm:col-span-1 sm:justify-self-center">
            <Switch
              checked={product.is_active}
              onCheckedChange={(checked) => onToggleActive(product.id, checked)}
            />
            Đang bán
          </Label>

          {product.status === "draft" && (
            <Button
              type="button"
              size="sm"
              className="col-span-2 sm:col-span-1"
              onClick={() => onSubmitForReview(product.id)}
            >
              Gửi duyệt
            </Button>
          )}
        </div>

        <Separator className="my-4" />

        <div className="flex flex-wrap items-start gap-4">
          <ProductImageEditor productId={product.id} />
          <ProductMediaUploader productId={product.id} />
        </div>
        <div className="mt-3">
          <ProductMediaGallery productId={product.id} />
        </div>
        <div className="mt-3">
          <ProductStockManager vendorId={vendorId} productId={product.id} categoryId={product.category_id} />
        </div>
      </CardContent>
    </Card>
  );
}
