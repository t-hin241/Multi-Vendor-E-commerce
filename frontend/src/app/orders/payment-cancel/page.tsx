"use client";

import { Suspense } from "react";

import { PaymentOutcome } from "@/components/orders/payment-outcome";

// The provider sends the buyer here when they leave its payment page. Its
// query string is a navigation hint only: the page reads the order from our
// API; cancelling the order goes through Order's cancel endpoint.
export default function PaymentCancelPage() {
  return (
    <Suspense fallback={null}>
      <PaymentOutcome returned={false} />
    </Suspense>
  );
}
