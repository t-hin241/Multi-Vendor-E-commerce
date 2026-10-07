"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect } from "react";

import { PageShell } from "@/components/page-shell";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { SupportStatusBadge } from "@/components/support/support-status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { useAuth } from "@/lib/auth-context";
import { useSupportCases } from "@/lib/hooks/use-support-cases";
import { supportCategoryLabel } from "@/lib/support-cases";

// The buyer's support cases, newest first. Cases are opened from an order
// page ("Cần hỗ trợ").
export default function MySupportCasesPage() {
  const { user, isReady } = useAuth();
  const router = useRouter();
  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) router.replace("/login");
  }, [isReady, user, router]);
  const enabled = Boolean(user && user.role === "buyer");
  const cases = useSupportCases("buyer", {}, enabled);

  if (!enabled) return null;
  const items = cases.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <PageShell maxWidth="sm">
      <h1 className="text-2xl font-semibold tracking-tight">Yêu cầu hỗ trợ</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        Mở yêu cầu mới từ trang chi tiết của{" "}
        <Link href="/orders" className="text-primary underline">
          đơn hàng
        </Link>
        .
      </p>
      <div className="mt-6 flex flex-col gap-2">
        {cases.isPending && <LoadingState rows={3} />}
        {cases.error && (
          <ErrorState message="Không tải được yêu cầu hỗ trợ." onRetry={cases.refetch} />
        )}
        {cases.data && items.length === 0 && <EmptyState title="Bạn chưa có yêu cầu hỗ trợ nào" />}
        {items.map((c) => (
          <Link key={c.id} href={`/support/${c.id}`}>
            <Card className="transition-colors hover:bg-muted/40">
              <CardContent className="flex flex-wrap items-center justify-between gap-2 text-sm">
                <div>
                  <p className="font-medium">{supportCategoryLabel(c.category)}</p>
                  <p className="text-muted-foreground">
                    Mã #{c.id.slice(0, 8)} · đơn #{c.order_id.slice(0, 8)} ·{" "}
                    {new Date(c.created_at).toLocaleDateString("vi-VN")}
                  </p>
                </div>
                <SupportStatusBadge status={c.status} />
              </CardContent>
            </Card>
          </Link>
        ))}
        {cases.hasNextPage && (
          <Button
            variant="outline"
            disabled={cases.isFetchingNextPage}
            onClick={() => cases.fetchNextPage()}
          >
            {cases.isFetchingNextPage ? "Đang tải…" : "Xem thêm"}
          </Button>
        )}
      </div>
    </PageShell>
  );
}
