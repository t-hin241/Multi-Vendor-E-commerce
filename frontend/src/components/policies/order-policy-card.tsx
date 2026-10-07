"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { policyKindLabel, policyVersionHref, returnRulesSummary } from "@/lib/policies";

// OrderPolicyCard shows the policy versions and return rules an order was
// placed under (Order's snapshot), which later versions never change.
export function OrderPolicyCard({
  orderId,
  scope,
  title = "Chính sách áp dụng cho đơn",
}: {
  orderId: string;
  scope: "buyer" | "admin";
  title?: string;
}) {
  const { callWithAuth } = useAuth();
  const query = useQuery({
    queryKey: ["order-policy-snapshot", scope, orderId],
    queryFn: () => callWithAuth((token) => api.getOrderPolicySnapshot(token, scope, orderId)),
    retry: false,
    staleTime: 5 * 60_000,
  });
  const view = query.data;
  if (!view) return null;

  return (
    <Card className="mt-4">
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-1 text-sm">
        {view.legacy || !view.order ? (
          <p className="text-muted-foreground">
            Đơn đặt trước khi có chính sách theo phiên bản: áp dụng quy định đổi trả tại thời điểm
            đặt hàng (phí vận chuyển không được hoàn khi trả hàng).
          </p>
        ) : (
          <>
            <p>{returnRulesSummary(view.order)}</p>
            {view.order.policies.map((p) => (
              <Link
                key={p.policy_id}
                href={policyVersionHref(p.kind, p.version)}
                className="text-primary underline"
              >
                {policyKindLabel(p.kind)} — phiên bản {p.version}
              </Link>
            ))}
            {view.order.source === "config" && (
              <p className="text-xs text-muted-foreground">
                Khi đặt đơn sàn chưa công bố chính sách đổi trả theo phiên bản; quy định trên được
                ghi lại cho đơn này.
              </p>
            )}
            {view.vendor_orders.some((v) => v.shop_policy) && (
              <p className="text-xs text-muted-foreground">
                Kèm chính sách bổ sung của shop đã được sàn duyệt tại thời điểm đặt hàng.
              </p>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}
