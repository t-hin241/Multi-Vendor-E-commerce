"use client";

import { useQuery } from "@tanstack/react-query";
import { FinanceDashboard } from "@/components/vendor/finance-dashboard";
import { ShopReadiness } from "@/components/vendor/shop-readiness";
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { VendorStatusBadge } from "@/components/vendor/status-badges";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { resolveActiveVendor } from "@/lib/vendor";

export default function VendorDashboardPage() {
  const { callWithAuth, selectedVendorId } = useAuth();

  // Same query key as the shop switcher in the console shell -- React Query
  // dedupes it into one fetch, no prop drilling needed between them.
  const vendorsQuery = useQuery({
    queryKey: ["my-vendors"],
    queryFn: () => callWithAuth((token) => api.listMyVendors(token)),
  });

  if (vendorsQuery.isPending) {
    return <p className="text-sm text-muted-foreground">Đang tải…</p>;
  }

  const vendors = vendorsQuery.data ?? [];
  if (vendors.length === 0) {
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

  const vendor = resolveActiveVendor(vendors, selectedVendorId)!;

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
        <Link href="/vendor/shops" className="mt-4 inline-block text-sm text-primary underline">
          Quản lý cửa hàng
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">{vendor.shop_name}</h1>
      <ShopReadiness vendor={vendor} />
      <FinanceDashboard key={vendor.id} vendorId={vendor.id} />
    </div>
  );
}
