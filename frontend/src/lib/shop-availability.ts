import { request } from "./api-client";

// Shop vacation (AF-18). Vendor decides and enforces; the console only
// shows the schedule and sends what the owner chose. Times travel as UTC
// RFC3339 and are entered/shown in Vietnam time (UTC+7, no DST).

export type VacationInterval = {
  id: string;
  status: "scheduled" | "active";
  starts_at: string;
  ends_at?: string;
  message: string;
  paused_at?: string;
  updated_at: string;
};

export type ShopAvailability = {
  vendor_id: string;
  shop_status: string;
  accepting_orders: boolean;
  availability_version: number;
  sale_version: number;
  enforcement_pending: boolean;
  next_open_at?: string;
  current?: VacationInterval;
  enabled: boolean;
};

export type VacationRequest = {
  starts_at?: string;
  ends_at?: string;
  message: string;
  expected_version: number;
};

const path = (vendorId: string) => `/api/vendor/${encodeURIComponent(vendorId)}/availability`;

export const getShopAvailability = (token: string, vendorId: string) =>
  request<ShopAvailability>(path(vendorId), { token });

export const scheduleVacation = (token: string, vendorId: string, body: VacationRequest) =>
  request<ShopAvailability>(path(vendorId), { method: "PUT", token, json: body });

export const resumeShop = (token: string, vendorId: string, expectedVersion: number) =>
  request<ShopAvailability>(path(vendorId), {
    method: "DELETE",
    token,
    query: { expected_version: expectedVersion },
  });

// A datetime-local value ("2026-12-24T08:00") read as Vietnam time, as an
// RFC3339 instant with an explicit offset; "" stays undefined (= now / no
// end). null for anything unparseable.
export function vietnamInputToInstant(value: string): string | undefined | null {
  if (!value) return undefined;
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) return null;
  const d = new Date(`${value}:00+07:00`);
  if (!Number.isFinite(d.getTime())) return null;
  // Date normalizes impossible calendar dates (e.g. February 30).
  // Refuse those instead of silently scheduling a different day.
  if (instantToVietnamInput(d.toISOString()) !== value) return null;
  return d.toISOString();
}

// The datetime-local value showing an instant in Vietnam time.
export function instantToVietnamInput(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (!Number.isFinite(d.getTime())) return "";
  return new Date(d.getTime() + 7 * 3600_000).toISOString().slice(0, 16);
}

export function formatVietnamTime(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (!Number.isFinite(d.getTime())) return "";
  return d.toLocaleString("vi-VN", {
    timeZone: "Asia/Ho_Chi_Minh",
    dateStyle: "medium",
    timeStyle: "short",
  });
}

const ERRORS: Record<string, string> = {
  version_conflict:
    "Lịch tạm nghỉ vừa được thay đổi ở nơi khác. Đã tải lại; hãy kiểm tra rồi thử lại.",
  invalid_schedule: "Lịch tạm nghỉ không hợp lệ.",
  feature_disabled: "Tính năng tạm nghỉ chưa được bật cho sàn.",
  shop_not_selling:
    "Chỉ cửa hàng đã được duyệt (hoặc đang bị sàn tạm khóa) mới đặt lịch tạm nghỉ được.",
  permission_denied: "Bạn không có quyền bật/tắt trạng thái bán của cửa hàng.",
};

export function availabilityErrorMessage(code: string, fallback: string): string {
  if (code === "invalid_schedule") return fallback || "Lịch tạm nghỉ không hợp lệ.";
  return ERRORS[code] ?? fallback;
}
