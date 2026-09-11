"use client";

import { useQuery } from "@tanstack/react-query";
import { useParams } from "next/navigation";
import { useState } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function formatMoney(amount: number, currency: string) {
  return `${amount.toLocaleString("vi-VN")} ${currency}`;
}

function variantLabel(variant: api.ProductVariant): string {
  return variant.options.map((o) => o.option_value).join(" / ");
}

export default function ProductDetailPage() {
  const params = useParams<{ slug: string }>();
  const { user, callWithAuth } = useAuth();
  const [quantity, setQuantity] = useState(1);
  const [selectedVariantId, setSelectedVariantId] = useState<string>("");
  const [message, setMessage] = useState<string | null>(null);
  const [isAdding, setIsAdding] = useState(false);

  const productQuery = useQuery({
    queryKey: ["product", params.slug],
    queryFn: () => api.getProductBySlug(params.slug),
  });

  const hasVariants = (productQuery.data?.variants?.length ?? 0) > 0;

  async function handleAddToCart() {
    if (!productQuery.data) return;
    if (hasVariants && !selectedVariantId) {
      setMessage("Please choose an option before adding to cart.");
      return;
    }
    setMessage(null);
    setIsAdding(true);
    try {
      await callWithAuth((token) =>
        api.addCartItem(
          token,
          productQuery.data.id,
          quantity,
          hasVariants ? selectedVariantId : undefined,
        ),
      );
      setMessage("Added to cart.");
    } catch (err) {
      setMessage(err instanceof api.ApiError ? err.message : "Could not add to cart.");
    } finally {
      setIsAdding(false);
    }
  }

  if (productQuery.isPending) {
    return <main className="mx-auto max-w-2xl px-6 py-10 text-sm text-slate-500">Loading…</main>;
  }

  if (productQuery.error || !productQuery.data) {
    return (
      <main className="mx-auto max-w-2xl px-6 py-10 text-sm text-red-600">Product not found.</main>
    );
  }

  const product = productQuery.data;

  return (
    <main className="mx-auto max-w-2xl px-6 py-10">
      {product.images && product.images.length > 0 && (
        <div className="mb-6 flex flex-wrap gap-2">
          {product.images.map((img) => (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              key={img.id}
              src={img.url}
              alt={product.name}
              className="h-40 w-40 rounded object-cover"
            />
          ))}
        </div>
      )}

      <h1 className="text-2xl font-semibold text-slate-900">{product.name}</h1>
      <p className="mt-2 text-lg text-slate-700">
        {formatMoney(product.price_amount, product.currency)}
      </p>
      <p className="mt-4 whitespace-pre-wrap text-sm text-slate-600">{product.description}</p>

      {product.media && product.media.length > 0 && (
        <div className="mt-6">
          <p className="text-sm font-medium text-slate-700">Other media description</p>
          <div className="mt-2 flex flex-wrap gap-2">
            {product.media.map((item) =>
              item.kind === "video" ? (
                <video
                  key={item.id}
                  controls
                  src={item.url}
                  className="h-48 w-64 rounded bg-slate-100"
                />
              ) : (
                // eslint-disable-next-line @next/next/no-img-element
                <img
                  key={item.id}
                  src={item.url}
                  alt=""
                  className="h-48 w-48 rounded object-cover"
                />
              ),
            )}
          </div>
        </div>
      )}

      {hasVariants && (
        <div className="mt-6">
          <label className="block text-sm font-medium text-slate-700" htmlFor="variant-select">
            Option
          </label>
          <select
            id="variant-select"
            value={selectedVariantId}
            onChange={(e) => setSelectedVariantId(e.target.value)}
            className="mt-1 w-full max-w-xs rounded border border-slate-300 px-3 py-2 text-sm"
          >
            <option value="">Choose an option…</option>
            {product.variants!.map((variant) => {
              const outOfStock = variant.available_quantity === 0;
              return (
                <option key={variant.id} value={variant.id} disabled={outOfStock}>
                  {variantLabel(variant)}
                  {outOfStock ? " (Out of stock)" : ""}
                </option>
              );
            })}
          </select>
          {product.stock_info_degraded && (
            <p className="mt-1 text-xs text-amber-600">
              Stock levels may be out of date right now.
            </p>
          )}
        </div>
      )}

      {user?.role === "buyer" && (
        <div className="mt-6 flex items-center gap-3">
          <input
            type="number"
            min={1}
            value={quantity}
            onChange={(e) => setQuantity(Math.max(1, Number(e.target.value)))}
            className="w-20 rounded border border-slate-300 px-3 py-2 text-sm"
          />
          <button
            onClick={handleAddToCart}
            disabled={isAdding || (hasVariants && !selectedVariantId)}
            className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
          >
            {isAdding ? "Adding…" : "Add to cart"}
          </button>
        </div>
      )}

      {message && <p className="mt-3 text-sm text-emerald-600">{message}</p>}
    </main>
  );
}
