"use client";

import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useAuth } from "@/lib/auth-context";
import {
  type Amount,
  type BusinessReport,
  type DrilldownRow,
  formatRate,
  getDrilldown,
  type Ratio,
  type ReportOwner,
  type ReportSource,
} from "@/lib/business-reports";
import { describeApiError } from "@/lib/errors";
import { formatMoney } from "@/lib/format";

// BusinessReportView shows the owners' figures side by side (AF-20): each
// panel carries its own as_of; a source that did not answer says so instead
// of zero; ordered GMV and captured money stay in separate panels. Clicking
// a figure lists the ids behind it at the same cutoff.

type Locale = "en" | "vi";

const TEXT = {
  en: {
    order: "Orders",
    payment: "Money (Payment)",
    shipment: "Fulfillment (Shipment)",
    asOf: "As of",
    unavailable: "Unavailable",
    restricted: "No permission",
    notZero: "This source did not answer; its figures are not shown (not zero).",
    restrictedHint: "You do not have the permission this source needs.",
    placed: "Orders placed",
    vendorOrders: "Shop packages (sub-orders)",
    gmv: "GMV ordered",
    gmvHint: "Merchandise before discounts, including unpaid and cancelled. Not money received.",
    paid: "Paid packages",
    awaiting: "Awaiting fulfillment",
    cancel: "Cancellation rate",
    paidCancel: "Cancelled after payment",
    returns: "Return rate (of paid)",
    captured: "Captured",
    refunded: "Refunded",
    net: "Net cash",
    settled: "Settled sales (ledger)",
    paidOut: "Paid out",
    estimated: "captures before this report started have an estimated time",
    fulfillable: "Packages fulfillable",
    handedOff: "Handed to carrier",
    late: "Late handoff (> 3 days)",
    delivered: "Delivered",
    returned: "Returned",
    lost: "Lost",
    cancelled: "Cancelled",
    handoffHours: "Handoff time (avg / p90)",
    deliveryHours: "Delivery time (avg / p90)",
    hours: "h",
    series: "By period",
    period: "Period",
    shops: "Busiest shops",
    shop: "Shop",
    insufficient: "Not enough data",
    drill: "Records behind",
    close: "Close",
    more: "Load more",
    none: "No records.",
    definition: "Definitions",
    reasons: {
      vendor_allocation_not_available:
        "A payment covers every shop of an order; a shop's share is not known.",
      shop_scope_only: "Shown for one shop only.",
      legacy_payout_currency_requires_review: "Older payouts without a currency need review.",
    } as Record<string, string>,
  },
  vi: {
    order: "Đơn hàng",
    payment: "Tiền (theo Thanh toán)",
    shipment: "Giao hàng",
    asOf: "Số liệu lúc",
    unavailable: "Không có số liệu",
    restricted: "Không có quyền",
    notZero: "Nguồn này không trả lời; số liệu bị ẩn (không phải bằng 0).",
    restrictedHint: "Bạn chưa có quyền mà nguồn này yêu cầu (tiền cần quyền xem tài chính).",
    placed: "Đơn đã đặt",
    vendorOrders: "Gói hàng của shop",
    gmv: "Giá trị hàng đã đặt (GMV)",
    gmvHint:
      "Giá hàng trước giảm giá, gồm cả đơn chưa thanh toán và đã hủy. Không phải tiền đã nhận.",
    paid: "Gói đã thanh toán",
    awaiting: "Chờ giao cho vận chuyển",
    cancel: "Tỉ lệ hủy",
    paidCancel: "Hủy sau thanh toán",
    returns: "Tỉ lệ trả hàng (trên gói đã thanh toán)",
    captured: "Đã thu",
    refunded: "Đã hoàn",
    net: "Thu thuần",
    settled: "Doanh thu đã ghi sổ",
    paidOut: "Đã chuyển cho shop",
    estimated: "giao dịch thu tiền cũ có thời điểm ước tính",
    fulfillable: "Gói sẵn sàng giao",
    handedOff: "Đã giao cho vận chuyển",
    late: "Giao vận chuyển trễ (> 3 ngày)",
    delivered: "Đã giao thành công",
    returned: "Hoàn về",
    lost: "Thất lạc",
    cancelled: "Đã hủy",
    handoffHours: "Thời gian chuẩn bị (TB / p90)",
    deliveryHours: "Thời gian vận chuyển (TB / p90)",
    hours: "giờ",
    series: "Theo kỳ",
    period: "Kỳ",
    shops: "Shop nhiều đơn nhất",
    shop: "Shop",
    insufficient: "Chưa đủ dữ liệu",
    drill: "Danh sách chi tiết",
    close: "Đóng",
    more: "Xem thêm",
    none: "Không có bản ghi.",
    definition: "Định nghĩa",
    reasons: {
      vendor_allocation_not_available:
        "Một lần thanh toán trả cho mọi shop của đơn; phần của riêng shop chưa xác định được. Xem sao kê để biết tiền của shop.",
      shop_scope_only: "Chỉ hiển thị khi xem một shop.",
      legacy_payout_currency_requires_review:
        "Có lần chuyển tiền cũ thiếu loại tiền, cần kiểm tra.",
    } as Record<string, string>,
  },
};

