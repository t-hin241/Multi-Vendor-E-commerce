import { ApiError, request } from "./api-client";

// Business reports (AF-20). Every figure is computed by the service that
// owns it (Order, Payment, Shipment); this module only fetches and words
// them. A source that did not answer is "unavailable", never zero, and
// ordered GMV is never added to captured money.

export type Ratio = { numerator: number; denominator: number; rate: number | null };
export type Amount = { value: number | null; available: boolean; reason?: string };
export type ReportGroup = "day" | "month";
export type ReportRange = { from: string; to: string; group: ReportGroup; vendor_id?: string };

export type OrderReport = {
  source: "order";
  definition_version: string;
  range: ReportRange;
  as_of: string;
  currency: string;
  placed_orders: number;
  vendor_orders: number;
  gmv_ordered: number;
  paid_vendor_orders: number;
  awaiting_fulfillment: number;
  cancellation: Ratio;
  paid_cancellation: Ratio;
  returns: Ratio;
  series: {
    bucket_start: string;
    placed_orders: number;
    vendor_orders: number;
    gmv_ordered: number;
  }[];
  shops?: {
    vendor_id: string;
    vendor_orders: number;
    gmv_ordered: number;
    cancellation: Ratio;
    returns: Ratio;
    insufficient_sample: boolean;
  }[];
};

export type PaymentReport = {
  source: "payment";
  definition_version: string;
  range: ReportRange;
  as_of: string;
  currency: string;
  scope: "marketplace" | "shop";
  captured: Amount;
  refunded: Amount;
  net_cash: Amount;
  settled_sales: Amount;
  paid_out: Amount;
  captured_count?: number;
  refund_count: number;
  estimated_captures: number;
  series: { bucket_start: string; captured: number | null; refunded: number }[];
};

export type HoursSummary = { average: number; p90: number; count: number } | null;

export type ShipmentReport = {
  source: "shipment";
  definition_version: string;
  range: ReportRange;
  as_of: string;
  fulfillable: number;
  handed_off: number;
  delivered: number;
  returned: number;
  lost: number;
  cancelled: number;
  late_handoff: Ratio;
  handoff_hours: HoursSummary;
  delivery_hours: HoursSummary;
  series: { bucket_start: string; fulfillable: number; handed_off: number; delivered: number }[];
};

export type ReportOwner = "order" | "payment" | "shipment";

export type ReportSource = {
  name: ReportOwner;
  status: "ok" | "unavailable" | "restricted";
  generated_at?: string;
  error?: string;
};

export type BusinessReport = {
  generated_at: string;
  definition_version: string;
  range: ReportRange;
  sources: ReportSource[];
  order: OrderReport | null;
  payment: PaymentReport | null;
  shipment: ShipmentReport | null;
};

export type ReportPeriod = { from: string; to: string };

// Inclusive calendar dates in Vietnam time become a half-open UTC period
// (at most 366 days); null when invalid.
export function reportPeriod(from: string, to: string): ReportPeriod | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(from) || !/^\d{4}-\d{2}-\d{2}$/.test(to)) return null;
  const start = new Date(`${from}T00:00:00+07:00`);
  const end = new Date(new Date(`${to}T00:00:00+07:00`).getTime() + 86_400_000);
  if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || end <= start)
    return null;
  if (end.getTime() - start.getTime() > 366 * 86_400_000) return null;
  if (
    new Date(`${from}T00:00:00Z`).toISOString().slice(0, 10) !== from ||
    new Date(`${to}T00:00:00Z`).toISOString().slice(0, 10) !== to
  )
    return null;
  return { from: start.toISOString(), to: end.toISOString() };
}

// Today in Vietnam as YYYY-MM-DD, shifted by days.
export function vietnamDate(offsetDays = 0, now = new Date()): string {
  return new Date(now.getTime() + 7 * 3_600_000 + offsetDays * 86_400_000)
    .toISOString()
    .slice(0, 10);
}

export const getAdminBusinessReport = (
  token: string,
  period: ReportPeriod,
  group: ReportGroup,
  vendorId?: string,
) =>
  request<BusinessReport>("/api/admin/business-reports", {
    token,
    query: { ...period, group, vendor_id: vendorId || undefined },
  });

const ownerSegment: Record<ReportOwner, string> = {
  order: "orders",
  payment: "payments",
  shipment: "shipments",
};

