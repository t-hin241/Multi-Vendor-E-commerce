"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";

export const ORDERS_PAGE_SIZE = 10;

// The order service doesn't return a total count, so pagination here fetches
// one extra row per page to detect whether a next page exists (see
// orders/page.tsx, which slices the displayed list back down to
// ORDERS_PAGE_SIZE).
export function useOrders(page: number, enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.ordersMine(page),
    queryFn: () =>
      callWithAuth((token) =>
        api.listMyOrders(token, {
          limit: ORDERS_PAGE_SIZE + 1,
          offset: (page - 1) * ORDERS_PAGE_SIZE,
        }),
      ),
    enabled,
  });
}

// useOrder reads the order; while Order is still holding stock for it
// (checkout_state "preparing") it looks again every 2s, for up to 2 minutes.
export function useOrder(orderId: string, enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.order(orderId),
    queryFn: () => callWithAuth((token) => api.getOrder(token, orderId)),
    enabled,
    refetchInterval: (query) =>
      query.state.data?.checkout_state === "preparing" && query.state.dataUpdateCount < 60
        ? 2000
        : false,
  });
}

export function useCancelOrder(orderId: string) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => callWithAuth((token) => api.cancelOrder(token, orderId)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.order(orderId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.ordersMine() });
      toast.success("Đã hủy đơn hàng.");
    },
    onError: (err) => toast.error(describeApiError(err, "Không thể hủy đơn hàng.")),
  });
}

export function useCreateReturnRequest(orderId: string) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vars: {
      orderItemId: string;
      quantity: number;
      reason: string;
      evidence?: string;
    }) => callWithAuth((token) => api.createReturnRequest(token, orderId, vars)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.order(orderId) });
      toast.success("Đã gửi yêu cầu trả hàng. Người bán sẽ xác nhận trước khi sàn duyệt.");
    },
    onError: (err) => toast.error(describeApiError(err, "Không thể gửi yêu cầu trả hàng.")),
  });
}