type Texts = (typeof TEXT)["en"];

function time(value: string | undefined, locale: Locale) {
  if (!value) return "—";
  return new Date(value).toLocaleString(locale === "vi" ? "vi-VN" : "en-GB", {
    timeZone: "Asia/Ho_Chi_Minh",
    dateStyle: "short",
    timeStyle: "short",
  });
}

function bucket(value: string, group: string, locale: Locale) {
  return new Date(value).toLocaleDateString(locale === "vi" ? "vi-VN" : "en-GB", {
    timeZone: "Asia/Ho_Chi_Minh",
    ...(group === "month" ? { month: "2-digit", year: "numeric" } : { dateStyle: "short" }),
  });
}

type DrillTarget = { owner: ReportOwner; metric: string; label: string; cutoff: string };

function Figure({
  label,
  value,
  hint,
  onClick,
}: {
  label: string;
  value: string;
  hint?: string;
  onClick?: () => void;
}) {
  const body = (
    <>
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-xl font-semibold tabular-nums">{value}</span>
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </>
  );
  return onClick ? (
    <button
      type="button"
      onClick={onClick}
      className="flex flex-col items-start gap-0.5 rounded-md border p-3 text-left transition-colors hover:bg-muted/50"
    >
      {body}
    </button>
  ) : (
    <div className="flex flex-col gap-0.5 rounded-md border p-3">{body}</div>
  );
}

function ratioText(r: Ratio) {
  return `${formatRate(r)} (${r.numerator}/${r.denominator})`;
}

function amountText(a: Amount, t: Texts): { value: string; hint?: string } {
  if (a.available && a.value !== null) return { value: formatMoney(a.value) };
  return { value: "—", hint: (a.reason && t.reasons[a.reason]) || t.unavailable };
}

function SourceState({ source, t, locale }: { source?: ReportSource; t: Texts; locale: Locale }) {
  if (!source) return null;
  if (source.status === "ok") {
    return (
      <span className="text-xs font-normal text-muted-foreground">
        {t.asOf} {time(source.generated_at, locale)}
      </span>
    );
  }
  return (
    <Badge variant="outline">{source.status === "restricted" ? t.restricted : t.unavailable}</Badge>
  );
}

