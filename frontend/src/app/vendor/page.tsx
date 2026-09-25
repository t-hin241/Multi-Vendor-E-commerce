"use client";

import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, DollarSign, Package, ShoppingCart } from "lucide-react";
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { SectionHeader } from "@/components/section-header";
import { StatCard } from "@/components/vendor/stat-card";
import { VendorStatusBadge } from "@/components/vendor/status-badges";
import { OrderStatusBadge } from "@/components/order-status-badge";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
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

  if (vendor.status !== "approved") {
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

  return <ApprovedDashboard vendorId={vendor.id} shopName={vendor.shop_name} />;
}

function ApprovedDashboard({ vendorId, shopName }: { vendorId: string; shopName: string }) {
  const { callWithAuth } = useAuth();

  const ordersQuery = useQuery({
    queryKey: ["vendor-orders", vendorId],
    queryFn: () => callWithAuth((token) => api.listVendorOrders(token, vendorId)),
  });
  const productsQuery = useQuery({
    queryKey: ["vendor-products", vendorId],
    queryFn: () => callWithAuth((token) => api.listMyProducts(token, vendorId)),
  });
  const inventoryQuery = useQuery({
    queryKey: ["vendor-inventory", vendorId],
    queryFn: () => callWithAuth((token) => api.listMyInventory(token, vendorId)),
  });
  const summaryQuery = useQuery({
    queryKey: ["vendor-summary", vendorId],
    queryFn: () => callWithAuth((token) => api.getVendorSummary(token, vendorId)),
  });

  const orders = ordersQuery.data ?? [];
  const products = productsQuery.data ?? [];
  const inventory = inventoryQuery.data ?? [];
  const summary = summaryQuery.data;

  const pendingOrders = orders.filter((o) => o.status === "paid" || o.status === "processing");
  const activeApprovedCount = products.filter((p) => p.status === "approved" && p.is_active).length;
  const sellableProductIds = new Set(
    products.filter((p) => p.status === "approved" && p.is_active).map((p) => p.id),
  );
  const outOfStockCount = inventory.filter(
    (i) => i.available_quantity === 0 && sellableProductIds.has(i.product_id),
  ).length;

  const needsAction = orders
    .filter((o) => o.status === "paid")
    .sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime())
    .slice(0, 5);

  return (
    <div className="flex flex-col gap-6">
      <SectionHeader as="h1" title={shopName} subtitle="Tổng quan cửa hàng" />

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="Đơn chờ xử lý" value={pendingOrders.length} icon={ShoppingCart} />
        <StatCard
          label="Sản phẩm"
          value={products.length}
          hint={`${activeApprovedCount} đang bán`}
          icon={Package}
        />
        <StatCard label="Cảnh báo tồn kho" value={outOfStockCount} hint="Sản phẩm hết hàng" icon={AlertTriangle} />
        <StatCard
          label="Doanh thu"
          value={
            summary && summary.total_orders > 0 ? formatMoney(summary.total_revenue) : "—"
          }
          hint={!summary || summary.total_orders === 0 ? "Chưa có dữ liệu doanh thu" : undefined}
          icon={DollarSign}
        />
      </div>

      <Card>
        <CardContent>
          <div className="flex items-center justify-between">
            <p className="font-medium">Cần xử lý</p>
            <Button variant="link" size="sm" className="h-auto p-0" asChild>
              <Link href="/vendor/orders">Xem tất cả đơn hàng</Link>
            </Button>
          </div>
          {needsAction.length === 0 ? (
            <p className="mt-2 text-sm text-muted-foreground">Không có đơn nào cần xử lý.</p>
          ) : (
            <ul className="mt-3 flex flex-col gap-2">
              {needsAction.map((vo) => (
                <li key={vo.id} className="flex items-center justify-between text-sm">
                  <span>Đơn #{vo.order_id.slice(0, 8)}</span>
                  <span className="flex items-center gap-2 text-muted-foreground">
                    {formatMoney(vo.subtotal_amount, vo.currency)}
                    <OrderStatusBadge status={vo.status} />
                  </span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
