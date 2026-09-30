"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useRef } from "react";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { checkoutAttempt, checkoutErrorKind, type CheckoutAttempt } from "@/lib/order-workflow";
import { queryKeys } from "@/lib/query-keys";

// Order's price for the cart at an address, with shipping quoted per shop.
// Keyed by cart version so any cart change re-quotes.
export function useCheckoutPreview(addressId: string, cartVersion: number, enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.checkoutPreview(addressId, cartVersion),
    queryFn: () => callWithAuth((token) => api.previewCheckout(token, addressId)),
    enabled: enabled && Boolean(addressId),
    staleTime: 30_000,
    retry: false,
  });
}

// Places the order for the cart version and total the buyer confirmed. The
// Idempotency-Key stays the same while the input does, so a retry after a
// timeout returns the order already created instead of a second one.
export function useCheckout() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const router = useRouter();
  const attempt = useRef<CheckoutAttempt | null>(null);
  return useMutation({
    mutationFn: (vars: { addressId: string; cartVersion: number; expectedTotalAmount: number }) => {
      attempt.current = checkoutAttempt(attempt.current, vars);
      const idempotencyKey = attempt.current.key;
      return callWithAuth((token) => api.checkout(token, { ...vars, idempotencyKey }));
    },
    onSuccess: (order) => {
      attempt.current = null;
      queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
      queryClient.invalidateQueries({ queryKey: queryKeys.ordersMine() });
      toast.success("Đặt hàng thành công.");
      router.push(`/orders/${order.id}`);
    },
    onError: (err) => {
      const refresh = () => {
        queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
        queryClient.invalidateQueries({ queryKey: queryKeys.checkoutPreviewAll() });
      };
      switch (checkoutErrorKind(err)) {
        case "cart_changed":
          refresh();
          toast.warning(
            `${describeApiError(err)} Giỏ hàng đã được tải lại, vui lòng kiểm tra rồi đặt hàng lại.`,
          );
          return;
        case "total_changed":
          refresh();
          toast.warning("Tổng tiền đã thay đổi. Vui lòng xem lại tổng mới rồi xác nhận lại.");
          return;
        case "shipping_unavailable":
          refresh();
          toast.error(describeApiError(err, "Chưa hỗ trợ giao hàng đến địa chỉ này."));
          return;
        case "in_progress":
          // Same key: pressing again later returns the order being created.
          toast.info("Đơn hàng đang được tạo. Vui lòng đợi vài giây rồi bấm lại.");
          return;
      }
      if (err instanceof api.ApiError && err.status === 409) {
        refresh();
        toast.error(describeApiError(err), {
          action: { label: "Xem đơn hàng", onClick: () => router.push("/orders") },
        });
        return;
      }
      toast.error(describeApiError(err, "Đặt hàng thất bại. Vui lòng thử lại."));
    },
  });
}
