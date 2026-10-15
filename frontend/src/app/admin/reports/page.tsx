"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { BusinessReportView } from "@/components/reports/business-report-view";
import { defaultReportFilter, ReportFilters } from "@/components/reports/report-filters";
import { SectionHeader } from "@/components/section-header";
import { ApiError } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { getAdminBusinessReport, reportPeriod } from "@/lib/business-reports";
import { describeApiError } from "@/lib/errors";

// Business reports (AF-20): Order, Payment and Shipment figures for the
// marketplace or one shop, each with its own as_of. Admin only reads.
export default function AdminReportsPage() {
  const { callWithAuth } = useAuth();
  const [filter, setFilter] = useState(defaultReportFilter);
  const period = reportPeriod(filter.from, filter.to);
  const report = useQuery({
    queryKey: ["admin-business-report", filter],
    queryFn: () =>
      callWithAuth((token) =>
        getAdminBusinessReport(token, period!, filter.group, filter.vendorId),
      ),
    enabled: !!period,
  });
  const disabled = report.error instanceof ApiError && report.error.code === "feature_disabled";
  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Business reports"
        subtitle="Ordered value, money and fulfillment come from their own services and are never added together."
      />
      <ReportFilters value={filter} onApply={setFilter} locale="en" withShop />
      {report.isPending && <p className="text-sm text-muted-foreground">Loading…</p>}
      {disabled && (
        <p className="text-sm text-muted-foreground">Business reports are not enabled.</p>
      )}
      {report.error && !disabled && (
        <p role="alert" className="text-sm text-destructive">
          {describeApiError(report.error, "Could not load the report.")}
        </p>
      )}
      {report.data && <BusinessReportView report={report.data} scope="admin" locale="en" />}
    </div>
  );
}
