"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";

export function useCreatePaymentIntent(orderId: string) {
  const { callWithAuth } = useAuth();
  return useMutation({
    mutationFn: () => callWithAuth((token) => api.createPaymentIntent(token, orderId)),
    onError: (err) => toast.error(describeApiError(err, "Could not start payment.")),
  });
}

export function useSimulatePaymentOutcome(orderId: string) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vars: { intentId: string; outcome: "succeeded" | "failed" }) =>
      callWithAuth((token) =>
        api.simulatePaymentOutcome(
          token,
          vars.intentId,
          vars.outcome,
          vars.outcome === "failed" ? "insufficient_funds" : undefined,
        ),
      ),
    onSuccess: (_intent, vars) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.order(orderId) });
      if (vars.outcome === "succeeded") {
        toast.success("Payment succeeded.");
      } else {
        toast.error("Payment failed.");
      }
    },
    onError: (err) => toast.error(describeApiError(err, "Could not process payment.")),
  });
}
