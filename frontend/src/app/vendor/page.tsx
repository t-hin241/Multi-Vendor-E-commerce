"use client";

import { useQuery } from "@tanstack/react-query";
import { FinanceDashboard } from "@/components/vendor/finance-dashboard";
import { ShopReadiness } from "@/components/vendor/shop-readiness";
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { VendorStatusBadge } from "@/components/vendor/status-badges";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { useConsoleShops } from "@/lib/hooks/use-console-shops";
import { queryKeys } from "@/lib/query-keys";
import { can, isOwner } from "@/lib/shop-access";

export default function VendorDashboardPage() {
  const { callWithAuth, user } = useAuth();

  // Same query as the shop switcher in the console shell (AF-17: owned and
  // staffed shops) -- React Query dedupes it into one fetch.
  const { query: shopsQuery, activeShop } = useConsoleShops();
  const shopQuery = useQuery({
    queryKey: queryKeys.memberShop(activeShop?.id ?? ""),
    queryFn: () => callWithAuth((token) => api.getMemberShop(token, activeShop!.id)),
    enabled: Boolean(activeShop),
  });

  if (shopsQuery.isPending || (activeShop && shopQuery.isPending)) {
    return <p className="text-sm text-muted-foreground">Đang tải…</p>;
  }
  if (shopQuery.isError) {
    return <p className="text-sm text-destructive">Chưa tải được cửa hàng. Vui lòng thử lại.</p>;
  }

  if (!activeShop || !shopQuery.data) {
    if (user?.role !== "vendor") {
      return (
        <p className="text-sm text-muted-foreground">
          Bạn chưa là nhân viên của cửa hàng nào. Mở liên kết trong email mời để tham gia.
        </p>
      );
    }
    return (
      <div>
        <h1 className="text-xl font-semibold">Chào mừng</h1>
        <p className="mt-2 text-sm text-muted-foreground">Bạn chưa có cửa hàng nào.</p>
        <Button className="mt-4" asChild>
          <Link href="/vendor/shops">Đăng ký bán hàng</Link>
        </Button>
      </div>
    );
  }

  const vendor = shopQuery.data;
  const owner = isOwner(activeShop);

  if (vendor.status !== "approved" && vendor.status !== "suspended") {
    return (
      <div>
        <div className="flex items-center gap-3">
          <h1 className="text-xl font-semibold">{vendor.shop_name}</h1>
          <VendorStatusBadge status={vendor.status} />
        </div>
        {vendor.status === "rejected" && vendor.rejection_reason && (
          <p className="mt-2 text-sm text-destructive">Lý do: {vendor.rejection_reason}</p>
        )}
        {vendor.status === "pending" && (
          <p className="mt-2 text-sm text-muted-foreground">
            Đơn đăng ký của bạn đang chờ admin xét duyệt.
          </p>
        )}
        {owner && (
          <Link href="/vendor/shops" className="mt-4 inline-block text-sm text-primary underline">
            Quản lý cửa hàng
          </Link>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">{vendor.shop_name}</h1>
      {owner && <ShopReadiness vendor={vendor} />}
      {can(activeShop, "analytics.read") ? (
        <FinanceDashboard key={vendor.id} vendorId={vendor.id} />
      ) : (
        <p className="text-sm text-muted-foreground">
          Bạn không có quyền xem tổng quan bán hàng của cửa hàng này. Dùng menu bên trái để mở các
          mục được phân quyền.
        </p>
      )}
    </div>
  );
}
