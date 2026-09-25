"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { OrderStatusBadge } from "@/components/order-status-badge";
import { PageShell } from "@/components/page-shell";
import { Pagination } from "@/components/pagination";
import { SectionHeader } from "@/components/section-header";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import { ORDERS_PAGE_SIZE, useOrders } from "@/lib/hooks/use-orders";

export default function OrdersPage() {
  const { user, isReady } = useAuth();
  const router = useRouter();
  const [page, setPage] = useState(1);

  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  const ordersQuery = useOrders(page, Boolean(user && user.role === "buyer"));
  const orders = ordersQuery.data?.slice(0, ORDERS_PAGE_SIZE) ?? [];
  const hasNextPage = (ordersQuery.data?.length ?? 0) > ORDERS_PAGE_SIZE;

  function handlePageChange(nextPage: number) {
    setPage(nextPage);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  if (!user || user.role !== "buyer") return null;

  return (
    <PageShell maxWidth="sm">
      <SectionHeader as="h1" title="Đơn hàng của tôi" />

      {ordersQuery.isPending && <LoadingState className="mt-6" rows={3} />}
      {ordersQuery.error && (
        <ErrorState message="Không thể tải danh sách đơn hàng." onRetry={ordersQuery.refetch} />
      )}
      {ordersQuery.data?.length === 0 && (
        <EmptyState
          className="mt-6"
          title="Chưa có đơn hàng nào"
          description="Đơn hàng bạn đặt sẽ hiển thị ở đây."
          action={
            <Button asChild>
              <Link href="/">Mua sắm ngay</Link>
            </Button>
          }
        />
      )}

      {orders.length > 0 && (
        <>
          <Card className="mt-6 gap-0 py-0">
            {orders.map((order, i) => (
              <div key={order.id}>
                {i > 0 && <Separator />}
                <CardContent className="flex items-center justify-between p-4">
                  <div>
                    <Link href={`/orders/${order.id}`} className="font-medium hover:underline">
                      Đơn hàng #{order.id.slice(0, 8)}
                    </Link>
                    <p className="text-sm text-muted-foreground">
                      {new Date(order.created_at).toLocaleString()}
                    </p>
                  </div>
                  <div className="text-right">
                    <OrderStatusBadge status={order.status} />
                    <p className="mt-1 text-sm font-medium">
                      {formatMoney(order.total_amount, order.currency)}
                    </p>
                  </div>
                </CardContent>
              </div>
            ))}
          </Card>
          <Pagination currentPage={page} hasNextPage={hasNextPage} onPageChange={handlePageChange} />
        </>
      )}
    </PageShell>
  );
}
