"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";

// paymentStartError words Payment's refusals for the buyer.
export function paymentStartError(err: unknown): string {
  if (err instanceof api.ApiError) {
    switch (err.code) {
      case "payment_in_progress":
        return "Đang chuẩn bị liên kết thanh toán. Vui lòng thử lại sau vài giây.";
      case "payment_provider_unavailable":
        return "Cổng thanh toán tạm thời không phản hồi. Vui lòng thử lại sau ít phút.";
    }
  }
  return describeApiError(err, "Không thể bắt đầu thanh toán.");
}

export function useCreatePaymentIntent(orderId: string) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => callWithAuth((token) => api.createPaymentIntent(token, orderId)),
    onError: (err) => {
      // The order may just have been paid through an earlier link.
      queryClient.invalidateQueries({ queryKey: queryKeys.order(orderId) });
      toast.error(paymentStartError(err));
    },
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
      // Order learns of the payment asynchronously; look again shortly.
      setTimeout(() => queryClient.invalidateQueries({ queryKey: queryKeys.order(orderId) }), 2000);
      if (vars.outcome === "succeeded") {
        toast.success("Payment succeeded.");
      } else {
        toast.error("Payment failed.");
      }
    },
    onError: (err) => toast.error(describeApiError(err, "Could not process payment.")),
  });
}

// AF-06: the buyer's refunds of one order, refreshed while one is open.
export function useMyRefunds(orderId: string, enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.myRefunds(orderId),
    queryFn: () => callWithAuth((token) => api.listMyRefunds(token, orderId)),
    enabled,
    refetchInterval: (query) =>
      query.state.data?.some((r) => r.stage !== "refunded" && r.stage !== "failed")
        ? 30_000
        : false,
  });
}

// refundDestinationError words Payment's refusals of a destination.
export function refundDestinationError(err: unknown): string {
  if (err instanceof api.ApiError) {
    switch (err.code) {
      case "destination_changed":
        return "Thông tin hoàn tiền vừa thay đổi. Vui lòng tải lại trang.";
      case "attempt_active":
        return "Sàn đang chuyển khoản cho yêu cầu này nên không thể đổi tài khoản lúc này.";
      case "feature_disabled":
      case "destination_key_unavailable":
        return "Tạm thời chưa nhận được thông tin tài khoản. Vui lòng thử lại sau hoặc liên hệ hỗ trợ.";
    }
  }
  return describeApiError(err, "Không gửi được thông tin tài khoản.");
}

export function useSubmitRefundDestination(orderId: string) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: {
      refundId: string;
      bank_code: string;
      account_number: string;
      account_name: string;
      expected_version: number;
    }) =>
      callWithAuth((token) => {
        const { refundId, ...body } = input;
        return api.submitRefundDestination(token, refundId, body);
      }),
    onSuccess: () => {
      toast.success("Đã gửi thông tin tài khoản. Sàn sẽ xác minh trước khi chuyển tiền.");
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.myRefunds(orderId) }),
  });
}
