"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function formatMoney(amount: number, currency = "VND") {
  return `${amount.toLocaleString("vi-VN")} ${currency}`;
}

export default function CartPage() {
  const { user, isReady, callWithAuth } = useAuth();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const [isCheckingOut, setIsCheckingOut] = useState(false);
  const [selectedAddressId, setSelectedAddressId] = useState<string>("");

  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  const cartQuery = useQuery({
    queryKey: ["cart"],
    queryFn: () => callWithAuth((token) => api.getCart(token)),
    enabled: Boolean(user && user.role === "buyer"),
  });

  const addressesQuery = useQuery({
    queryKey: ["buyer-addresses"],
    queryFn: () => callWithAuth((token) => api.listBuyerAddresses(token)),
    enabled: Boolean(user && user.role === "buyer"),
  });

  // Derived, not effect-driven: falls back to the default (or first) saved
  // address until the buyer explicitly picks a different one, without a
  // render-triggering setState in an effect.
  const addresses = addressesQuery.data ?? [];
  const defaultAddressId = addresses.find((a) => a.is_default)?.id ?? addresses[0]?.id ?? "";
  const effectiveAddressId = selectedAddressId || defaultAddressId;

  async function handleSetQuantity(productId: string, quantity: number, variantId?: string) {
    setError(null);
    try {
      if (quantity <= 0) {
        await callWithAuth((token) => api.removeCartItem(token, productId, variantId));
      } else {
        await callWithAuth((token) => api.setCartItemQuantity(token, productId, quantity, variantId));
      }
      await queryClient.invalidateQueries({ queryKey: ["cart"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update cart.");
    }
  }

  async function handleCheckout() {
    if (!effectiveAddressId) {
      setError("Please choose a shipping address before checking out.");
      return;
    }
    setError(null);
    setIsCheckingOut(true);
    try {
      const order = await callWithAuth((token) => api.checkout(token, effectiveAddressId));
      await queryClient.invalidateQueries({ queryKey: ["cart"] });
      router.push(`/orders/${order.id}`);
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Checkout failed.");
    } finally {
      setIsCheckingOut(false);
    }
  }

  if (!user || user.role !== "buyer") return null;

  return (
    <main className="mx-auto max-w-2xl px-6 py-10">
      <h1 className="text-2xl font-semibold text-slate-900">Your cart</h1>

      {cartQuery.isPending && <p className="mt-6 text-sm text-slate-500">Loading…</p>}
      {error && <p className="mt-4 text-sm text-red-600">{error}</p>}

      {cartQuery.data && cartQuery.data.items.length === 0 && (
        <p className="mt-6 text-sm text-slate-500">Your cart is empty.</p>
      )}

      {cartQuery.data && cartQuery.data.items.length > 0 && (
        <>
          <ul className="mt-6 divide-y divide-slate-200 rounded border border-slate-200 bg-white">
            {cartQuery.data.items.map((line) => (
              <li
                key={line.variant_id ? `${line.product_id}:${line.variant_id}` : line.product_id}
                className="flex items-center justify-between gap-4 p-4"
              >
                <div>
                  <p className="font-medium text-slate-900">
                    {line.product_name ?? line.product_id}
                  </p>
                  {line.variant_label && (
                    <p className="text-sm text-slate-500">{line.variant_label}</p>
                  )}
                  <p className="text-sm text-slate-600">
                    {formatMoney(line.price_amount, line.currency)} each
                  </p>
                  {!line.available && <p className="text-sm text-red-600">No longer available</p>}
                </div>
                <div className="flex items-center gap-3">
                  <input
                    type="number"
                    min={0}
                    defaultValue={line.quantity}
                    onBlur={(e) =>
                      handleSetQuantity(line.product_id, Number(e.target.value), line.variant_id)
                    }
                    className="w-16 rounded border border-slate-300 px-2 py-1 text-sm"
                  />
                  <span className="w-28 text-right text-sm text-slate-700">
                    {formatMoney(line.subtotal, line.currency)}
                  </span>
                  <button
                    onClick={() => handleSetQuantity(line.product_id, 0, line.variant_id)}
                    className="text-sm text-red-600 hover:underline"
                  >
                    Remove
                  </button>
                </div>
              </li>
            ))}
          </ul>

          <div className="mt-6 rounded border border-slate-200 bg-white p-4">
            <p className="text-sm font-medium text-slate-700">Shipping address</p>
            {addresses.length > 0 ? (
              <select
                value={effectiveAddressId}
                onChange={(e) => setSelectedAddressId(e.target.value)}
                className="mt-2 w-full rounded border border-slate-300 px-3 py-2 text-sm"
              >
                {addresses.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.recipient_name} — {a.street_address}, {a.ward}, {a.district}, {a.province}
                    {a.is_default ? " (default)" : ""}
                  </option>
                ))}
              </select>
            ) : (
              <p className="mt-2 text-sm text-slate-500">
                You have no saved address yet.{" "}
                <a href="/addresses" className="text-slate-900 underline">
                  Add one
                </a>{" "}
                before checking out.
              </p>
            )}
            <a href="/addresses" className="mt-2 inline-block text-xs text-slate-500 underline">
              Manage addresses
            </a>
          </div>

          <div className="mt-4 flex items-center justify-between">
            <p className="text-lg font-semibold text-slate-900">
              Total: {formatMoney(cartQuery.data.total)}
            </p>
            <button
              onClick={handleCheckout}
              disabled={isCheckingOut || !effectiveAddressId}
              className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
            >
              {isCheckingOut ? "Placing order…" : "Checkout"}
            </button>
          </div>
        </>
      )}
    </main>
  );
}
