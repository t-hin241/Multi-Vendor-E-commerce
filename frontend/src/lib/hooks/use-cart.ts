"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";

export function useCart(enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.cart(),
    queryFn: () => callWithAuth((token) => api.getCart(token)),
    enabled,
  });
}

// Adds one unit of a product (or a specific variant) to the cart — the
// per-card "Add to cart" action on product grids and the product detail
// page's buy box.
export function useAddCartItem() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vars: { productId: string; quantity?: number; variantId?: string }) =>
      callWithAuth((token) =>
        api.addCartItem(token, vars.productId, vars.quantity ?? 1, vars.variantId),
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
      toast.success("Added to cart.");
    },
    onError: (err) => toast.error(describeApiError(err, "Could not add to cart.")),
  });
}

// Sets a cart line's quantity; a quantity of 0 or below removes the line —
// mirrors the single "commit" interaction the quantity input already has
// (no separate remove mutation needed, matches the existing UX exactly).
export function useSetCartItemQuantity() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { productId: string; quantity: number; variantId?: string }) => {
      if (vars.quantity <= 0) {
        await callWithAuth((token) => api.removeCartItem(token, vars.productId, vars.variantId));
      } else {
        await callWithAuth((token) =>
          api.setCartItemQuantity(token, vars.productId, vars.quantity, vars.variantId),
        );
      }
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.cart() }),
    onError: (err) => toast.error(describeApiError(err, "Could not update cart.")),
  });
}
