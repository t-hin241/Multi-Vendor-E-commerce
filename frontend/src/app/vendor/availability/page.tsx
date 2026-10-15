"use client";

import { SectionHeader } from "@/components/section-header";
import { VacationSchedule } from "@/components/vendor/vacation-schedule";
import { useConsoleShops } from "@/lib/hooks/use-console-shops";
import { can } from "@/lib/shop-access";

export default function VendorAvailabilityPage() {
  const { activeShop, query } = useConsoleShops();
  if (query.isPending) return <p className="text-sm text-muted-foreground">Đang tải…</p>;
  if (query.isError || !activeShop) return <p role="alert">Không tải được cửa hàng.</p>;
  return (
    <div className="flex flex-col gap-6">
      <SectionHeader title="Tạm nghỉ bán" subtitle={activeShop.shop_name} />
      <VacationSchedule
        key={activeShop.id}
        vendorId={activeShop.id}
        canWrite={can(activeShop, "shop.availability.write")}
      />
    </div>
  );
}
