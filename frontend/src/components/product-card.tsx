"use client";

import { ImageOff } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import type { Product } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import { useAddCartItem } from "@/lib/hooks/use-cart";

export function ProductCard({ product }: { product: Product }) {
  const router = useRouter();
  const { user, accessToken } = useAuth();
  const addToCart = useAddCartItem();
  const canAddToCart = user?.role === "buyer";
  const isAdding = addToCart.isPending && addToCart.variables?.productId === product.id;

  async function handleBuyNow() {
    try {
      // Storefront listings deliberately omit variants. Resolve the public
      // detail before changing the cart so a variant product never receives
      // an invalid product-only cart request. The buyer chooses its option
      // on the product page, then its Buy action continues to checkout.
      const detail = await api.getProductBySlug(product.slug, accessToken ?? undefined);
      if ((detail.variants?.length ?? 0) > 0) {
        router.push(`/products/${product.slug}`);
        return;
      }

      await addToCart.mutateAsync({ productId: product.id });
      router.push("/checkout");
    } catch {
      // The cart mutation displays the failure message.
    }
  }

  return (
    <Card className="group gap-0 overflow-hidden py-0 transition-shadow hover:shadow-md hover:ring-1 hover:ring-primary/30">
      <Link href={`/products/${product.slug}`} className="block overflow-hidden">
        {product.images?.[0] ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={product.images[0].url}
            alt=""
            className="aspect-square w-full object-cover transition-transform duration-200 group-hover:scale-105"
          />
        ) : (
          <div className="flex aspect-square w-full items-center justify-center bg-muted">
            <ImageOff className="size-8 text-muted-foreground" />
          </div>
        )}
      </Link>
      <CardContent className="flex flex-col gap-1 p-3">
        <Link
          href={`/products/${product.slug}`}
          className="line-clamp-2 min-h-10 text-sm font-medium text-foreground hover:underline"
        >
          {product.name}
        </Link>
        <p className="truncate text-xs text-muted-foreground">{product.vendor_name || "—"}</p>
        <p className="text-sm font-semibold text-primary">
          {formatMoney(product.price_amount, product.currency)}
        </p>
        <p className="text-xs text-muted-foreground">Đã bán {product.quantity_sold ?? 0}</p>
        {canAddToCart && (
          <Button size="sm" className="mt-2" disabled={isAdding} onClick={handleBuyNow}>
            {isAdding ? "Đang thêm…" : "Mua ngay"}
          </Button>
        )}
      </CardContent>
    </Card>
  );
}
