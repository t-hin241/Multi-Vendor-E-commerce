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

export function useOrder(orderId: string, enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.order(orderId),
    queryFn: () => callWithAuth((token) => api.getOrder(token, orderId)),
    enabled,
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
      toast.success("Order cancelled.");
    },
    onError: (err) => toast.error(describeApiError(err, "Could not cancel order.")),
  });
}
