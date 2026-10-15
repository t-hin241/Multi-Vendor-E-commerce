"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { type ReportGroup, reportPeriod, vietnamDate } from "@/lib/business-reports";

export type ReportFilterValue = { from: string; to: string; group: ReportGroup; vendorId: string };

const TEXT = {
  en: {
    from: "From",
    to: "To",
    group: "Group by",
    day: "Day",
    month: "Month",
    shop: "Shop id (optional)",
    apply: "Show",
    invalid: "Choose valid dates (at most 366 days) and a valid shop id.",
    zone: "Dates are in Vietnam time.",
  },
  vi: {
    from: "Từ ngày",
    to: "Đến ngày",
    group: "Nhóm theo",
    day: "Ngày",
    month: "Tháng",
    shop: "Mã shop",
    apply: "Xem",
    invalid: "Chọn ngày hợp lệ, tối đa 366 ngày.",
    zone: "Ngày theo giờ Việt Nam.",
  },
};

export function defaultReportFilter(): ReportFilterValue {
  return { from: vietnamDate(-29), to: vietnamDate(0), group: "day", vendorId: "" };
}

// ReportFilters picks the period (inclusive Vietnam dates), the grouping
// and, for admins, one shop. Applied only on submit.
export function ReportFilters({
  value,
  onApply,
  locale,
  withShop,
}: {
  value: ReportFilterValue;
  onApply: (v: ReportFilterValue) => void;
  locale: "en" | "vi";
  withShop?: boolean;
}) {
  const t = TEXT[locale];
  const [draft, setDraft] = useState(value);
  const [error, setError] = useState<string | null>(null);
  return (
    <form
      className="flex flex-wrap items-end gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        const vendorId = draft.vendorId.trim();
        if (
          !reportPeriod(draft.from, draft.to) ||
          (vendorId &&
            !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(vendorId))
        ) {
          setError(t.invalid);
          return;
        }
        setError(null);
        onApply({ ...draft, vendorId });
      }}
    >
      <div className="flex flex-col gap-1">
        <Label htmlFor="report-from">{t.from}</Label>
        <Input
          id="report-from"
          type="date"
          value={draft.from}
          onChange={(e) => setDraft({ ...draft, from: e.target.value })}
        />
      </div>
      <div className="flex flex-col gap-1">
        <Label htmlFor="report-to">{t.to}</Label>
        <Input
          id="report-to"
          type="date"
          value={draft.to}
          onChange={(e) => setDraft({ ...draft, to: e.target.value })}
        />
      </div>
      <div className="flex flex-col gap-1">
        <Label htmlFor="report-group">{t.group}</Label>
        <select
          id="report-group"
          className="h-9 rounded-md border bg-background px-2 text-sm"
          value={draft.group}
          onChange={(e) => setDraft({ ...draft, group: e.target.value as ReportGroup })}
        >
          <option value="day">{t.day}</option>
          <option value="month">{t.month}</option>
        </select>
      </div>
      {withShop && (
        <div className="flex flex-col gap-1">
          <Label htmlFor="report-shop">{t.shop}</Label>
          <Input
            id="report-shop"
            value={draft.vendorId}
            onChange={(e) => setDraft({ ...draft, vendorId: e.target.value })}
            className="w-80 max-w-full"
          />
        </div>
      )}
      <Button type="submit">{t.apply}</Button>
      <p className="w-full text-xs text-muted-foreground">{t.zone}</p>
      {error && (
        <p role="alert" className="w-full text-sm text-destructive">
          {error}
        </p>
      )}
    </form>
  );
}
