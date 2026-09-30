"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { isCartChanged } from "@/lib/cart-status";
import { describeApiError } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";

// Places the order for the cart version the buyer reviewed. The final
// amount is decided by Order from live prices; the cart subtotal shown
// before is only an estimate.
export function useCheckout() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const router = useRouter();
  return useMutation({
    mutationFn: (vars: { addressId: string; cartVersion: number }) =>
      callWithAuth((token) => api.checkout(token, vars.addressId, vars.cartVersion)),
    onSuccess: (order) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
      toast.success("Đặt hàng thành công.");
      router.push(`/orders/${order.id}`);
    },
    onError: (err) => {
      if (isCartChanged(err)) {
        queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
        toast.warning(
          `${describeApiError(err)} Giỏ hàng đã được tải lại, vui lòng kiểm tra rồi đặt hàng lại.`,
        );
        return;
      }
      if (err instanceof api.ApiError && err.status === 409) {
        // E.g. an earlier order of this cart is still being finalized:
        // point the buyer to their orders instead of letting them buy twice.
        queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
        toast.error(describeApiError(err), {
          action: { label: "Xem đơn hàng", onClick: () => router.push("/orders") },
        });
        return;
      }
      toast.error(describeApiError(err, "Đặt hàng thất bại. Vui lòng thử lại."));
    },
  });
}
