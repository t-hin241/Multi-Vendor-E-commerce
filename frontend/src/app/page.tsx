"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { SystemStatus } from "@/components/system-status";

function formatMoney(amount: number, currency: string) {
  return `${amount.toLocaleString("vi-VN")} ${currency}`;
}

export default function StorefrontPage() {
  const { user, callWithAuth } = useAuth();
  const [categoryId, setCategoryId] = useState("");
  const [addingId, setAddingId] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const categoriesQuery = useQuery({ queryKey: ["categories"], queryFn: api.listCategories });
  const productsQuery = useQuery({
    queryKey: ["storefront-products", categoryId],
    queryFn: () => api.listStorefrontProducts({ categoryId: categoryId || undefined }),
  });

  async function handleAddToCart(productId: string) {
    setMessage(null);
    setAddingId(productId);
    try {
      await callWithAuth((token) => api.addCartItem(token, productId, 1));
      setMessage("Added to cart.");
    } catch (err) {
      setMessage(err instanceof api.ApiError ? err.message : "Could not add to cart.");
    } finally {
      setAddingId(null);
    }
  }

  const products = productsQuery.data?.products ?? [];
  const isDegraded =
    productsQuery.data?.vendor_info_degraded || productsQuery.data?.sales_info_degraded;

  return (
    <main className="mx-auto max-w-4xl px-6 py-10">
      <h1 className="text-2xl font-semibold text-slate-900">Storefront</h1>
      <div className="mt-2">
        <SystemStatus />
      </div>

      {/* Search bar: UI only for now, no filtering wired up yet. */}
      <input
        placeholder="Search products…"
        disabled
        className="mt-6 w-full rounded border border-slate-300 px-4 py-2.5 text-sm text-slate-500"
      />

      {/* Category boxes: clicking still filters the grid below, same as the
          category dropdown this replaces — only the placeholder image is a
          stand-in for real per-category photos. */}
      <div className="mt-6 flex flex-wrap gap-3">
        <button
          onClick={() => setCategoryId("")}
          className="flex w-20 flex-col items-center gap-1.5"
        >
          <span
            className={`flex h-16 w-16 items-center justify-center rounded border text-xs text-slate-400 ${
              categoryId === "" ? "border-slate-900" : "border-slate-200 bg-slate-100"
            }`}
          >
            All
          </span>
          <span className="text-center text-xs text-slate-700">All</span>
        </button>
        {categoriesQuery.data?.map((c) => (
          <button
            key={c.id}
            onClick={() => setCategoryId(c.id)}
            className="flex w-20 flex-col items-center gap-1.5"
          >
            <span
              className={`h-16 w-16 rounded border bg-slate-100 ${
                categoryId === c.id ? "border-slate-900" : "border-slate-200"
              }`}
            />
            <span className="line-clamp-2 text-center text-xs text-slate-700">{c.name}</span>
          </button>
        ))}
      </div>

      {message && <p className="mt-4 text-sm text-emerald-600">{message}</p>}
      {isDegraded && (
        <p className="mt-4 text-sm text-amber-600">
          Một số thông tin (tên shop / lượt bán) có thể chưa được cập nhật, vui lòng thử lại sau.
        </p>
      )}

      {productsQuery.isPending && <p className="mt-6 text-sm text-slate-500">Loading products…</p>}
      {productsQuery.error && <p className="mt-6 text-sm text-red-600">Could not load products.</p>}

      <ul className="mt-6 grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
        {products.map((product) => (
          <li key={product.id} className="rounded border border-slate-200 bg-white p-3">
            <Link href={`/products/${product.slug}`}>
              {product.images?.[0] ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img
                  src={product.images[0].url}
                  alt=""
                  className="h-32 w-full rounded object-cover"
                />
              ) : (
                <div className="h-32 w-full rounded bg-slate-100" />
              )}
              <p className="mt-2 font-medium text-slate-900 hover:underline">{product.name}</p>
            </Link>
            <p className="mt-0.5 text-xs text-slate-500">{product.vendor_name || "—"}</p>
            <p className="mt-1 text-sm font-semibold text-slate-900">
              {formatMoney(product.price_amount, product.currency)}
            </p>
            <p className="mt-0.5 text-xs text-slate-500">Đã bán {product.quantity_sold ?? 0}</p>
            {user?.role === "buyer" && (
              <button
                onClick={() => handleAddToCart(product.id)}
                disabled={addingId === product.id}
                className="mt-3 rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
              >
                {addingId === product.id ? "Adding…" : "Add to cart"}
              </button>
            )}
          </li>
        ))}
      </ul>

      {productsQuery.data && products.length === 0 && (
        <p className="mt-6 text-sm text-slate-500">No products found.</p>
      )}
    </main>
  );
}
