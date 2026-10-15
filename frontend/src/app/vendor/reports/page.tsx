"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { BusinessReportView } from "@/components/reports/business-report-view";
import { defaultReportFilter, ReportFilters } from "@/components/reports/report-filters";
import { SectionHeader } from "@/components/section-header";
import { useAuth } from "@/lib/auth-context";
import { getShopBusinessReport, reportPeriod } from "@/lib/business-reports";
import { describeApiError } from "@/lib/errors";
import { useConsoleShops } from "@/lib/hooks/use-console-shops";
import { can } from "@/lib/shop-access";

// The shop's own business report (AF-20): same definitions as the admin's,
// always scoped to the active shop. Money needs finance.read; the shop's
// money statement and export stay on the statements page (AF-14).
export default function VendorReportsPage() {
  const { activeShop, query } = useConsoleShops();
  const { callWithAuth } = useAuth();
  const [filter, setFilter] = useState(defaultReportFilter);
  const period = reportPeriod(filter.from, filter.to);
  const vendorId = activeShop?.id ?? "";
  const money = can(activeShop, "finance.read");
  const allowed = can(activeShop, "analytics.read");
  const report = useQuery({
    queryKey: ["shop-business-report", vendorId, money, filter],
    queryFn: () =>
      callWithAuth((token) => getShopBusinessReport(token, vendorId, period!, filter.group, money)),
    enabled: !!period && !!vendorId && allowed,
  });
  if (query.isPending) return <p className="text-sm text-muted-foreground">Đang tải…</p>;
  if (!activeShop || !allowed)
    return <p role="alert">Bạn cần quyền xem tổng quan bán hàng của cửa hàng.</p>;
  const allDisabled = report.data?.sources.every((s) => s.error === "feature_disabled");
  return (
    <div className="flex flex-col gap-4">
      <SectionHeader title="Báo cáo kinh doanh" subtitle={activeShop.shop_name} />
      <ReportFilters value={filter} onApply={setFilter} locale="vi" />
      {money && (
        <p className="text-xs text-muted-foreground">
          Tiền của shop theo từng giao dịch và file xuất xem tại{" "}
          <Link href="/vendor/statements" className="underline">
            Sao kê tài chính
          </Link>
          .
        </p>
      )}
      {report.isPending && <p className="text-sm text-muted-foreground">Đang tải…</p>}
      {report.error && (
        <p role="alert" className="text-sm text-destructive">
          {describeApiError(report.error, "Không tải được báo cáo.")}
        </p>
      )}
      {allDisabled && (
        <p className="text-sm text-muted-foreground">Sàn chưa bật báo cáo kinh doanh.</p>
      )}
      {report.data && !allDisabled && (
        <BusinessReportView key={vendorId} report={report.data} scope="shop" locale="vi" />
      )}
    </div>
  );
}
