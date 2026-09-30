"use client";

import { useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import type { PaymentIntent } from "@/lib/api-client";
import { useCreatePaymentIntent, useSimulatePaymentOutcome } from "@/lib/hooks/use-payments";

// Stands in for a real hosted checkout page: this deployment runs the mock
// payment provider (no real Stripe/PayPal account configured yet), so
// paying "for real" means starting a payment intent and then telling it how
// the (fictitious) payment went — which exercises the same
// signature-verified webhook path a real provider's callback would.
export function PaymentSection({ orderId }: { orderId: string }) {
  const [intent, setIntent] = useState<PaymentIntent | null>(null);
  const createIntent = useCreatePaymentIntent(orderId);
  const simulate = useSimulatePaymentOutcome(orderId);

  async function handleStartPayment() {
    try {
      const created = await createIntent.mutateAsync();

      // A real provider owns its checkout UI. The browser redirect is only
      // navigation; Payment still waits for its signed webhook before Order
      // can become paid.
      if (created.checkout_url) {
        window.location.assign(created.checkout_url);
        return;
      }
      setIntent(created);
    } catch {
      // toasted by the mutation's onError
    }
  }

  if (!intent) {
    return (
      <Button onClick={handleStartPayment} disabled={createIntent.isPending}>
        {createIntent.isPending ? "Đang khởi tạo…" : "Thanh toán ngay"}
      </Button>
    );
  }

  if (intent.checkout_url) {
    return (
      <Card className="w-full max-w-sm">
        <CardContent className="flex flex-col gap-3">
          <Alert>
            <AlertDescription>Đang chuyển đến cổng thanh toán an toàn…</AlertDescription>
          </Alert>
          <Button asChild>
            <a href={intent.checkout_url}>Mở trang thanh toán</a>
          </Button>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="w-full max-w-sm">
      <CardContent className="flex flex-col gap-3">
        <Alert>
          <AlertDescription>
            Thanh toán thử nghiệm (sandbox) — không phát sinh giao dịch thật. Chọn một kết quả để mô
            phỏng:
          </AlertDescription>
        </Alert>
        <div className="flex gap-2">
          <Button
            className="flex-1 bg-success text-success-foreground hover:bg-success/90"
            disabled={simulate.isPending}
            onClick={() => simulate.mutate({ intentId: intent.id, outcome: "succeeded" })}
          >
            Mô phỏng thành công
          </Button>
          <Button
            variant="destructive"
            className="flex-1"
            disabled={simulate.isPending}
            onClick={() => simulate.mutate({ intentId: intent.id, outcome: "failed" })}
          >
            Mô phỏng thất bại
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
