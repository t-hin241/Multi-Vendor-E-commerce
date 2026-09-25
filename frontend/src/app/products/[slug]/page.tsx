"use client";

import DOMPurify from "dompurify";
import { Store } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useMemo, useRef, useState } from "react";

import { PageShell } from "@/components/page-shell";
import { ProductGallery, type GalleryItem } from "@/components/product-gallery";
import { QuantityStepper } from "@/components/quantity-stepper";
import { ReadMore } from "@/components/read-more";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import type { ProductVariant } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { removeNonSizeImages } from "@/lib/description-images";
import { formatMoney } from "@/lib/format";
import { useAddCartItem } from "@/lib/hooks/use-cart";
import { useProduct } from "@/lib/hooks/use-product";
import { cn } from "@/lib/utils";

function variantLabel(variant: ProductVariant): string {
  return variant.options.map((o) => o.option_value).join(" / ");
}

// Scraped (Tiki) descriptions are HTML; descriptions from the normal
// vendor-submitted flow are plain text. There's no separate flag for this,
// so detect it from the content itself.
function looksLikeHtml(value: string): boolean {
  return /<([a-z][a-z0-9]*)\b[^>]*>/i.test(value);
}

export default function ProductDetailPage() {
  const params = useParams<{ slug: string }>();
  const { user } = useAuth();
  const [quantity, setQuantity] = useState(1);
  const [selectedVariantId, setSelectedVariantId] = useState<string>("");
  const [variantError, setVariantError] = useState<string | null>(null);
  const [buyBoxVisible, setBuyBoxVisible] = useState(true);
  const buyBoxRef = useRef<HTMLDivElement>(null);

  const productQuery = useProduct(params.slug);
  const addToCart = useAddCartItem();

  const hasVariants = (productQuery.data?.variants?.length ?? 0) > 0;

  useEffect(() => {
    const el = buyBoxRef.current;
    if (!el) return;
    // rootMargin offsets for the sticky nav bar's height (h-16 = 64px) so the
    // CTA row counts as "gone" once it's actually hidden behind the header,
    // not just once it's fully off-screen.
    const observer = new IntersectionObserver(
      ([entry]) => setBuyBoxVisible(!!entry?.isIntersecting),
      { rootMargin: "-64px 0px 0px 0px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
    // Re-attach once the product (and therefore the buy-box CTA row, which
    // only renders for buyer role after the product loads) is in the DOM --
    // the ref is null on the very first render.
  }, [productQuery.data]);

  // Scraped descriptions come back as raw HTML (Tiki formatting); sanitize
  // before embedding so it renders as content instead of literal tags, and
  // strips anything unsafe (scripts, event handlers, etc.) from the source.
  // DOMPurify needs a real DOM, so this only runs client-side — this page has
  // no server-fetched data anyway, so the description never renders during
  // the server pass regardless.
  const descriptionHtml = useMemo(() => {
    if (typeof window === "undefined") return "";
    const sanitized = DOMPurify.sanitize(productQuery.data?.description ?? "");
    return removeNonSizeImages(sanitized);
  }, [productQuery.data?.description]);

  function handleAddToCart() {
    if (!productQuery.data) return;
    if (hasVariants && !selectedVariantId) {
      setVariantError("Vui lòng chọn một tùy chọn trước khi thêm vào giỏ.");
      return;
    }
    setVariantError(null);
    addToCart.mutate({
      productId: productQuery.data.id,
      quantity,
      variantId: hasVariants ? selectedVariantId : undefined,
    });
  }

  if (productQuery.isPending) {
    return (
      <PageShell maxWidth="lg">
        <LoadingState rows={4} />
      </PageShell>
    );
  }

  if (productQuery.error || !productQuery.data) {
    return (
      <PageShell maxWidth="lg">
        {productQuery.error ? (
          <ErrorState message="Không thể tải sản phẩm này." onRetry={productQuery.refetch} />
        ) : (
          <EmptyState title="Không tìm thấy sản phẩm" />
        )}
      </PageShell>
    );
  }

  const product = productQuery.data;
  const isHtmlDescription = looksLikeHtml(product.description);
  // `media` is a superset of `images` (position 0 of each always matches --
  // see fetching_data/tiki_scraper/normalize/product.py), so it alone is the
  // full gallery; `images` is only a fallback for a product that somehow has
  // no media rows.
  const galleryItems: GalleryItem[] =
    product.media && product.media.length > 0
      ? product.media.map((m) => ({ id: m.id, kind: m.kind, url: m.url }))
      : (product.images ?? []).map((img) => ({ id: img.id, kind: "image" as const, url: img.url }));
  const canBuy = user?.role === "buyer";

  return (
    <PageShell maxWidth="lg" className="pb-24 md:pb-8">
      <div className="grid gap-8 md:grid-cols-2">
        {/* Gallery */}
        <div>
          <ProductGallery items={galleryItems} alt={product.name} />
        </div>

        {/* Buy box */}
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{product.name}</h1>

          {product.vendor_name ? (
            <Link
              href={`/shop/${product.vendor_id}`}
              className="mt-2 flex items-center gap-3 rounded-lg border p-3 hover:bg-muted/50"
            >
              <Store className="size-5 shrink-0 text-muted-foreground" />
              <div>
                <p className="text-sm font-medium">{product.vendor_name}</p>
                <p className="text-xs text-muted-foreground">Xem cửa hàng</p>
              </div>
            </Link>
          ) : (
            <div className="mt-2 flex items-center gap-3 rounded-lg border p-3">
              <Store className="size-5 shrink-0 text-muted-foreground" />
              <div>
                <p className="text-sm font-medium">Người bán ẩn danh</p>
                <p className="text-xs text-muted-foreground">Người bán trên Shopee Multi Vendor</p>
              </div>
            </div>
          )}

          <p className="mt-3 text-3xl font-semibold text-primary">
            {formatMoney(product.price_amount, product.currency)}
          </p>
          <p className="mt-1 text-sm text-muted-foreground">Đã bán {product.quantity_sold ?? 0}</p>
          {/* Only present for an admin or this product's own vendor viewing
              a non-variant product — see getProductBySlug/useProduct. A
              plain buyer never receives stock_quantity here. */}
          {!hasVariants && product.stock_quantity !== undefined && (
            <p className="mt-1 text-sm text-muted-foreground">
              Tồn kho:{" "}
              <span className={cn("font-medium", product.stock_quantity === 0 && "text-destructive")}>
                {product.stock_quantity === 0 ? "Hết hàng" : product.stock_quantity}
              </span>
            </p>
          )}

          <Separator className="my-4" />

          {hasVariants && (
            <div className="mb-4">
              <label className="mb-2 block text-sm font-medium" htmlFor="variant-select">
                Tùy chọn
              </label>
              <Select
                value={selectedVariantId}
                onValueChange={(value) => {
                  setSelectedVariantId(value);
                  setVariantError(null);
                }}
              >
                <SelectTrigger id="variant-select" className="w-full max-w-xs">
                  <SelectValue placeholder="Chọn một tùy chọn…" />
                </SelectTrigger>
                <SelectContent>
                  {product.variants!.map((variant) => {
                    const outOfStock = variant.available_quantity === 0;
                    return (
                      <SelectItem key={variant.id} value={variant.id} disabled={outOfStock}>
                        {variantLabel(variant)}
                        {outOfStock ? " (Hết hàng)" : ""}
                      </SelectItem>
                    );
                  })}
                </SelectContent>
              </Select>
              {product.stock_info_degraded && (
                <p className="mt-1 text-xs text-warning-foreground">
                  Thông tin tồn kho có thể chưa cập nhật.
                </p>
              )}
              {variantError && <p className="mt-1 text-sm text-destructive">{variantError}</p>}
            </div>
          )}

          {canBuy && (
            <div ref={buyBoxRef} className="flex items-center gap-3">
              <QuantityStepper value={quantity} min={1} onChange={setQuantity} ariaLabel="số lượng" />
              <Button
                size="lg"
                onClick={handleAddToCart}
                disabled={addToCart.isPending}
                className="flex-1"
              >
                {addToCart.isPending ? "Đang thêm…" : "Thêm vào giỏ"}
              </Button>
            </div>
          )}

          <p className="mt-3 text-xs text-muted-foreground">
            Giao hàng toàn quốc · Thanh toán qua ví sandbox khi đặt hàng
          </p>
        </div>
      </div>

      {/* Description */}
      <div className="mt-6">
        <h2 className="text-lg font-medium">Mô tả sản phẩm</h2>
        {isHtmlDescription ? (
          <Card className="mt-3">
            <ReadMore fadeFrom="card" className="px-(--card-spacing)">
              <CardContent
                className={cn(
                  "!px-0 text-sm text-muted-foreground [&_a]:text-primary [&_a]:underline [&_h1]:mb-2 [&_h1]:text-lg [&_h1]:font-semibold [&_h2]:mb-2 [&_h2]:text-base [&_h2]:font-semibold [&_h3]:mb-1 [&_h3]:font-semibold [&_img]:my-2 [&_img]:max-w-full [&_img]:rounded [&_li]:mb-1 [&_ol]:mb-2 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:mb-2 [&_strong]:font-semibold [&_em]:italic [&_table]:w-full [&_table]:border-collapse [&_td]:border [&_td]:p-1.5 [&_th]:border [&_th]:p-1.5 [&_ul]:mb-2 [&_ul]:list-disc [&_ul]:pl-5",
                )}
                dangerouslySetInnerHTML={{ __html: descriptionHtml }}
              />
            </ReadMore>
          </Card>
        ) : (
          <ReadMore className="mt-3">
            <p className="text-sm whitespace-pre-wrap text-muted-foreground">
              {product.description}
            </p>
          </ReadMore>
        )}
      </div>

      {user === null && (
        <Alert className="mt-6">
          <AlertDescription>
            <a href="/login" className="font-medium text-primary underline">
              Đăng nhập
            </a>{" "}
            bằng tài khoản người mua để thêm sản phẩm vào giỏ hàng.
          </AlertDescription>
        </Alert>
      )}

      {canBuy && !buyBoxVisible && (
        <div className="fixed inset-x-0 bottom-0 z-30 flex items-center gap-3 border-t bg-background p-3 pb-[calc(env(safe-area-inset-bottom)+0.75rem)] shadow-lg md:hidden">
          <span className="flex-1 text-lg font-semibold text-primary">
            {formatMoney(product.price_amount, product.currency)}
          </span>
          <Button onClick={handleAddToCart} disabled={addToCart.isPending}>
            {addToCart.isPending ? "Đang thêm…" : "Thêm vào giỏ"}
          </Button>
        </div>
      )}
    </PageShell>
  );
}