function Panel({
  title,
  source,
  t,
  locale,
  children,
}: {
  title: string;
  source?: ReportSource;
  t: Texts;
  locale: Locale;
  children: React.ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2 text-base">
          {title}
          <SourceState source={source} t={t} locale={locale} />
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {source && source.status !== "ok" ? (
          <p className="text-sm text-muted-foreground">
            {source.status === "restricted" ? t.restrictedHint : t.notZero}
            {source.error ? ` (${source.error})` : ""}
          </p>
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}

function SeriesTable({
  headers,
  rows,
  t,
}: {
  headers: string[];
  rows: { key: string; cells: string[] }[];
  t: Texts;
}) {
  if (rows.length === 0) return null;
  return (
    <details>
      <summary className="cursor-pointer text-sm font-medium">{t.series}</summary>
      <div className="mt-2 overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-muted-foreground">
              <th className="py-1 pr-3 font-normal">{t.period}</th>
              {headers.map((h) => (
                <th key={h} className="py-1 pr-3 text-right font-normal">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.key} className="border-t">
                {r.cells.map((c, i) => (
                  <td
                    key={i}
                    className={i === 0 ? "py-1 pr-3" : "py-1 pr-3 text-right tabular-nums"}
                  >
                    {c}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </details>
  );
}

function DrilldownList({
  target,
  report,
  scope,
  t,
  locale,
  onClose,
}: {
  target: DrillTarget;
  report: BusinessReport;
  scope: "admin" | "shop";
  t: Texts;
  locale: Locale;
  onClose: () => void;
}) {
  const { callWithAuth } = useAuth();
  const query = useInfiniteQuery({
    queryKey: ["business-report-drill", scope, report.range, target],
    queryFn: ({ pageParam }) =>
      callWithAuth((token) =>
        getDrilldown(token, {
          owner: target.owner,
          metric: target.metric,
          cutoff: target.cutoff,
          range: report.range,
          scope,
          cursor: pageParam || undefined,
        }),
      ),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor,
  });
  const rows: DrilldownRow[] = query.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center justify-between gap-2 text-base">
          <span>
            {t.drill}: {target.label}{" "}
            <span className="text-xs font-normal text-muted-foreground">
              {t.asOf} {time(target.cutoff, locale)}
            </span>
          </span>
          <Button size="sm" variant="outline" onClick={onClose}>
            {t.close}
          </Button>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-2 text-sm">
        {query.isPending && <p className="text-muted-foreground">…</p>}
        {query.error && <p className="text-destructive">{describeApiError(query.error)}</p>}
        {query.data && rows.length === 0 && <p className="text-muted-foreground">{t.none}</p>}
        {rows.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <tbody>
                {rows.map((r) => (
                  <tr key={r.id} className="border-t">
                    <td className="py-1 pr-3 font-mono">{r.id}</td>
                    <td className="py-1 pr-3">{r.kind ?? r.status}</td>
                    <td className="py-1 pr-3">{r.status && r.kind ? r.status : ""}</td>
                    <td className="py-1 pr-3 font-mono">{r.order_id ?? r.vendor_order_id ?? ""}</td>
                    <td className="py-1 pr-3 text-right tabular-nums">
                      {r.subtotal_amount !== undefined
                        ? formatMoney(r.subtotal_amount)
                        : r.amount !== undefined
                          ? formatMoney(r.amount)
                          : ""}
                    </td>
                    <td className="py-1 pr-3">
                      {time(r.recorded_at ?? r.created_at, locale)}
                      {r.estimated ? " *" : ""}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {query.hasNextPage && (
          <div>
            <Button
              size="sm"
              variant="outline"
              disabled={query.isFetchingNextPage}
              onClick={() => query.fetchNextPage()}
            >
              {t.more}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export function BusinessReportView({
  report,
  scope,
  locale,
  shopLabel,
}: {
  report: BusinessReport;
  scope: "admin" | "shop";
  locale: Locale;
  // shopLabel names a shop id in the ranking (admin), when known.
  shopLabel?: (vendorId: string) => string;
}) {
  const t = TEXT[locale];
  const [drill, setDrill] = useState<DrillTarget | null>(null);
  const src = (name: ReportOwner) => report.sources.find((s) => s.name === name);
  const { order, payment, shipment } = report;
  const group = report.range.group;
  const open = (owner: ReportOwner, metric: string, label: string, cutoff?: string) =>
    cutoff ? () => setDrill({ owner, metric, label, cutoff }) : undefined;

  return (
    <div className="flex flex-col gap-4">
      <p className="text-xs text-muted-foreground">
        {t.definition}: {report.definition_version || "—"}
      </p>

      <Panel title={t.order} source={src("order")} t={t} locale={locale}>
        {order && (
          <>
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <Figure
                label={t.placed}
                value={order.placed_orders.toLocaleString()}
                onClick={open("order", "placed_orders", t.placed, order.as_of)}
              />
              <Figure
                label={t.vendorOrders}
                value={order.vendor_orders.toLocaleString()}
                onClick={open("order", "vendor_orders", t.vendorOrders, order.as_of)}
              />
              <Figure
                label={t.gmv}
                value={formatMoney(order.gmv_ordered, order.currency)}
                hint={t.gmvHint}
              />
              <Figure
                label={t.paid}
                value={order.paid_vendor_orders.toLocaleString()}
                onClick={open("order", "paid_vendor_orders", t.paid, order.as_of)}
              />
              <Figure
                label={t.awaiting}
                value={order.awaiting_fulfillment.toLocaleString()}
                onClick={open("order", "awaiting_fulfillment", t.awaiting, order.as_of)}
              />
              <Figure
                label={t.cancel}
                value={ratioText(order.cancellation)}
                onClick={open("order", "cancelled", t.cancel, order.as_of)}
              />
              <Figure
                label={t.paidCancel}
                value={ratioText(order.paid_cancellation)}
                onClick={open("order", "paid_cancelled", t.paidCancel, order.as_of)}
              />
              <Figure
                label={t.returns}
                value={ratioText(order.returns)}
                onClick={open("order", "returned", t.returns, order.as_of)}
              />
            </div>
            <SeriesTable
              t={t}
              headers={[t.placed, t.vendorOrders, t.gmv]}
              rows={order.series.map((b) => ({
                key: b.bucket_start,
                cells: [
                  bucket(b.bucket_start, group, locale),
                  String(b.placed_orders),
                  String(b.vendor_orders),
                  formatMoney(b.gmv_ordered, order.currency),
                ],
              }))}
            />
            {order.shops && order.shops.length > 0 && (
              <div className="overflow-x-auto">
                <p className="mb-1 text-sm font-medium">{t.shops}</p>
                <table className="w-full text-sm">
                  <thead>
                    <tr className="text-left text-xs text-muted-foreground">
                      <th className="py-1 pr-3 font-normal">{t.shop}</th>
                      <th className="py-1 pr-3 text-right font-normal">{t.vendorOrders}</th>
                      <th className="py-1 pr-3 text-right font-normal">{t.gmv}</th>
                      <th className="py-1 pr-3 text-right font-normal">{t.cancel}</th>
                      <th className="py-1 pr-3 text-right font-normal">{t.returns}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {order.shops.map((s) => (
                      <tr key={s.vendor_id} className="border-t">
                        <td className="py-1 pr-3">
                          {shopLabel ? shopLabel(s.vendor_id) : s.vendor_id}
                        </td>
                        <td className="py-1 pr-3 text-right tabular-nums">{s.vendor_orders}</td>
                        <td className="py-1 pr-3 text-right tabular-nums">
                          {formatMoney(s.gmv_ordered)}
                        </td>
                        <td className="py-1 pr-3 text-right">
                          {s.insufficient_sample ? t.insufficient : formatRate(s.cancellation)}
                        </td>
                        <td className="py-1 pr-3 text-right">
                          {s.insufficient_sample ? t.insufficient : formatRate(s.returns)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )}
      </Panel>

      <Panel title={t.payment} source={src("payment")} t={t} locale={locale}>
        {payment && (
          <>
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              {payment.scope === "marketplace" ? (
                <>
                  <Figure
                    label={t.captured}
                    {...amountText(payment.captured, t)}
                    onClick={open("payment", "captured", t.captured, payment.as_of)}
                  />
                  <Figure
                    label={t.refunded}
                    {...amountText(payment.refunded, t)}
                    onClick={open("payment", "refunded", t.refunded, payment.as_of)}
                  />
                  <Figure label={t.net} {...amountText(payment.net_cash, t)} />
                </>
              ) : (
                <>
                  <Figure label={t.captured} {...amountText(payment.captured, t)} />
                  <Figure
                    label={t.refunded}
                    {...amountText(payment.refunded, t)}
                    onClick={open("payment", "refunded", t.refunded, payment.as_of)}
                  />
                  <Figure label={t.settled} {...amountText(payment.settled_sales, t)} />
                  <Figure label={t.paidOut} {...amountText(payment.paid_out, t)} />
                </>
              )}
            </div>
            {payment.estimated_captures > 0 && (
              <p className="text-xs text-muted-foreground">
                * {payment.estimated_captures} {t.estimated}
              </p>
            )}
            <SeriesTable
              t={t}
              headers={payment.scope === "marketplace" ? [t.captured, t.refunded] : [t.refunded]}
              rows={payment.series.map((b) => ({
                key: b.bucket_start,
                cells: [
                  bucket(b.bucket_start, group, locale),
                  ...(payment.scope === "marketplace" ? [formatMoney(b.captured ?? 0)] : []),
                  formatMoney(b.refunded),
                ],
              }))}
            />
          </>
        )}
      </Panel>

      <Panel title={t.shipment} source={src("shipment")} t={t} locale={locale}>
        {shipment && (
          <>
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <Figure
                label={t.fulfillable}
                value={String(shipment.fulfillable)}
                onClick={open("shipment", "fulfillable", t.fulfillable, shipment.as_of)}
              />
              <Figure
                label={t.handedOff}
                value={String(shipment.handed_off)}
                onClick={open("shipment", "handed_off", t.handedOff, shipment.as_of)}
              />
              <Figure
                label={t.late}
                value={ratioText(shipment.late_handoff)}
                onClick={open("shipment", "late_handoff", t.late, shipment.as_of)}
              />
              <Figure
                label={t.delivered}
                value={String(shipment.delivered)}
                onClick={open("shipment", "delivered", t.delivered, shipment.as_of)}
              />
              <Figure
                label={t.returned}
                value={String(shipment.returned)}
                onClick={open("shipment", "returned", t.returned, shipment.as_of)}
              />
              <Figure
                label={t.lost}
                value={String(shipment.lost)}
                onClick={open("shipment", "lost", t.lost, shipment.as_of)}
              />
              <Figure
                label={t.cancelled}
                value={String(shipment.cancelled)}
                onClick={open("shipment", "cancelled", t.cancelled, shipment.as_of)}
              />
              <Figure
                label={t.handoffHours}
                value={
                  shipment.handoff_hours
                    ? `${shipment.handoff_hours.average.toFixed(1)} / ${shipment.handoff_hours.p90.toFixed(1)} ${t.hours}`
                    : "—"
                }
              />
              <Figure
                label={t.deliveryHours}
                value={
                  shipment.delivery_hours
                    ? `${shipment.delivery_hours.average.toFixed(1)} / ${shipment.delivery_hours.p90.toFixed(1)} ${t.hours}`
                    : "—"
                }
              />
            </div>
            <SeriesTable
              t={t}
              headers={[t.fulfillable, t.handedOff, t.delivered]}
              rows={shipment.series.map((b) => ({
                key: b.bucket_start,
                cells: [
                  bucket(b.bucket_start, group, locale),
                  String(b.fulfillable),
                  String(b.handed_off),
                  String(b.delivered),
                ],
              }))}
            />
          </>
        )}
      </Panel>

      {drill && (
        <DrilldownList
          key={`${drill.owner}-${drill.metric}`}
          target={drill}
          report={report}
          scope={scope}
          t={t}
          locale={locale}
          onClose={() => setDrill(null)}
        />
      )}
    </div>
  );
}
