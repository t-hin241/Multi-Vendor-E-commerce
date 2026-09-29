"use client";

import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";

// The provider redirect is informational only. The destination order page
// fetches its state again from our API, which is updated exclusively by the
// signature-verified server webhook.
function PaymentReturnRedirect() {
  const router = useRouter();
  const params = useSearchParams();
  useEffect(() => {
    const orderId = params.get("order_id");
    router.replace(orderId ? `/orders/${orderId}` : "/orders");
  }, [params, router]);
  return null;
}

export default function PaymentReturnPage() {
  return (
    <Suspense fallback={null}>
      <PaymentReturnRedirect />
    </Suspense>
  );
}
