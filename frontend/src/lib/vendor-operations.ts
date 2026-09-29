import { request } from "./api-client";
import type { Vendor } from "./api-client";

export type PayoutAccount = {
  id: string;
  vendor_id: string;
  version: number;
  bank_bin: string;
  last4: string;
  status: "pending" | "verified" | "rejected" | "disabled";
  is_default: boolean;
  rejection_reason?: string;
  created_at: string;
};
export type PayoutDetails = {
  account_id: string;
  version: number;
  bank_bin: string;
  account_number: string;
  account_name: string;
};
export type Amount = { value: number | null; available: boolean; reason?: string };
export type Report = {
  gross_ordered: Amount;
  captured: Amount;
  refunded: Amount;
  eligible: Amount;
  paid_out: Amount;
  total_orders: number;
  awaiting_fulfillment: number;
  as_of: string;
  stale: boolean;
};
export type Dashboard = {
  orders: Report | null;
  payments: Report | null;
  orders_unavailable: boolean;
  payments_unavailable: boolean;
  as_of: string;
};
const path = (vendorId: string, admin: boolean) =>
  admin
    ? `/api/vendor/admin/shops/${vendorId}/payout-accounts`
    : `/api/vendor/${vendorId}/payout-accounts`;
export function listAccounts(token: string, vendorId: string, admin = false, offset = 0) {
  return request<PayoutAccount[]>(`${path(vendorId, admin)}?limit=20&offset=${offset}`, { token });
}
export function submitAccount(
  token: string,
  vendorId: string,
  form: { bank_bin: string; account_number: string; account_name: string },
) {
  return request<PayoutAccount>(path(vendorId, false), { token, method: "POST", json: form });
}
export function decideAccount(
  token: string,
  vendorId: string,
  account: PayoutAccount,
  verify: boolean,
  reason: string,
) {
  return request<PayoutAccount>(`${path(vendorId, true)}/${account.id}/decision`, {
    token,
    method: "POST",
    json: { version: account.version, verify, reason },
  });
}
export function accountDetails(
  token: string,
  vendorId: string,
  account: PayoutAccount,
  purpose: string,
) {
  return request<PayoutDetails>(`${path(vendorId, true)}/${account.id}/details`, {
    token,
    method: "POST",
    json: { version: account.version, purpose },
  });
}
export function changeStatus(
  token: string,
  id: string,
  action: "suspend" | "restore",
  reason: string,
) {
  return request<Vendor>(`/api/vendor/admin/applications/${id}/${action}`, {
    token,
    method: "PATCH",
    json: { reason },
  });
}
export function resubmit(token: string, id: string) {
  return request<Vendor>(`/api/vendor/${id}/resubmit`, { token, method: "POST" });
}
export function replay(token: string, id: string) {
  return request(`/api/vendor/admin/applications/${id}/replay`, { token, method: "POST" });
}
export function dashboard(token: string, id: string, from: string, to: string) {
  return request<Dashboard>(
    `/api/vendor/${id}/dashboard?${new URLSearchParams({ from, to, currency: "VND" })}`,
    { token },
  );
}
