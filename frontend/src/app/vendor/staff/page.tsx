"use client";

import { SectionHeader } from "@/components/section-header";
import { StaffManager } from "@/components/vendor/staff-manager";
import { useConsoleShops } from "@/lib/hooks/use-console-shops";
import { can } from "@/lib/shop-access";

export default function VendorStaffPage() {
  const { activeShop, query } = useConsoleShops();
  if (query.isPending) return <p className="text-sm text-muted-foreground">Đang tải…</p>;
  if (!activeShop || !can(activeShop, "staff.manage")) {
    return (
      <p className="text-sm text-muted-foreground">
        Bạn không có quyền quản lý nhân viên của cửa hàng này.
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-6">
      <SectionHeader title="Nhân viên" subtitle={activeShop.shop_name} />
      <StaffManager key={activeShop.id} shop={activeShop} />
    </div>
  );
}
