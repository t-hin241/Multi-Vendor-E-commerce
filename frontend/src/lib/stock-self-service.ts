import { request } from "./api-client";

export type StockPolicy = {
  enabled: boolean;
  feature_enabled: boolean;
  version: number;
  limits: { receipt: number };
};
export type Adjustment = {
  item_id: string;
  kind: "receipt" | "damage" | "correction" | "count";
  quantity_delta: number;
  physical_count?: number;
  reason: string;
  evidence_ref: string;
  expected_version: number;
};
export type Receipt = {
  operation_id: string;
  status: string;
  status_url: string;
  available: number;
  reserved: number;
  on_hand: number;
  version: number;
};
export type Movement = {
  id: string;
  kind: string;
  quantity_delta: number;
  reserved_delta: number;
  reason: string;
  evidence_ref: string;
  created_at: string;
};
export type Work = Adjustment & {
  first_approved_by?: string;
  id: string;
  vendor_id: string;
  status: string;
  policy_version: number;
};

export type AlertWork = { id: string; item_id: string; vendor_id: string; attempts: number };
export const alertWork = (token: string, cursor = "") =>
  request<Page<AlertWork>>("/api/inventory/admin/stock-alerts", { token, query: { cursor } });
export const retryAlert = (token: string, key: string, id: string, reason: string) =>
  request("/api/inventory/admin/stock-alerts/" + id + "/retry", {
    token,
    method: "POST",
    headers: { "Idempotency-Key": key },
    json: { reason },
  });
export type Page<T> = { items: T[]; next_cursor?: string };
const base = "/api/inventory";
export const policy = (token: string, vendor: string, admin = false) =>
  request<StockPolicy>(
    admin
      ? `${base}/admin/vendors/${vendor}/self-service-policy`
      : `${base}/vendor/self-service-policy`,
    { token, query: admin ? undefined : { vendor_id: vendor } },
  );
export const adjust = (token: string, key: string, input: Adjustment) =>
  request<Receipt>(
    `${base}/vendor/${input.kind === "count" ? "stock-counts" : "stock-adjustments"}`,
    { token, method: "POST", headers: { "Idempotency-Key": key }, json: input },
  );
export const alerts = (
  token: string,
  key: string,
  item: string,
  input: { enabled: boolean; low_stock_threshold: number; expected_version: number },
) =>
  request(`${base}/vendor/items/${item}/alert-settings`, {
    token,
    method: "PUT",
    headers: { "Idempotency-Key": key },
    json: input,
  });
export const history = (token: string, item: string, cursor = "") =>
  request<Page<Movement>>(`${base}/vendor/movements`, { token, query: { item_id: item, cursor } });
export const work = (token: string, cursor = "") =>
  request<Page<Work>>(`${base}/admin/stock-adjustments`, { token, query: { cursor } });
export const setPolicy = (
  token: string,
  key: string,
  vendor: string,
  input: {
    enabled: boolean;
    limits: { receipt: number };
    reason: string;
    expected_version: number;
  },
) =>
  request<StockPolicy>(`${base}/admin/vendors/${vendor}/self-service-policy`, {
    token,
    method: "POST",
    headers: { "Idempotency-Key": key },
    json: input,
  });
export const decide = (token: string, key: string, item: Work, decision: string, reason: string) =>
  request<Receipt>(`${base}/admin/stock-adjustments/${item.id}/decisions`, {
    token,
    method: "POST",
    headers: { "Idempotency-Key": key },
    json: { decision, reason, expected_version: item.expected_version },
  });
