"use client";

import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { isCartChanged } from "@/lib/cart-status";
import { describeApiError } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";

const CART_CHANGED_MESSAGE =
  "Giỏ hàng vừa thay đổi ở nơi khác. Đã tải lại, vui lòng kiểm tra rồi thao tác lại.";

export function useCart(enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.cart(),
    queryFn: () => callWithAuth((token) => api.getCart(token)),
    enabled,
  });
}

function cachedVersion(queryClient: QueryClient): number | undefined {
  return queryClient.getQueryData<api.Cart>(queryKeys.cart())?.version;
}

// Shared error handling: a version conflict means the buyer's view is stale,
// so reload it and say so instead of showing a generic failure.
function handleCartError(queryClient: QueryClient, err: unknown, fallback: string) {
  if (isCartChanged(err)) {
    queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
    toast.warning(CART_CHANGED_MESSAGE);
    return;
  }
  toast.error(describeApiError(err, fallback));
}

// Adds a product (or a specific variant) to the cart from product grids and
// the product detail page. Not conditional on a cart version: the buyer is
// not looking at the cart when they add.
export function useAddCartItem() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vars: { productId: string; quantity?: number; variantId?: string }) =>
      callWithAuth((token) =>
        api.addCartItem(token, vars.productId, vars.quantity ?? 1, vars.variantId),
      ),
    onSuccess: (cart) => {
      queryClient.setQueryData(queryKeys.cart(), cart);
      toast.success("Đã thêm vào giỏ hàng.");
    },
    onError: (err) => handleCartError(queryClient, err, "Không thể thêm vào giỏ hàng."),
  });
}

// Sets a cart line's quantity; 0 removes the line. Conditional on the cart
// version the buyer is looking at, so an edit from another tab is never
// silently overwritten.
export function useSetCartItemQuantity() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (vars: { productId: string; quantity: number; variantId?: string }) => {
      const version = cachedVersion(queryClient);
      if (vars.quantity <= 0) {
        await callWithAuth((token) =>
          api.removeCartItem(token, vars.productId, vars.variantId, version),
        );
        return null;
      }
      return callWithAuth((token) =>
        api.setCartItemQuantity(token, vars.productId, vars.quantity, vars.variantId, version),
      );
    },
    onSuccess: (cart) => {
      if (cart) queryClient.setQueryData(queryKeys.cart(), cart);
      else queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
    },
    onError: (err) => handleCartError(queryClient, err, "Không thể cập nhật giỏ hàng."),
  });
}

// Accepts the new prices currently shown for the changed lines.
export function useConfirmCartPrices() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vars: {
      version: number;
      lines: { line_id: string; price_amount: number; currency: string }[];
    }) => callWithAuth((token) => api.confirmCartPrices(token, vars.version, vars.lines)),
    onSuccess: (cart) => {
      queryClient.setQueryData(queryKeys.cart(), cart);
      toast.success("Đã xác nhận giá mới.");
    },
    onError: (err) => {
      if (isCartChanged(err)) {
        queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
        toast.warning("Giá vừa thay đổi thêm lần nữa. Vui lòng xem lại giá mới.");
        return;
      }
      toast.error(describeApiError(err, "Không thể xác nhận giá."));
    },
  });
}
