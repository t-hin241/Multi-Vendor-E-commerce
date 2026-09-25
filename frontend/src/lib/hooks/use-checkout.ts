"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";

export function useCheckout() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const router = useRouter();
  return useMutation({
    mutationFn: (addressId: string) => callWithAuth((token) => api.checkout(token, addressId)),
    onSuccess: (order) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.cart() });
      toast.success("Order placed.");
      router.push(`/orders/${order.id}`);
    },
    onError: (err) => toast.error(describeApiError(err, "Checkout failed.")),
  });
}
