import { request } from "./api-client";

export type StatementEntry = {
  id: string;
  vendor_order_id?: string;
  type: string;
  amount: number;
  currency: string;
  source_ref: string;
  recorded_at: string;
  occurred_at: string;
  occurred_at_estimated: boolean;
  eligible_at: string;
};
export type Statement = {
  vendor_id: string;
  filters: { from: string; to: string; currency: string };
  balances: Record<
    "opening" | "credit" | "debit" | "closing" | "held" | "not_eligible" | "payable" | "in_payout",
    number
  >;
  balances_available: boolean;
  gap_count: number;
  entries: StatementEntry[];
  next_cursor?: string;
  as_of: string;
  watermark: string | null;
};
export type StatementExport = {
  operation_id: string;
  status: "pending" | "processing" | "ready" | "failed" | "expired";
  expires_at: string;
  snapshot: Statement;
  checksum?: string;
};
export type StatementPayout = {
  id: string;
  batch_id: string;
  amount: number;
  currency: string;
  status: string;
  masked_destination: string;
  entry_refs: string[];
};
const path = (vendor: string, admin: boolean) =>
  `/api/payments/${admin ? "admin/" : ""}vendor/${encodeURIComponent(vendor)}`;

// Inclusive calendar dates in Vietnam become a half-open UTC interval.
export function statementPeriod(from: string, to: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(from) || !/^\d{4}-\d{2}-\d{2}$/.test(to)) return null;
  const start = new Date(`${from}T00:00:00+07:00`);
  const end = new Date(new Date(`${to}T00:00:00+07:00`).getTime() + 86400000);
  if (
    !Number.isFinite(start.getTime()) ||
    !Number.isFinite(end.getTime()) ||
    end <= start ||
    end.getTime() - start.getTime() > 366 * 86400000
  )
    return null;
  if (
    new Date(`${from}T00:00:00Z`).toISOString().slice(0, 10) !== from ||
    new Date(`${to}T00:00:00Z`).toISOString().slice(0, 10) !== to
  )
    return null;
  return { from: start.toISOString(), to: end.toISOString(), currency: "VND" };
}
export const statementCapability = (token: string, vendor: string, admin = false) =>
  request<{ statements_enabled: boolean }>(`${path(vendor, admin)}/statement-capabilities`, {
    token,
  });
export const readStatement = (
  token: string,
  vendor: string,
  period: NonNullable<ReturnType<typeof statementPeriod>>,
  cursor: string,
  admin = false,
) =>
  request<Statement>(`${path(vendor, admin)}/statements`, {
    token,
    query: { ...period, cursor, limit: 20 },
  });
export const createStatementExport = (
  token: string,
  vendor: string,
  period: NonNullable<ReturnType<typeof statementPeriod>>,
  key: string,
) =>
  request<StatementExport>(`${path(vendor, false)}/statement-exports`, {
    token,
    method: "POST",
    json: { ...period, format: "csv" },
    headers: { "Idempotency-Key": key },
  });
export const getStatementExport = (token: string, vendor: string, id: string) =>
  request<StatementExport>(`${path(vendor, false)}/statement-exports/${encodeURIComponent(id)}`, {
    token,
  });
export const statementDownload = (token: string, vendor: string, id: string) =>
  request<{ url: string; expires_at: string }>(
    `${path(vendor, false)}/statement-exports/${encodeURIComponent(id)}/download-links`,
    { token, method: "POST" },
  );
export const statementPayout = (token: string, vendor: string, id: string, admin = false) =>
  request<StatementPayout>(`${path(vendor, admin)}/payouts/${encodeURIComponent(id)}`, { token });