// Where an owner serves a report: the admin route, or the shop-scoped one
// (Payment names the shop in the path, the others take vendor_id).
function reportPath(owner: ReportOwner, scope: "admin" | "shop", vendorId?: string): string {
  if (scope === "admin") return `/api/${ownerSegment[owner]}/admin/business-reports`;
  if (owner === "payment")
    return `/api/payments/vendor/${encodeURIComponent(vendorId ?? "")}/business-reports`;
  return `/api/${ownerSegment[owner]}/vendor/business-reports`;
}

function failure(name: ReportOwner, err: unknown): ReportSource {
  if (err instanceof ApiError && (err.code === "permission_denied" || err.status === 403)) {
    return { name, status: "restricted", error: err.code };
  }
  return { name, status: "unavailable", error: err instanceof ApiError ? err.code : "unavailable" };
}

// The shop's own report: each owner asked in parallel; an owner the person
// may not read (no finance.read for money) is "restricted".
export async function getShopBusinessReport(
  token: string,
  vendorId: string,
  period: ReportPeriod,
  group: ReportGroup,
  canReadMoney: boolean,
): Promise<BusinessReport> {
  const query = { ...period, group, vendor_id: vendorId };
  const owners: ReportOwner[] = ["order", "payment", "shipment"];
  const results = await Promise.all(
    owners.map(async (name) => {
      try {
        if (name === "payment" && !canReadMoney) throw new ApiError(403, "permission_denied", "");
        const report = await request<OrderReport | PaymentReport | ShipmentReport>(
          reportPath(name, "shop", vendorId),
          { token, query },
        );
        return { name, report, error: null };
      } catch (error) {
        return { name, report: null, error };
      }
    }),
  );
  const out: BusinessReport = {
    generated_at: new Date().toISOString(),
    definition_version: "",
    range: { ...period, group, vendor_id: vendorId },
    sources: [],
    order: null,
    payment: null,
    shipment: null,
  };
  for (const { name, report, error } of results) {
    if (!report) {
      out.sources.push(failure(name, error));
      continue;
    }
    out.sources.push({ name, status: "ok", generated_at: report.as_of });
    out.definition_version ||= report.definition_version;
    if (name === "order") out.order = report as OrderReport;
    if (name === "payment") out.payment = report as PaymentReport;
    if (name === "shipment") out.shipment = report as ShipmentReport;
  }
  // Never show two definition versions side by side unlabeled.
  for (const name of owners) {
    const report = out[name];
    if (report && out.definition_version && report.definition_version !== out.definition_version) {
      out[name] = null;
      out.sources = out.sources.map((s) =>
        s.name === name
          ? { name, status: "unavailable", error: `definition ${report.definition_version}` }
          : s,
      );
    }
  }
  return out;
}

export type DrilldownPage = {
  metric: string;
  cutoff: string;
  definition_version: string;
  items: DrilldownRow[];
  next_cursor?: string;
};

// The union of the owners' rows: ids and the facts the figure counted.
export type DrilldownRow = {
  id: string;
  kind?: string;
  order_id?: string;
  vendor_order_id?: string;
  vendor_id?: string;
  status?: string;
  subtotal_amount?: number;
  amount?: number;
  currency?: string;
  paid?: boolean;
  created_at?: string;
  recorded_at?: string;
  shipped_at?: string;
  delivered_at?: string;
  estimated?: boolean;
};

export type DrilldownRequest = {
  owner: ReportOwner;
  metric: string;
  cutoff: string;
  range: ReportRange;
  scope: "admin" | "shop";
  cursor?: string;
};

export const getDrilldown = (token: string, d: DrilldownRequest) =>
  request<DrilldownPage>(`${reportPath(d.owner, d.scope, d.range.vendor_id)}/drilldown`, {
    token,
    query: {
      from: d.range.from,
      to: d.range.to,
      group: d.range.group,
      vendor_id: d.range.vendor_id,
      metric: d.metric,
      cutoff: d.cutoff,
      cursor: d.cursor,
      limit: 20,
    },
  });

export function formatRate(r: Ratio): string {
  if (r.rate === null) return "—";
  return `${(r.rate * 100).toLocaleString("vi-VN", { maximumFractionDigits: 1 })}%`;
}
