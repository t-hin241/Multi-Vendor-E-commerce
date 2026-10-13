"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { clearAttempt, nextAttempt } from "@/lib/checkout-attempt";
import { checkoutErrorKind } from "@/lib/order-workflow";
import { selectionKey } from "@/lib/cart-selection";
import { useDebouncedValue } from "@/lib/use-debounced-value";
import { queryKeys } from "@/lib/query-keys";

// Order's price for the cart at an address, with shipping quoted per shop.
// Keyed by cart version so any cart change re-quotes.
export function useCheckoutPreview(
  addressId: string,
  cartVersion: number,
  enabled: boolean,
  selectedLineIds?: string[],
) {
  const { user, callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const key = selectionKey(selectedLineIds);
  const requestKey = JSON.stringify([addressId, cartVersion, key]);
  const debouncedKey = useDebouncedValue(requestKey, 300);
  const settled = requestKey === debouncedKey;
  const query = useQuery({
    queryKey: [...queryKeys.checkoutPreview(addressId, cartVersion), user?.id, key],
    queryFn: () =>
      callWithAuth((token) => api.previewCheckout(token, addressId, cartVersion, selectedLineIds)),
    enabled: enabled && Boolean(addressId) && settled,
    staleTime: 0,
    retry: false,
  });
  useEffect(() => {
    if (checkoutErrorKind(query.error) === "cart_changed") {
      void queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
    }
  }, [query.error, queryClient]);
  // Never expose the preceding selection/address quote during debounce or refresh.
  return {
    ...query,
    data: enabled && settled && !query.isFetching ? query.data : undefined,
    isFetching: query.isFetching || !settled,
  };
}

// Places the order for the cart version and total the buyer confirmed. The
// Idempotency-Key stays the same while the input does -- also across a
// reload, a lost response or another tab (see checkout-attempt) -- so a
// retry returns the order already created instead of a second one.
// Disabling the button is only convenience; the key is the guarantee.
export function useCheckout() {
  const { user, callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const router = useRouter();
  return useMutation({
    mutationFn: (vars: {
      addressId: string;
      cartVersion: number;
      expectedTotalAmount: number;
      acceptedPolicyVersions?: Record<string, number>;
      expectedQuoteVersion?: string;
      selectedLineIds?: string[];
    }) => {
      if (!user) throw new api.ApiError(401, "unauthorized", "You must be signed in.");
      const { key } = nextAttempt(user.id, vars);
      return callWithAuth((token) => api.checkout(token, { ...vars, idempotencyKey: key }));
    },
    onSuccess: (order) => {
      if (user) clearAttempt(user.id);
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
        case "policy_changed":
          refresh();
          toast.warning(
            "Chính sách của sàn vừa có phiên bản mới. Vui lòng xem lại chính sách rồi đặt hàng lại.",
          );
          return;
        case "in_progress":
          // Same key: pressing again later returns the order being created.
          toast.info("Đơn hàng đang được tạo. Vui lòng đợi vài giây rồi bấm lại.");
          return;
      }
      if (api.isOutcomeUnknown(err)) {
        // The order may exist. The same key is kept: pressing again returns
        // it instead of creating a second one.
        queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
        queryClient.invalidateQueries({ queryKey: queryKeys.ordersMine() });
        toast.warning(
          "Chưa rõ đơn hàng đã được tạo chưa. Bấm Đặt hàng lần nữa sẽ không tạo đơn trùng, hoặc xem Đơn hàng của tôi.",
          { action: { label: "Đơn hàng của tôi", onClick: () => router.push("/orders") } },
        );
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
