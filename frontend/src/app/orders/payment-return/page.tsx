"use client";

import { Suspense } from "react";

import { PaymentOutcome } from "@/components/orders/payment-outcome";

// The provider sends the buyer here after paying. Its query string is a
// navigation hint only: the page reads the order from our API, which only
// Payment's signature-verified webhook can mark paid.
export default function PaymentReturnPage() {
  return (
    <Suspense fallback={null}>
      <PaymentOutcome returned={true} />
    </Suspense>
  );
}
